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
		// logrus.Printf("got new conf..\n")
		// fmt.Printf("%s\n", dumpJson(conf))
		logrus.Infoln("refreshing traefik-kop configuration")

		dc := &dockerCache{
			client:  dockerClient,
			list:    nil,
			details: make(map[string]types.ContainerJSON),
		}

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

func keepContainer(ns string, container types.ContainerJSON) bool {
	containerNS := container.Config.Labels["kop.namespace"]
	return ns == containerNS || (ns == "" && containerNS == "")
}

// filter out services by namespace
// ns is traefik-kop's configured namespace to match against.
func filterServices(dc *dockerCache, conf *dynamic.Configuration, ns string) {
	if conf.HTTP != nil && conf.HTTP.Services != nil {
		for svcName := range conf.HTTP.Services {
			container, err := dc.findContainerByServiceName("http", svcName, getRouterOfService(conf, svcName, "http"))
			if err != nil {
				logrus.Warnf("failed to find container for service '%s': %s", svcName, err)
				continue
			}
			if !keepContainer(ns, container) {
				logrus.Infof("skipping service %s (not in namespace %s)", svcName, ns)
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
			if !keepContainer(ns, container) {
				logrus.Infof("skipping router %s (not in namespace %s)", routerName, ns)
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
			if !keepContainer(ns, container) {
				logrus.Infof("skipping service %s (not in namespace %s)", svcName, ns)
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
			if !keepContainer(ns, container) {
				logrus.Infof("skipping router %s (not in namespace %s)", routerName, ns)
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
			if !keepContainer(ns, container) {
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
			if !keepContainer(ns, container) {
				logrus.Infof("skipping router %s (not in namespace %s)", routerName, ns)
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
				ip, changed := getKopOverrideBinding(dc, conf, "http", svcName, ip)
				if !changed {
					// override with container IP if we have a routable IP
					ip = getContainerNetworkIP(dc, conf, "http", svcName, ip)
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
						u.Host = ip + ":" + p
					} else {
						u.Host = ip
					}
					server.URL = u.String()
				} else {
					scheme := "http"
					if server.Scheme != "" {
						scheme = server.Scheme
					}
					server.URL = fmt.Sprintf("%s://%s", scheme, ip)
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
			return
		}

		merge, _ := strconv.ParseBool(container.Config.Labels["traefik.merge-lbs"])
		if !merge {
			return
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
