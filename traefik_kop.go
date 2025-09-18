package traefikkop

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
	ptypes "github.com/traefik/paerser/types"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/config/static"
	"github.com/traefik/traefik/v2/pkg/provider"
	"github.com/traefik/traefik/v2/pkg/provider/aggregator"
	"github.com/traefik/traefik/v2/pkg/provider/docker"
	"github.com/traefik/traefik/v2/pkg/safe"
	"github.com/traefik/traefik/v2/pkg/server"
	"golang.org/x/exp/slices"
)

var Version = ""

// const defaultThrottleDuration = 5 * time.Second

// newDockerProvider creates a provider via yaml config or returns a default
// which connects to docker over a unix socket
func newDockerProvider(config Config) *docker.Provider {
	dp, err := loadDockerConfig(config.DockerConfig)
	if err != nil {
		logrus.Fatalf("failed to read docker config: %s", err)

	}

	if dp == nil {
		dp = &docker.Provider{}
	}

	// set defaults
	if dp.Endpoint == "" {
		dp.Endpoint = config.DockerHost
	}
	if dp.HTTPClientTimeout.String() != "0s" && strings.HasPrefix(dp.Endpoint, "unix://") {
		// force to 0 for unix socket
		dp.HTTPClientTimeout = ptypes.Duration(defaultTimeout)
	}
	if dp.SwarmModeRefreshSeconds.String() == "0s" {
		dp.SwarmModeRefreshSeconds = ptypes.Duration(15 * time.Second)
	}
	dp.Watch = true // always

	return dp
}

func createConfigHandler(config Config, store TraefikStore, dp *docker.Provider, dockerClient client.APIClient) func(conf dynamic.Configuration) {
	return func(conf dynamic.Configuration) {
		logrus.Infoln("refreshing traefik-kop configuration")

		dc := &dockerCache{
			client:         dockerClient,
			list:           nil,
			details:        make(map[string]types.ContainerJSON),
			originalLabels: make(map[string]map[string]string),
			namespaces:     config.Namespace,
		}

		// Enhance configuration with kop.namespace services/routers before filtering
		enhanceConfigurationWithKopLabels(dc, &conf, config.Namespace)

		filterServices(dc, &conf, config.Namespace)

		if !dp.UseBindPortIP {
			// if not using traefik's built in IP/Port detection, use our own
			replaceIPs(dc, &conf, config.BindIP)
		}

		mergeLoadBalancers(dc, &conf, store)

		err := store.Store(conf)
		if err != nil {
			panic(err)
		}
	}
}

func Start(config Config) {
	dp := newDockerProvider(config)
	store := NewRedisStore(config.Hostname, config.Addr, config.RedisTTL, config.Pass, config.DB)
	err := store.Ping()
	if err != nil {
		if strings.Contains(err.Error(), config.Addr) {
			logrus.Fatalf("failed to connect to redis: %s", err)
		}
		logrus.Fatalf("failed to connect to redis at %s: %s", config.Addr, err)
	}

	providers := &static.Providers{
		Docker: dp,
	}
	providerAggregator := aggregator.NewProviderAggregator(*providers)

	dockerClient, err := createDockerClient(config.DockerHost)
	if err != nil {
		logrus.Fatalf("failed to create docker client: %s", err)
	}

	ctx := context.Background()
	routinesPool := safe.NewPool(ctx)

	handleConfigChange := createConfigHandler(config, store, dp, dockerClient)

	pollingDockerProvider := newDockerProvider(config)
	pollingDockerProvider.Watch = false
	multiProvider := NewMultiProvider([]provider.Provider{
		providerAggregator,
		NewPollingProvider(
			time.Second*time.Duration(config.PollInterval),
			pollingDockerProvider,
			store,
		),
	})

	// initialize all providers
	err = multiProvider.Init()
	if err != nil {
		panic(err)
	}

	watcher := server.NewConfigurationWatcher(
		routinesPool,
		multiProvider,
		[]string{},
		"docker",
	)
	watcher.AddListener(handleConfigChange)
	watcher.Start()

	select {} // go forever
}

