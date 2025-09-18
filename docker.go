package traefikkop

import (
	"context"
	"encoding/json"
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
	client         client.APIClient
	list           []types.Container
	details        map[string]types.ContainerJSON
	originalLabels map[string]map[string]string // Store original labels for namespace checking
	expires        time.Time
	namespaces     []string // Add namespace information for label processing
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

// populate the cache with the current list of running containers and their details.
//
// Cache expires after 30 seconds.
func (dc *dockerCache) populate() error {
	if time.Now().After(dc.expires) {
		dc.list = nil
		dc.details = make(map[string]types.ContainerJSON)
		// Don't reset originalLabels - we want to preserve the original labels from first load
		if dc.originalLabels == nil {
			dc.originalLabels = make(map[string]map[string]string)
		}
	}

	if dc.list == nil {
		var err error
		dc.list, err = dc.client.ContainerList(context.Background(), container.ListOptions{})
		if err != nil {
			return errors.Wrap(err, "failed to list containers")
		}
		logrus.Debugf("populate: listed %d containers", len(dc.list))
	}

	// Clean up originalLabels for containers that no longer exist
	if len(dc.originalLabels) > 0 {
		currentContainerIDs := make(map[string]bool)
		for _, c := range dc.list {
			currentContainerIDs[c.ID] = true
		}

		for containerID := range dc.originalLabels {
			if !currentContainerIDs[containerID] {
				delete(dc.originalLabels, containerID)
			}
		}
	}

	for _, c := range dc.list {
		var container types.ContainerJSON
		var ok bool
		if container, ok = dc.details[c.ID]; !ok {
			var err error
			container, err = dc.client.ContainerInspect(context.Background(), c.ID)
			if err != nil {
				return errors.Wrapf(err, "failed to inspect container %s", c.ID)
			}
			dc.details[c.ID] = container
			logrus.Debugf("populate: inspected %s", container.ID)
		}

		// Always refresh original labels from live Docker inspect to avoid drift
		refreshedOriginal := make(map[string]string, len(container.Config.Labels))
		for k, v := range container.Config.Labels {
			refreshedOriginal[strings.ToLower(k)] = v
		}
		dc.originalLabels[container.ID] = refreshedOriginal
		logrus.Debugf("populate: refreshed original labels for %s: %d labels", container.ID, len(refreshedOriginal))

		// Process labels with namespace-aware logic, but do NOT write back
		// to the cached container. Consumers should use dc.processLabelsForNamespaces(container)
		// at read sites instead of relying on mutated labels.
	}

	dc.expires = time.Now().Add(30 * time.Second) // cache expires in 30 seconds

	return nil
}

// processLabelsForNamespaces processes container labels based on namespace-specific prefixes
// It implements the label isolation mechanism:
// - If kop.$namespace.traefik.* labels exist for our namespaces, use only those (converted to traefik.*)
// - Otherwise, use all traefik.* labels for backward compatibility, but only if namespace filtering allows it
func (dc *dockerCache) processLabelsForNamespaces(container types.ContainerJSON) map[string]string {
	labels := make(map[string]string)

	// Always use original labels to check for kop labels, since container.Config.Labels
	// might have been processed already and no longer contain the original kop.* labels
	originalLabels := dc.getOriginalLabels(container.ID)
	if originalLabels == nil {
		// Fallback: create original labels from current labels if not stored yet
		originalLabels = make(map[string]string)
		for k, v := range container.Config.Labels {
			originalLabels[strings.ToLower(k)] = v
		}
	}

	// Check if any kop.$namespace.traefik.* labels exist for our namespaces
	hasKopLabels := false
	for _, targetNamespace := range dc.namespaces {
		kopPrefix := fmt.Sprintf("kop.%s.traefik.", targetNamespace)
		for k := range originalLabels {
			if strings.HasPrefix(k, kopPrefix) {
				hasKopLabels = true
				break
			}
		}
		if hasKopLabels {
			break
		}
	}

	if hasKopLabels {
		// New mode: only read kop.$namespace.traefik.* and convert to traefik.*
		for _, targetNamespace := range dc.namespaces {
			kopPrefix := fmt.Sprintf("kop.%s.traefik.", targetNamespace)
			for k, v := range originalLabels {
				if strings.HasPrefix(k, kopPrefix) {
					// Remove kop.$namespace. prefix, convert to standard traefik.* label
					newKey := strings.TrimPrefix(k, fmt.Sprintf("kop.%s.", targetNamespace))
					labels[newKey] = v
				}
			}
		}
	} else {
		// Backward compatibility mode: use all traefik.* labels from original labels
		for k, v := range originalLabels {
			if strings.HasPrefix(k, "traefik.") {
				labels[k] = v
			}
		}
	}

	return labels
}

// getOriginalLabels returns the original (unprocessed) labels for a container
func (dc *dockerCache) getOriginalLabels(containerID string) map[string]string {
	if dc.originalLabels == nil {
		return nil
	}
	return dc.originalLabels[containerID]
}

// looks up the docker container by finding the matching service or router traefik label
func (dc *dockerCache) findContainerByServiceName(svcType string, svcName string, routerName string) (types.ContainerJSON, error) {
	// Use a consistent snapshot. Populate only if empty to avoid mid-run drift.
	if dc.list == nil || len(dc.details) == 0 {
		if err := dc.populate(); err != nil {
			return types.ContainerJSON{}, err
		}
	}

	svcName = stripDocker(svcName)
	routerName = stripDocker(routerName)

	for _, container := range dc.details {
		// Check in original labels first (for services that might have been filtered)
		originalLabels := dc.getOriginalLabels(container.ID)
		if originalLabels != nil {
			svcNeedle := fmt.Sprintf("traefik.%s.services.%s.", svcType, svcName)
			routerNeedle := fmt.Sprintf("traefik.%s.routers.%s.", svcType, routerName)

			// Check original labels
			for k := range originalLabels {
				if strings.HasPrefix(k, svcNeedle) || (routerName != "" && strings.HasPrefix(k, routerNeedle)) {
					logrus.Debugf("found container '%s' (%s) for service '%s' in original labels", container.Name, container.ID, svcName)
					return container, nil
				}
			}

			// Also check for kop labels that could generate this service
			for _, targetNS := range dc.namespaces {
				kopSvcNeedle := fmt.Sprintf("kop.%s.traefik.%s.services.%s.", targetNS, svcType, svcName)
				kopRouterNeedle := fmt.Sprintf("kop.%s.traefik.%s.routers.%s.", targetNS, svcType, routerName)
				for k := range originalLabels {
					if strings.HasPrefix(k, kopSvcNeedle) || (routerName != "" && strings.HasPrefix(k, kopRouterNeedle)) {
						logrus.Debugf("found container '%s' (%s) for service '%s' in kop labels", container.Name, container.ID, svcName)
						return container, nil
					}
				}
			}
		}

		// Fallback to processed labels
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
		if v[0].HostPort != "" {
			logrus.Debugf("found host-port binding %s", v[0].HostPort)
			return v[0].HostPort, nil
		}
	}

	// check for a randomly set port via --publish-all
	if container.NetworkSettings != nil && len(container.NetworkSettings.Ports) == 1 {
		logrus.Debugln("looking for [randomly set] port in network settings")
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
	} else {
		logrus.Debug("skipping network settings check, no ports found")
	}

	return "", errors.Errorf("no host-port binding found for container '%s'", container.Name)
}

func logJSON(name string, v interface{}) {
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			logrus.Debug("failed to marshal: ", err)
		} else {
			logrus.Debugf("json dump of %s", name)
			fmt.Println(string(data))
		}
	}
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
