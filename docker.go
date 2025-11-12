package traefikkop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Copied from traefik. See docker provider package for original impl

type dockerCache struct {
	client  client.APIClient
	list    []container.Summary
	details map[string]container.InspectResponse
	expires time.Time
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

	// Use API version negotiation for compatibility with both old and new Docker daemons
	// This fixes the issue with Docker CE 29.0.0 which requires minimum API version 1.44
	opts = append(opts, client.WithAPIVersionNegotiation())

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

// populate the cache with the current list of running containers and their details.
//
// Cache expires after 30 seconds.
func (dc *dockerCache) populate() error {
	if time.Now().After(dc.expires) {
		dc.list = nil
		dc.details = make(map[string]container.InspectResponse)
	}

	if dc.list == nil {
		var err error
		dc.list, err = dc.client.ContainerList(context.Background(), container.ListOptions{})
		if err != nil {
			return errors.Wrap(err, "failed to list containers")
		}
	}

	for _, c := range dc.list {
		var container container.InspectResponse
		var ok bool
		if container, ok = dc.details[c.ID]; !ok {
			var err error
			container, err = dc.client.ContainerInspect(context.Background(), c.ID)
			if err != nil {
				return errors.Wrapf(err, "failed to inspect container %s", c.ID)
			}
			dc.details[c.ID] = container
		}

		// normalize labels
		labels := make(map[string]string, len(container.Config.Labels))
		for k, v := range container.Config.Labels {
			labels[strings.ToLower(k)] = v
		}
		container.Config.Labels = labels
	}

	dc.expires = time.Now().Add(5 * time.Second) // cache expires in 30 seconds

	return nil
}

// looks up the docker container by finding the matching service or router traefik label
func (dc *dockerCache) findContainerByServiceName(svcType string, svcName string, routerName string) (container.InspectResponse, error) {
	err := dc.populate()
	if err != nil {
		return container.InspectResponse{}, err
	}

	svcName = stripDocker(svcName)
	routerName = stripDocker(routerName)

	for _, container := range dc.details {
		// check labels
		svcNeedle := fmt.Sprintf("traefik.%s.services.%s.", svcType, svcName)
		routerNeedle := fmt.Sprintf("traefik.%s.routers.%s.", svcType, routerName)
		for k := range container.Config.Labels {
			if strings.HasPrefix(k, svcNeedle) || (routerName != "" && strings.HasPrefix(k, routerNeedle)) {
				log.Debug().Msgf("found container '%s' (%s) for service '%s'", container.Name, container.ID, svcName)
				return container, nil
			}
		}
	}

	return container.InspectResponse{}, errors.Errorf("service label not found for %s/%s", svcType, svcName)
}

// Check if the port is explicitly set via label
func isPortSet(container container.InspectResponse, svcType string, svcName string) string {
	svcName = stripDocker(svcName)
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
func getPortBinding(container container.InspectResponse) (string, error) {
	log.Debug().Msg("looking for port in host config bindings")
	numBindings := len(container.HostConfig.PortBindings)
	log.Debug().Msgf("found %d host-port bindings", numBindings)
	if numBindings > 1 {
		return "", errors.Errorf("found more than one host-port binding for container '%s' (%s)", container.Name, portBindingString(container.HostConfig.PortBindings))
	}
	for _, v := range container.HostConfig.PortBindings {
		if len(v) > 1 {
			return "", errors.Errorf("found more than one host-port binding for container '%s' (%s)", container.Name, portBindingString(container.HostConfig.PortBindings))
		}
		if v[0].HostPort != "" {
			log.Debug().Msgf("found host-port binding %s", v[0].HostPort)
			return v[0].HostPort, nil
		}
	}

	// check for a randomly set port via --publish-all
	if container.NetworkSettings != nil && len(container.NetworkSettings.Ports) == 1 {
		log.Debug().Msg("looking for [randomly set] port in network settings")
		for _, v := range container.NetworkSettings.Ports {
			if len(v) > 0 {
				port := v[0].HostPort
				if port != "" {
					if len(v) > 1 {
						log.Warn().Msgf("found %d port(s); trying the first one", len(v))
					}
					return port, nil
				}
			}
		}
	} else {
		log.Debug().Msg("skipping network settings check, no ports found")
	}

	return "", errors.Errorf("no host-port binding found for container '%s'", container.Name)
}

func logJSON(name string, v interface{}) {
	log.Debug().Func(func(e *zerolog.Event) {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			e.Msgf("failed to marshal: %s", err)
		} else {
			e.Msgf("json dump of %s", name)
			fmt.Println(string(data))
		}
	})
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

// stripDocker removes the @docker suffix from a service name.
// This is used to normalize service names when storing or retrieving them from the store.
func stripDocker(svcName string) string {
	return strings.TrimSuffix(svcName, "@docker")
}
