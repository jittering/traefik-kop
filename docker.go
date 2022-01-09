package traefikkop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"github.com/traefik/traefik/v2/pkg/provider/docker"
)

// Copied from traefik. See docker provider package for original impl

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

func findContainerByServiceName(dc client.APIClient, svcType string, svcName string, routerName string) (types.ContainerJSON, error) {
	list, err := dc.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		return types.ContainerJSON{}, errors.Wrap(err, "failed to list containers")
	}
	for _, c := range list {
		container, err := dc.ContainerInspect(context.Background(), c.ID)
		if err != nil {
			return types.ContainerJSON{}, errors.Wrapf(err, "failed to inspect container %s", c.ID)
		}
		// check labels
		svcNeedle := fmt.Sprintf("traefik.%s.services.%s", svcType, svcName)
		routerNeedle := fmt.Sprintf("traefik.%s.routers.%s", svcType, routerName)
		for k := range container.Config.Labels {
			if strings.Contains(k, svcNeedle) {
				return container, nil
			} else if routerName != "" && strings.Contains(k, routerNeedle) {
				return container, nil
			}
		}
	}

	return types.ContainerJSON{}, errors.Errorf("service label not found for %s/%s", svcType, svcName)
}

// Check if the port is explicitly set via label
func isPortSet(container types.ContainerJSON, svcType string, svcName string) bool {
	needle := fmt.Sprintf("traefik.%s.services.%s.loadbalancer.server.port", svcType, svcName)
	for k := range container.Config.Labels {
		if k == needle {
			return true
		}
	}
	return false
}

func getPortBinding(container types.ContainerJSON) (string, error) {
	numBindings := len(container.HostConfig.PortBindings)
	if numBindings > 1 {
		return "", errors.Errorf("found more than one host-port binding for container '%s'", container.Name)
	}
	for _, v := range container.HostConfig.PortBindings {
		if len(v) > 1 {
			return "", errors.Errorf("found more than one host-port binding for container '%s'", container.Name)
		}
		return v[0].HostPort, nil
	}
	return "", errors.New("no host-port binding found")
}
