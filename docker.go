package traefikkop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/traefik/traefik/v2/pkg/provider/docker"
)

// Copied from traefik. See docker provider package for original impl

type dockerCache struct {
	client  client.APIClient
	list    []types.Container
	details map[string]types.ContainerJSON
}

// Must be 0 for unix socket?
// Non-zero throws an error
const defaultTimeout = time.Duration(0)

func createDockerClient(endpoint string) (client.APIClient, error) {
	opts, err := getClientOpts(endpoint)
	if err != nil {
		return nil, err
	}

	httpHeaders := map[string]string{
		"User-Agent": "traefik-kop " + Version,
	}
	opts = append(opts, client.WithHTTPHeaders(httpHeaders))

	apiVersion := docker.DockerAPIVersion
	SwarmMode := false
	if SwarmMode {
		apiVersion = docker.SwarmAPIVersion
	}
	opts = append(opts, client.WithVersion(apiVersion))

	return client.NewClientWithOpts(opts...)
}

func getClientOpts(endpoint string) ([]client.Opt, error) {
	// we currently do not support ssh, so skip helper setup
	opts := []client.Opt{
		client.WithHost(endpoint),
		client.WithTimeout(time.Duration(defaultTimeout)),
	}
	return opts, nil
}

// looks up the docker container by finding the matching service or router traefik label
func (dc *dockerCache) findContainerByServiceName(svcType string, svcName string, routerName string) (types.ContainerJSON, error) {
	svcName = strings.TrimSuffix(svcName, "@docker")
	routerName = strings.TrimSuffix(routerName, "@docker")

	if dc.list == nil {
		var err error
		dc.list, err = dc.client.ContainerList(context.Background(), container.ListOptions{})
		if err != nil {
			return types.ContainerJSON{}, errors.Wrap(err, "failed to list containers")
		}
	}

	for _, c := range dc.list {
		var container types.ContainerJSON
		var ok bool
		if container, ok = dc.details[c.ID]; !ok {
			var err error
			container, err = dc.client.ContainerInspect(context.Background(), c.ID)
			if err != nil {
				return types.ContainerJSON{}, errors.Wrapf(err, "failed to inspect container %s", c.ID)
			}
			dc.details[c.ID] = container
		}

		// normalize labels
		labels := make(map[string]string, len(container.Config.Labels))
		for k, v := range container.Config.Labels {
			labels[strings.ToLower(k)] = v
		}
		container.Config.Labels = labels

		// check labels
		svcNeedle := fmt.Sprintf("traefik.%s.services.%s.", svcType, svcName)
		routerNeedle := fmt.Sprintf("traefik.%s.routers.%s.", svcType, routerName)
		for k := range container.Config.Labels {
			if strings.HasPrefix(k, svcNeedle) || (routerName != "" && strings.HasPrefix(k, routerNeedle)) {
				logrus.Debugf("found container '%s' (%s) for service '%s'", container.Name, container.ID, svcName)
				return container, nil
			}
		}
	}

	return types.ContainerJSON{}, errors.Errorf("service label not found for %s/%s", svcType, svcName)
}

// Check if the port is explicitly set via label
func isPortSet(container types.ContainerJSON, svcType string, svcName string) string {
	svcName = strings.TrimSuffix(svcName, "@docker")
	needle := fmt.Sprintf("traefik.%s.services.%s.loadbalancer.server.port", svcType, svcName)
	return container.Config.Labels[needle]
}

// getPortBinding checks the docker container config for a port binding for the
// service. Currently this will only work if a single port is mapped/exposed.
//
// i.e., it looks for the following from a docker-compose service:
//
// ports:
//   - 5555:5555
//
// If more than one port is bound (e.g., for a service like minio), then this
// detection will fail. Instead, the user should explicitly set the port in the
// label.
func getPortBinding(container types.ContainerJSON) (string, error) {
	logrus.Debugln("looking for port in host config bindings")
	numBindings := len(container.HostConfig.PortBindings)
	logrus.Debugf("found %d host-port bindings", numBindings)
	if numBindings > 1 {
		return "", errors.Errorf("found more than one host-port binding for container '%s' (%s)", container.Name, portBindingString(container.HostConfig.PortBindings))
	}
	for _, v := range container.HostConfig.PortBindings {
		if len(v) > 1 {
			return "", errors.Errorf("found more than one host-port binding for container '%s' (%s)", container.Name, portBindingString(container.HostConfig.PortBindings))
		}
		if v[0].HostPort != "" && v[0].HostIP == "" {
			logrus.Debugf("found host-port binding %s", v[0].HostPort)
			return v[0].HostPort, nil
		}
	}

	// check for a randomly set port via --publish-all
	logrus.Debugln("looking for port in network settings")
	if container.NetworkSettings != nil && len(container.NetworkSettings.Ports) == 1 {
		for _, v := range container.NetworkSettings.Ports {
			if len(v) > 0 {
				port := v[0].HostPort
				if port != "" {
					if len(v) > 1 {
						logrus.Warnf("found %d port(s); trying the first one", len(v))
					}
					return port, nil
				}
			}
		}
	}

	return "", errors.Errorf("no host-port binding found for container '%s'", container.Name)
}

// Convert host:container port binding map to a compact printable string
func portBindingString(bindings nat.PortMap) string {
	s := []string{}
	for k, v := range bindings {
		if len(v) > 0 {
			containerPort := strings.TrimSuffix(string(k), "/tcp")
			containerPort = strings.TrimSuffix(string(containerPort), "/udp")
			s = append(s, fmt.Sprintf("%s:%s", v[0].HostPort, containerPort))
		}
	}
	return strings.Join(s, ", ")
}
