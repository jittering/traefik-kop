package traefikkop

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

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
		if !dp.UseBindPortIP {
			// if not using traefik's built in IP/Port detection, use our own
			replaceIPs(dockerClient, &conf, config.BindIP)
		}
		err := store.Store(conf)
		if err != nil {
			panic(err)
		}
	}
}

func Start(config Config) {
	dp := newDockerProvider(config)
	store := NewRedisStore(config.Hostname, config.Addr, config.Pass, config.DB)
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

// replaceIPs for all service endpoints
//
// By default, traefik finds the local/internal docker IP for each container.
// Since we are exposing these services to an external node/server, we need
// to replace any IPs with the correct IP for this server, as configured at startup.
//
// When using CNI, as indicated by the container label `traefik.docker.network`,
// we will stick with the container IP.
func replaceIPs(dockerClient client.APIClient, conf *dynamic.Configuration, ip string) {
	// modify HTTP URLs
	if conf.HTTP != nil && conf.HTTP.Services != nil {
		for svcName, svc := range conf.HTTP.Services {
			log := logrus.WithFields(logrus.Fields{"service": svcName, "service-type": "http"})
			log.Debugf("found http service: %s", svcName)
			for i := range svc.LoadBalancer.Servers {
				ip, changed := getKopOverrideBinding(dockerClient, conf, "http", svcName, ip)
				if !changed {
					// override with container IP if we have a routable IP
					ip = getContainerNetworkIP(dockerClient, conf, "http", svcName, ip)
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
					p := getContainerPort(dockerClient, conf, "http", svcName, u.Port())
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
					port := getContainerPort(dockerClient, conf, "http", svcName, server.Port)
					if port != "" {
						server.URL += ":" + server.Port
					}
				}
				log.Infof("publishing %s", server.URL)
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
				ip = getContainerNetworkIP(dockerClient, conf, "tcp", svcName, ip)

				server := &svc.LoadBalancer.Servers[i]
				server.Port = getContainerPort(dockerClient, conf, "tcp", svcName, server.Port)
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
				ip = getContainerNetworkIP(dockerClient, conf, "udp", svcName, ip)

				server := &svc.LoadBalancer.Servers[i]
				server.Port = getContainerPort(dockerClient, conf, "udp", svcName, server.Port)
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
	svcName = strings.TrimSuffix(svcName, "@docker")
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
func getContainerPort(dockerClient client.APIClient, conf *dynamic.Configuration, svcType string, svcName string, port string) string {
	log := logrus.WithFields(logrus.Fields{"service": svcName, "service-type": svcType})
	container, err := findContainerByServiceName(dockerClient, svcType, svcName, getRouterOfService(conf, svcName, svcType))
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
func getContainerNetworkIP(dockerClient client.APIClient, conf *dynamic.Configuration, svcType string, svcName string, hostIP string) string {
	container, err := findContainerByServiceName(dockerClient, svcType, svcName, getRouterOfService(conf, svcName, svcType))
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
func getKopOverrideBinding(dockerClient client.APIClient, conf *dynamic.Configuration, svcType string, svcName string, hostIP string) (string, bool) {
	container, err := findContainerByServiceName(dockerClient, svcType, svcName, getRouterOfService(conf, svcName, svcType))
	if err != nil {
		logrus.Debugf("failed to find container for service '%s': %s", svcName, err)
		return hostIP, false
	}

	svcName = strings.TrimSuffix(svcName, "@docker")
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
