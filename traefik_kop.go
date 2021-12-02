package traefikkop

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	ptypes "github.com/traefik/paerser/types"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/config/static"
	"github.com/traefik/traefik/v2/pkg/provider/aggregator"
	"github.com/traefik/traefik/v2/pkg/provider/docker"
	"github.com/traefik/traefik/v2/pkg/safe"
	"github.com/traefik/traefik/v2/pkg/server"
)

const defaultEndpointPath = "/var/run/docker.sock"
const defaultThrottleDuration = 5 * time.Second

func Start(config Config) {

	_, err := os.Stat(defaultEndpointPath)
	if err != nil {
		logrus.Fatal(err)
	}

	dp := &docker.Provider{
		Endpoint: "unix://" + defaultEndpointPath,
		// HTTPClientTimeout: pduration,
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
		replaceIPs(&conf, config.BindIP)
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
func replaceIPs(conf *dynamic.Configuration, ip string) {
	// modify HTTP URLs
	if conf.HTTP != nil && conf.HTTP.Services != nil {
		for _, svc := range conf.HTTP.Services {
			for i := range svc.LoadBalancer.Servers {
				server := &svc.LoadBalancer.Servers[i]
				if server.URL != "" {
					u, _ := url.Parse(server.URL)
					p := u.Port()
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
					if server.Port != "" {
						server.URL += ":" + server.Port
					}
				}
			}
		}
	}

	// TCP
	if conf.TCP != nil && conf.TCP.Services != nil {
		for _, svc := range conf.TCP.Services {
			for i := range svc.LoadBalancer.Servers {
				server := &svc.LoadBalancer.Servers[i]
				server.Address = ip
			}
		}
	}

	// UDP
	if conf.UDP != nil && conf.UDP.Services != nil {
		for _, svc := range conf.UDP.Services {
			for i := range svc.LoadBalancer.Servers {
				server := &svc.LoadBalancer.Servers[i]
				server.Address = ip
			}
		}
	}
}
