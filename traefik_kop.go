package traefikkop

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
	ptypes "github.com/traefik/paerser/types"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/config/static"
	"github.com/traefik/traefik/v2/pkg/provider/aggregator"
	"github.com/traefik/traefik/v2/pkg/provider/docker"
	"github.com/traefik/traefik/v2/pkg/safe"
	"github.com/traefik/traefik/v2/pkg/server"
)

var Version = ""

const defaultEndpointPath = "/var/run/docker.sock"
const defaultThrottleDuration = 5 * time.Second

func Start(config Config) {

	_, err := os.Stat(defaultEndpointPath)
	if err != nil {
		logrus.Fatal(err)
	}

	dp := &docker.Provider{
		Endpoint:                "unix://" + defaultEndpointPath,
		HTTPClientTimeout:       ptypes.Duration(defaultTimeout),
		SwarmMode:               false,
		Watch:                   true,
		SwarmModeRefreshSeconds: ptypes.Duration(15 * time.Second),
	}

	store := NewStore(config.Hostname, config.Addr, config.Pass, config.DB)
	err = store.Ping()
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

	err = providerAggregator.Init()
	if err != nil {
		panic(err)
	}

	dockerClient, err := createDockerClient("unix://" + defaultEndpointPath)
	if err != nil {
		logrus.Fatalf("failed to create docker client: %s", err)
	}

	ctx := context.Background()
	routinesPool := safe.NewPool(ctx)

	watcher := server.NewConfigurationWatcher(
		routinesPool,
		providerAggregator,
		time.Duration(defaultThrottleDuration),
		[]string{},
		"docker",
	)

	watcher.AddListener(func(conf dynamic.Configuration) {
		// logrus.Printf("got new conf..\n")
		// fmt.Printf("%s\n", dumpJson(conf))
		logrus.Infoln("refreshing configuration")
		replaceIPs(dockerClient, &conf, config.BindIP)
		err := store.Store(conf)
		if err != nil {
			panic(err)
		}
	})

	watcher.Start()

	select {} // go forever
}

// replaceIPs for all service endpoints
//
// By default, traefik finds the local/internal docker IP for each container.
// Since we are exposing these services to an external node/server, we need
// to replace an IPs with the correct IP for this server, as configured at startup.
func replaceIPs(dockerClient client.APIClient, conf *dynamic.Configuration, ip string) {
	// modify HTTP URLs
	if conf.HTTP != nil && conf.HTTP.Services != nil {
		for svcName, svc := range conf.HTTP.Services {
			logrus.Debugf("found http service: %s", svcName)
			for i := range svc.LoadBalancer.Servers {
				network_ip := getContainerIP(dockerClient, conf, "http", svcName)
				if network_ip != "" {
					// override with container IP, if available
					ip = network_ip
				}
				server := &svc.LoadBalancer.Servers[i]
				if server.URL != "" {
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
			}
		}
	}

	// TCP
	if conf.TCP != nil && conf.TCP.Services != nil {
		for svcName, svc := range conf.TCP.Services {
			logrus.Debugf("found tcp service: %s", svcName)
			for i := range svc.LoadBalancer.Servers {
				server := &svc.LoadBalancer.Servers[i]
				server.Address = ip
				server.Port = getContainerPort(dockerClient, conf, "tcp", svcName, server.Port)
			}
		}
	}

	// UDP
	if conf.UDP != nil && conf.UDP.Services != nil {
		for svcName, svc := range conf.UDP.Services {
			logrus.Debugf("found udp service: %s", svcName)
			for i := range svc.LoadBalancer.Servers {
				server := &svc.LoadBalancer.Servers[i]
				server.Address = ip
				server.Port = getContainerPort(dockerClient, conf, "udp", svcName, server.Port)
			}
		}
	}
}

func getRouterOfService(conf *dynamic.Configuration, svcName string, svcType string) string {
	if svcType == "http" {
		for routerName, router := range conf.HTTP.Routers {
			if router.Service == svcName {
				return routerName
			}
		}
	} else if svcType == "tcp" {
		for routerName, router := range conf.TCP.Routers {
			if router.Service == svcName {
				return routerName
			}
		}
	} else if svcType == "udp" {
		for routerName, router := range conf.UDP.Routers {
			if router.Service == svcName {
				return routerName
			}
		}
	}

	return ""
}

// Get host-port binding from container, if not explicitly set via labels
func getContainerPort(dockerClient client.APIClient, conf *dynamic.Configuration, svcType string, svcName string, port string) string {
	svcName = strings.TrimSuffix(svcName, "@docker")
	routerName := getRouterOfService(conf, svcName, svcType)
	routerName = strings.TrimSuffix(routerName, "@docker")

	logrus.Debugf("found router %s for service %s", routerName, svcName)
	container, err := findContainerByServiceName(dockerClient, svcType, svcName, routerName)
	if err != nil {
		logrus.Warnf("failed to find host-port: %s", err)
		return port
	}
	logrus.Debugf("found container '%s' (%s) for service '%s'", container.Name, container.ID, svcName)
	if isPortSet(container, svcType, svcName) {
		logrus.Debugf("using explicitly set port %s for %s", port, svcName)
		return port
	}
	exposedPort, err := getPortBinding(container)
	if err != nil {
		logrus.Warn(err)
		return port
	}
	if exposedPort == "" {
		logrus.Warnf("failed to find host-port for service %s", svcName)
		return port
	}
	logrus.Debugf("overriding service port from container host-port: using %s (was %s) for %s", exposedPort, port, svcName)
	return exposedPort
}

func getContainerIP(dockerClient client.APIClient, conf *dynamic.Configuration, svcType string, svcName string) string {
	svcName = strings.TrimSuffix(svcName, "@docker")
	routerName := getRouterOfService(conf, svcName, svcType)
	routerName = strings.TrimSuffix(routerName, "@docker")

	logrus.Debugf("found router %s for service %s", routerName, svcName)
	container, _ := findContainerByServiceName(dockerClient, svcType, svcName, routerName)

	network_name := container.Config.Labels["traefik.docker.network"]
	logrus.Debugf("found network name '%s' for %s", network_name, svcName)
	if network_name != "" {
		return container.NetworkSettings.Networks[network_name].IPAddress
	}
	return ""
}