func keepContainer(ns []string, container types.ContainerJSON, dc *dockerCache) bool {
	// Get original labels to check for namespace information
	originalLabels := dc.getOriginalLabels(container.ID)
	if originalLabels == nil {
		// Fallback to processed labels if original not available
		originalLabels = container.Config.Labels
	}

	// New logic: check for kop.$namespace.traefik.* labels in original labels
	for _, targetNS := range ns {
		kopPrefix := fmt.Sprintf("kop.%s.traefik.", targetNS)
		for k := range originalLabels {
			if strings.HasPrefix(strings.ToLower(k), kopPrefix) {
				return true
			}
		}
	}

	// Backward compatibility: check kop.namespace label
	containerNS := splitStringArr(originalLabels["kop.namespace"])
	if len(ns) == 0 && len(containerNS) == 0 {
		return true
	}
	for _, v := range ns {
		if slices.Contains(containerNS, v) {
			return true
		}
	}
	return false
}

// keepServiceForProtocol checks if a specific service should be kept based on namespace rules for a given protocol
// For containers with new kop.$namespace.traefik.* syntax, only services generated from those labels should be kept
func keepServiceForProtocol(ns []string, svcName string, protocol string, container types.ContainerJSON, dc *dockerCache) bool {
	// Get original labels to check for namespace information
	originalLabels := dc.getOriginalLabels(container.ID)
	if originalLabels == nil {
		originalLabels = container.Config.Labels
	}

	svcNameWithoutDocker := stripDocker(svcName)

	// Check if container has new syntax labels
	hasKopLabels := false
	for _, targetNS := range ns {
		kopPrefix := fmt.Sprintf("kop.%s.traefik.", targetNS)
		for k := range originalLabels {
			if strings.HasPrefix(strings.ToLower(k), kopPrefix) {
				hasKopLabels = true
				break
			}
		}
		if hasKopLabels {
			break
		}
	}

	if hasKopLabels {
		// New mode: only allow services that come from kop.$namespace.traefik.* labels
		// Check if this service was generated from kop labels by looking for matching service name in kop labels
		for _, targetNS := range ns {
			kopServicePrefix := fmt.Sprintf("kop.%s.traefik.%s.services.%s.", targetNS, protocol, svcNameWithoutDocker)
			for k := range originalLabels {
				if strings.HasPrefix(strings.ToLower(k), kopServicePrefix) {
					return true
				}
			}
		}
		return false
	} else {
		// Backward compatibility mode: use existing keepContainer logic
		return keepContainer(ns, container, dc)
	}
}

// keepService is a convenience wrapper for HTTP services
func keepService(ns []string, svcName string, container types.ContainerJSON, dc *dockerCache) bool {
	return keepServiceForProtocol(ns, svcName, "http", container, dc)
}

// keepRouterForProtocol checks if a specific router should be kept based on namespace rules for a given protocol
// For containers with new kop.$namespace.traefik.* syntax, only routers generated from those labels should be kept
func keepRouterForProtocol(ns []string, routerName string, protocol string, container types.ContainerJSON, dc *dockerCache) bool {
	// Get original labels to check for namespace information
	originalLabels := dc.getOriginalLabels(container.ID)
	if originalLabels == nil {
		originalLabels = container.Config.Labels
	}

	// Check if container has new syntax labels
	hasKopLabels := false
	for _, targetNS := range ns {
		kopPrefix := fmt.Sprintf("kop.%s.traefik.", targetNS)
		for k := range originalLabels {
			if strings.HasPrefix(strings.ToLower(k), kopPrefix) {
				hasKopLabels = true
				break
			}
		}
		if hasKopLabels {
			break
		}
	}

	if hasKopLabels {
		// New mode: only allow routers that come from kop.$namespace.traefik.* labels
		// Check if this router was generated from kop labels by looking for matching router name in kop labels
		routerNameWithoutDocker := stripDocker(routerName)
		for _, targetNS := range ns {
			kopRouterPrefix := fmt.Sprintf("kop.%s.traefik.%s.routers.%s.", targetNS, protocol, routerNameWithoutDocker)
			for k := range originalLabels {
				if strings.HasPrefix(strings.ToLower(k), kopRouterPrefix) {
					return true
				}
			}
		}
		return false
	} else {
		// Backward compatibility mode: use existing keepContainer logic
		return keepContainer(ns, container, dc)
	}
}

