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
	// err := dp.Init()
	if err != nil {
		panic(err)
	}

	// see if we can manually connect to docker

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
		// kv := configToKV(conf)
		kv := ConfigToKV(conf)
		for k, v := range kv {
			fmt.Printf("  %s = %s\n", k, v)
		}
		fmt.Println("")
	})

	watcher.Start()

	// configurationChan := make(chan dynamic.Message)
	// pool := safe.NewPool(context.Background())
	// go func() {
	// 	err := providerAggregator.Provide(configurationChan, pool)
	// 	if err != nil {
	// 		log.Errorf("Cannot start the provider %T: %v", dp, err)
	// 	} else {
	// 		log.Infof("finished providing from docker..\n")
	// 	}
	// }()

	// for msg := range configurationChan {
	// 	// fmt.Printf("got msg: %#v\n", msg)
	// 	fmt.Printf("%s\n", dumpJson(msg))
	// }

	select {} // go forever

}
