package traefikkop

import (
	"context"
	"fmt"
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

func Start() {
	defaultThrottleDuration := 5 * time.Second

	dp := &docker.Provider{
		Endpoint: "unix:///var/run/docker.sock",
		// HTTPClientTimeout: pduration,
		Watch:                   true,
		SwarmModeRefreshSeconds: ptypes.Duration(15 * time.Second),
	}

	providers := &static.Providers{
		Docker: dp,
	}
	providerAggregator := aggregator.NewProviderAggregator(*providers)

	err := providerAggregator.Init()
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
		logrus.Printf("got new conf..\n")
		fmt.Printf("%s\n", dumpJson(conf))
		fmt.Println("kv:")
		kv := ConfigToKV(conf)
		for k, v := range kv {
			fmt.Printf("  %s = %s\n", k, v)
		}
		fmt.Println("")
	})

	watcher.Start()

	select {} // go forever
}