// keepRouter is a convenience wrapper for HTTP routers
func keepRouter(ns []string, routerName string, container types.ContainerJSON, dc *dockerCache) bool {
	return keepRouterForProtocol(ns, routerName, "http", container, dc)
}

func joinNamespaces(ns []string) string {
	return strings.Join(ns, ", ")
}

// enhanceConfigurationWithKopLabels adds services and routers from kop.$namespace.traefik.* labels
// This ensures that containers with new syntax have their services/routers available for filtering
func enhanceConfigurationWithKopLabels(dc *dockerCache, conf *dynamic.Configuration, ns []string) {
	err := dc.populate()
	if err != nil {
		logrus.Errorf("failed to populate docker cache: %s", err)
		return
	}

	// Initialize HTTP sections if needed
	if conf.HTTP == nil {
		conf.HTTP = &dynamic.HTTPConfiguration{}
	}
	if conf.HTTP.Services == nil {
		conf.HTTP.Services = make(map[string]*dynamic.Service)
	}
	if conf.HTTP.Routers == nil {
		conf.HTTP.Routers = make(map[string]*dynamic.Router)
	}

	// Process each container
	for _, container := range dc.details {
		originalLabels := dc.getOriginalLabels(container.ID)
		if originalLabels == nil {
			continue
		}

		// Check if this container has kop.$namespace.traefik.* labels
		hasKopLabels := false
		for _, targetNS := range ns {
			kopPrefix := fmt.Sprintf("kop.%s.traefik.", targetNS)
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

		if !hasKopLabels {
			continue
		}

		// Convert kop labels to traefik configuration
		processedLabels := dc.processLabelsForNamespaces(container)
		// debug: label injection count (kept minimal)
		logrus.Debugf("enhance: %s injected %d kop labels", container.ID, len(processedLabels))

		// Generate additional configuration from the processed labels
		// This simulates what Traefik Docker Provider would do
		enhanceHTTPConfiguration(conf.HTTP, processedLabels, container)
	}
}

// enhanceHTTPConfiguration adds HTTP services and routers based on processed labels
func enhanceHTTPConfiguration(conf *dynamic.HTTPConfiguration, labels map[string]string, container types.ContainerJSON) {
	// Find all service definitions
	services := make(map[string]map[string]string)
	routers := make(map[string]map[string]string)

	for k, v := range labels {
		if strings.HasPrefix(k, "traefik.http.services.") {
			parts := strings.Split(k, ".")
			if len(parts) >= 4 {
				serviceName := parts[3]
				property := strings.Join(parts[4:], ".")
				if services[serviceName] == nil {
					services[serviceName] = make(map[string]string)
				}
				services[serviceName][property] = v
			}
		} else if strings.HasPrefix(k, "traefik.http.routers.") {
			parts := strings.Split(k, ".")
			if len(parts) >= 4 {
				routerName := parts[3]
				property := strings.Join(parts[4:], ".")
				if routers[routerName] == nil {
					routers[routerName] = make(map[string]string)
				}
				routers[routerName][property] = v
			}
		}
	}

	// Create HTTP services
	for serviceName, props := range services {
		if _, exists := conf.Services[serviceName+"@docker"]; !exists {
			service := &dynamic.Service{
				LoadBalancer: &dynamic.ServersLoadBalancer{
					PassHostHeader: func() *bool { b := true; return &b }(),
					Servers:        []dynamic.Server{},
				},
			}

			// Basic configuration - just add a server with the container IP
			// The port will be determined later by replaceIPs/replacePorts
			if container.NetworkSettings != nil && len(container.NetworkSettings.Networks) > 0 {
				for _, network := range container.NetworkSettings.Networks {
					port := "80" // default port
					if portStr, ok := props["loadbalancer.server.port"]; ok {
						port = portStr
					}
					server := dynamic.Server{
						URL: fmt.Sprintf("http://%s:%s", network.IPAddress, port),
					}
					service.LoadBalancer.Servers = append(service.LoadBalancer.Servers, server)
					break // Use first network
				}
			}

			conf.Services[serviceName+"@docker"] = service
		}
	}

	// Create HTTP routers
	for routerName, props := range routers {
		if _, exists := conf.Routers[routerName+"@docker"]; !exists {
			router := &dynamic.Router{}

			if rule, ok := props["rule"]; ok {
				router.Rule = rule
			}
			if service, ok := props["service"]; ok {
				router.Service = service
			} else {
				router.Service = routerName // default service name
			}
			if entrypoints, ok := props["entrypoints"]; ok {
				router.EntryPoints = strings.Split(entrypoints, ",")
			}

			conf.Routers[routerName+"@docker"] = router
		}
	}
}

// filter out services by namespace
// ns is traefik-kop's configured namespace to match against.
func filterServices(dc *dockerCache, conf *dynamic.Configuration, ns []string) {
	if conf.HTTP != nil && conf.HTTP.Services != nil {
		for svcName := range conf.HTTP.Services {
			container, err := dc.findContainerByServiceName("http", svcName, getRouterOfService(conf, svcName, "http"))
			if err != nil {
				logrus.Warnf("failed to find container for service '%s': %s", svcName, err)
				continue
			}
			if !keepServiceForProtocol(ns, svcName, "http", container, dc) {
				logrus.Infof("skipping service %s (not in namespace %s)", svcName, joinNamespaces(ns))
				delete(conf.HTTP.Services, svcName)
			}
		}
	}

	if conf.HTTP != nil && conf.HTTP.Routers != nil {
		for routerName, router := range conf.HTTP.Routers {
			svcName := router.Service
			container, err := dc.findContainerByServiceName("http", svcName, routerName)
			if err != nil {
				logrus.Warnf("failed to find container for service '%s': %s", svcName, err)
				continue
			}
			if !keepRouterForProtocol(ns, routerName, "http", container, dc) {
				logrus.Infof("skipping router %s (not in namespace %s)", routerName, joinNamespaces(ns))
				delete(conf.HTTP.Routers, routerName)
			}
		}
	}

	if conf.TCP != nil && conf.TCP.Services != nil {
		for svcName := range conf.TCP.Services {
			container, err := dc.findContainerByServiceName("tcp", svcName, getRouterOfService(conf, svcName, "tcp"))
			if err != nil {
				logrus.Warnf("failed to find container for service '%s': %s", svcName, err)
				continue
			}
			if !keepServiceForProtocol(ns, svcName, "tcp", container, dc) {
				logrus.Infof("skipping service %s (not in namespace %s)", svcName, joinNamespaces(ns))
				delete(conf.TCP.Services, svcName)
			}
		}
	}

	if conf.TCP != nil && conf.TCP.Routers != nil {
		for routerName, router := range conf.TCP.Routers {
			svcName := router.Service
			container, err := dc.findContainerByServiceName("tcp", svcName, routerName)
			if err != nil {
				logrus.Warnf("failed to find container for service '%s': %s", svcName, err)
				continue
			}
			if !keepRouterForProtocol(ns, routerName, "tcp", container, dc) {
				logrus.Infof("skipping router %s (not in namespace %s)", routerName, joinNamespaces(ns))
				delete(conf.TCP.Routers, routerName)
			}
		}
	}

	if conf.UDP != nil && conf.UDP.Services != nil {
		for svcName := range conf.UDP.Services {
			container, err := dc.findContainerByServiceName("udp", svcName, getRouterOfService(conf, svcName, "udp"))
			if err != nil {
				logrus.Warnf("failed to find container for service '%s': %s", svcName, err)
				continue
			}
			if !keepServiceForProtocol(ns, svcName, "udp", container, dc) {
				logrus.Warnf("service %s is not running: removing from config", svcName)
				delete(conf.UDP.Services, svcName)
			}
		}
	}

	if conf.UDP != nil && conf.UDP.Routers != nil {
		for routerName, router := range conf.UDP.Routers {
			svcName := router.Service
			container, err := dc.findContainerByServiceName("udp", svcName, routerName)
			if err != nil {
				logrus.Warnf("failed to find container for service '%s': %s", svcName, err)
				continue
			}
			if !keepRouterForProtocol(ns, routerName, "udp", container, dc) {
				logrus.Infof("skipping router %s (not in namespace %s)", routerName, joinNamespaces(ns))
				delete(conf.UDP.Routers, routerName)
			}
		}
	}
}

// replaceIPs for all service endpoints
//
// By default, traefik finds the local/internal docker IP for each container.
// Since we are exposing these services to an external node/server, we need
// to replace any IPs with the correct IP for this server, as configured at startup.
//
// When using CNI, as indicated by the container label `traefik.docker.network`,
// we will stick with the container IP.
func replaceIPs(dc *dockerCache, conf *dynamic.Configuration, ip string) {
	// modify HTTP URLs
	if conf.HTTP != nil && conf.HTTP.Services != nil {
		for svcName, svc := range conf.HTTP.Services {
			log := logrus.WithFields(logrus.Fields{"service": svcName, "service-type": "http"})
			log.Debugf("found http service: %s", svcName)
			for i := range svc.LoadBalancer.Servers {
				effectiveIP := ip
				if v, changed := getKopOverrideBinding(dc, conf, "http", svcName, ip); changed {
					effectiveIP = v
				} else {
					// For kop-generated services, prefer host IP; only use container IP for non-kop services
					if !isServiceFromKop(dc, conf, "http", svcName) {
						effectiveIP = getContainerNetworkIP(dc, conf, "http", svcName, ip)
					}
				}

				// replace ip into URLs
				server := &svc.LoadBalancer.Servers[i]
				if server.URL != "" {
					// the URL IP will initially refer to the container-local IP
					//
					// the URL Port will initially be the configured port number, either
					// explicitly via traefik label or detected by traefik. We cannot
					// determine how the port was set without looking at the traefik
					// labels ourselves.
					log.Debugf("using load balancer URL for port detection: %s", server.URL)
					u, _ := url.Parse(server.URL)
					p := getContainerPort(dc, conf, "http", svcName, u.Port())
					if p != "" {
						u.Host = effectiveIP + ":" + p
					} else {
						u.Host = effectiveIP
					}
					server.URL = u.String()
				} else {
					scheme := "http"
					if server.Scheme != "" {
						scheme = server.Scheme
					}
					server.URL = fmt.Sprintf("%s://%s", scheme, effectiveIP)
					port := getContainerPort(dc, conf, "http", svcName, server.Port)
					if port != "" {
						server.URL += ":" + server.Port
					}
				}
				log.Infof("publishing %s", server.URL)
			}

			if conf.HTTP.Routers != nil {
				for routerName, router := range conf.HTTP.Routers {
					if router.Service+"@docker" == svcName && (router.TLS == nil || strings.TrimSpace(router.TLS.CertResolver) == "") {
						log.Warnf("router %s has no TLS cert resolver", routerName)
					}
				}
			}
		}
	}

	// TCP
	if conf.TCP != nil && conf.TCP.Services != nil {
		for svcName, svc := range conf.TCP.Services {
			log := logrus.WithFields(logrus.Fields{"service": svcName, "service-type": "tcp"})
			log.Debugf("found tcp service: %s", svcName)
			for i := range svc.LoadBalancer.Servers {
				// override with container IP if we have a routable IP
				ip = getContainerNetworkIP(dc, conf, "tcp", svcName, ip)

				server := &svc.LoadBalancer.Servers[i]
				server.Port = getContainerPort(dc, conf, "tcp", svcName, server.Port)
				log.Debugf("using ip '%s' and port '%s' for %s", ip, server.Port, svcName)
				server.Address = ip
				if server.Port != "" {
					server.Address += ":" + server.Port
				}
				log.Infof("publishing %s", server.Address)
			}
		}
	}

	// UDP
	if conf.UDP != nil && conf.UDP.Services != nil {
		for svcName, svc := range conf.UDP.Services {
			log := logrus.WithFields(logrus.Fields{"service": svcName, "service-type": "udp"})
			log.Debugf("found udp service: %s", svcName)
			for i := range svc.LoadBalancer.Servers {
				// override with container IP if we have a routable IP
				ip = getContainerNetworkIP(dc, conf, "udp", svcName, ip)

				server := &svc.LoadBalancer.Servers[i]
				server.Port = getContainerPort(dc, conf, "udp", svcName, server.Port)
				log.Debugf("using ip '%s' and port '%s' for %s", ip, server.Port, svcName)
				server.Address = ip
				if server.Port != "" {
					server.Address += ":" + server.Port
				}
				log.Infof("publishing %s", server.Address)
			}
		}
	}
}

// Get the matching router name for the given service.
//
// It is possible that no traefik service was explicitly configured, only a
// router. In this case, we need to use the router name to find the traefik
// labels to identify the container.
func getRouterOfService(conf *dynamic.Configuration, svcName string, svcType string) string {
	svcName = stripDocker(svcName)
	name := ""

	if svcType == "http" {
		for routerName, router := range conf.HTTP.Routers {
			if router.Service == svcName {
				name = routerName
				break
			}
		}
	} else if svcType == "tcp" {
		for routerName, router := range conf.TCP.Routers {
			if router.Service == svcName {
				name = routerName
				break
			}
		}
	} else if svcType == "udp" {
		for routerName, router := range conf.UDP.Routers {
			if router.Service == svcName {
				name = routerName
				break
			}
		}
	}

	logrus.Debugf("found router '%s' for service %s", name, svcName)
	return name
}

// Get host-port binding from container, if not explicitly set via labels
//
// The `port` param is the value which was either set via label or inferred by
// traefik during its config parsing (possibly an container-internal port). The
// purpose of this method is to see if we can find a better match, specifically
// by looking at the host-port bindings in the docker config.
func getContainerPort(dc *dockerCache, conf *dynamic.Configuration, svcType string, svcName string, port string) string {
	log := logrus.WithFields(logrus.Fields{"service": svcName, "service-type": svcType})
	container, err := dc.findContainerByServiceName(svcType, svcName, getRouterOfService(conf, svcName, svcType))
	if err != nil {
		log.Warnf("failed to find host-port: %s", err)
		return port
	}
	if p := isPortSet(container, svcType, svcName); p != "" {
		log.Debugf("using explicitly set port %s for %s", p, svcName)
		return p
	}
	exposedPort, err := getPortBinding(container)
	if err != nil {
		if strings.Contains(err.Error(), "no host-port binding") {
			log.Debug(err)
		} else {
			log.Warn(err)
		}
		log.Debugf("using existing port %s", port)
		return port
	}
	if exposedPort == "" {
		log.Warnf("failed to find host-port for service %s", svcName)
		return port
	}
	log.Debugf("overriding service port from container host-port: using %s (was %s) for %s", exposedPort, port, svcName)
	return exposedPort
}

// Gets the container IP when it is configured to use a network-routable address
// (i.e., via CNI plugins such as calico or weave)
//
// If not configured, returns the globally bound hostIP
func getContainerNetworkIP(dc *dockerCache, conf *dynamic.Configuration, svcType string, svcName string, hostIP string) string {
	container, err := dc.findContainerByServiceName(svcType, svcName, getRouterOfService(conf, svcName, svcType))
	if err != nil {
		logrus.Debugf("failed to find container for service '%s': %s", svcName, err)
		return hostIP
	}

	// Only honor container IP if this is NOT a kop-generated service
	// kop-generated services should use BindIP unless explicitly overridden by kop.bind.ip
	if isServiceFromKop(dc, conf, svcType, svcName) {
		return hostIP
	}

	networkName := container.Config.Labels["traefik.docker.network"]
	if networkName == "" {
		logrus.Debugf("no network label set for %s", svcName)
		return hostIP
	}

	if container.NetworkSettings != nil {
		networkEndpoint := container.NetworkSettings.Networks[networkName]
		if networkEndpoint != nil {
			networkIP := networkEndpoint.IPAddress
			logrus.Debugf("found network name '%s' with container IP '%s' for service %s", networkName, networkIP, svcName)
			return networkIP
		}
	}
	// fallback
	logrus.Debugf("container IP not found for %s", svcName)
	return hostIP
}

// Check for explicit IP binding set via label
//
// Label can be one of two keys:
// - kop.<service name>.bind.ip = 2.2.2.2
// - kop.bind.ip = 2.2.2.2
//
// For a container with only a single exposed service, or where all services use
// the same IP, the latter is sufficient.
func getKopOverrideBinding(dc *dockerCache, conf *dynamic.Configuration, svcType string, svcName string, hostIP string) (string, bool) {
	container, err := dc.findContainerByServiceName(svcType, svcName, getRouterOfService(conf, svcName, svcType))
	if err != nil {
		logrus.Debugf("failed to find container for service '%s': %s", svcName, err)
		return hostIP, false
	}

	svcName = stripDocker(svcName)
	svcNeedle := fmt.Sprintf("kop.%s.bind.ip", svcName)
	if ip := container.Config.Labels[svcNeedle]; ip != "" {
		logrus.Debugf("found label %s with IP '%s' for service %s", svcNeedle, ip, svcName)
		return ip, true
	}

	if ip := container.Config.Labels["kop.bind.ip"]; ip != "" {
		logrus.Debugf("found label %s with IP '%s' for service %s", "kop.bind.ip", ip, svcName)
		return ip, true
	}

	return hostIP, false
}

// Determine whether this service originated from kop.$ns.traefik.* labels
func isServiceFromKop(dc *dockerCache, conf *dynamic.Configuration, svcType string, svcName string) bool {
	container, err := dc.findContainerByServiceName(svcType, svcName, getRouterOfService(conf, svcName, svcType))
	if err != nil {
		return false
	}
	original := dc.getOriginalLabels(container.ID)
	if original == nil {
		return false
	}
	svcName = stripDocker(svcName)
	// check kop.* services or routers labels
	for _, ns := range dc.namespaces {
		pref1 := fmt.Sprintf("kop.%s.traefik.%s.services.%s.", ns, svcType, svcName)
		pref2 := fmt.Sprintf("kop.%s.traefik.%s.routers.%s.", ns, svcType, svcName)
		for k := range original {
			if strings.HasPrefix(k, pref1) || strings.HasPrefix(k, pref2) {
				return true
			}
		}
	}
	return false
}

// mergeLoadBalancers merges load balancer servers for all protocols (http, tcp, udp) with the LBs
// found in the Traefik store (redis). This allows kops running on multiple nodes to add their respective
// IPs so that traefik can properly distribute the load.
func mergeLoadBalancers(dc *dockerCache, conf *dynamic.Configuration, store TraefikStore) {
	mergeGenericLoadBalancers(dc, conf, "http", conf.HTTP, store, func(server interface{}) string {
		if s, ok := server.(dynamic.Server); ok {
			return s.URL
		}
		return ""
	}, func(url string) interface{} {
		return dynamic.Server{URL: url}
	})

	mergeGenericLoadBalancers(dc, conf, "tcp", conf.TCP, store, func(server interface{}) string {
		if s, ok := server.(dynamic.TCPServer); ok {
			return s.Address
		}
		return ""
	}, func(addr string) interface{} {
		return dynamic.TCPServer{Address: addr}
	})

	mergeGenericLoadBalancers(dc, conf, "udp", conf.UDP, store, func(server interface{}) string {
		if s, ok := server.(dynamic.UDPServer); ok {
			return s.Address
		}
		return ""
	}, func(addr string) interface{} {
		return dynamic.UDPServer{Address: addr}
	})
}

// mergeGenericLoadBalancers merges servers for a given protocol using generic logic
func mergeGenericLoadBalancers(
	dc *dockerCache,
	conf *dynamic.Configuration,
	svcType string,
	svcConf interface{},
	store TraefikStore,
	getKey func(interface{}) string,
	makeServer func(string) interface{},
) {
	if svcConf == nil {
		return
	}

	// Use reflection to access Services map
	v := reflect.ValueOf(svcConf)
	servicesField := v.Elem().FieldByName("Services")
	if !servicesField.IsValid() || servicesField.IsNil() {
		return
	}

	for _, key := range servicesField.MapKeys() {
		svcName := key.String()

		container, err := dc.findContainerByServiceName(svcType, svcName, getRouterOfService(conf, svcName, svcType))
		if err != nil {
			logrus.Debugf("failed to find container for service '%s': %s", svcName, err)
			continue
		}

		merge, _ := strconv.ParseBool(container.Config.Labels["traefik.merge-lbs"])
		if !merge {
			continue
		}

		// Get existing keys from store
		var storeKey string
		switch svcType {
		case "http":
			storeKey = fmt.Sprintf("traefik/http/services/%s/loadBalancer/servers/*/url", stripDocker(svcName))
		case "tcp", "udp":
			storeKey = fmt.Sprintf("traefik/%s/services/%s/loadBalancer/servers/*/address", svcType, stripDocker(svcName))
		}
		existingKeys, err := store.Gets(storeKey)
		if err != nil {
			logrus.Warnf("failed to get existing servers for service %s: %s", svcName, err)
			continue
		}
		if len(existingKeys) == 0 {
			logrus.Debugf("no existing servers found for service %s, skip merge", svcName)
			continue
		}

		// collect list of urls or addresses
		keySet := make(map[string]struct{})
		for _, k := range existingKeys {
			keySet[k] = struct{}{}
		}

		// Add our config hosts
		svc := servicesField.MapIndex(key)
		if !svc.IsValid() || svc.IsNil() {
			continue
		}
		lbField := svc.Elem().FieldByName("LoadBalancer")
		if !lbField.IsValid() || lbField.IsNil() {
			continue
		}
		serversField := lbField.Elem().FieldByName("Servers")
		if !serversField.IsValid() {
			continue
		}
		for i := 0; i < serversField.Len(); i++ {
			server := serversField.Index(i).Interface()
			k := getKey(server)
			if k != "" {
				keySet[k] = struct{}{}
			}
		}

		// Create merged list
		newServers := reflect.MakeSlice(serversField.Type(), 0, len(keySet))
		for k := range keySet {
			newServers = reflect.Append(newServers, reflect.ValueOf(makeServer(k)))
		}
		serversField.Set(newServers)
		if newServers.Len() > len(existingKeys) {
			logrus.Debugf("merged %d %s servers into service %s", newServers.Len()-len(existingKeys), svcType, svcName)
		}
	}
}
