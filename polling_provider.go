package traefikkop

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/log"
	"github.com/traefik/traefik/v2/pkg/provider"
	"github.com/traefik/traefik/v2/pkg/safe"
)

// PollingProvider simply wraps the target upstream provider with a poller.
type PollingProvider struct {
	refreshInterval  time.Duration
	upstreamProvider provider.Provider
}

func newPollingProvider(refreshInterval time.Duration, upstream provider.Provider) *PollingProvider {
	return &PollingProvider{refreshInterval, upstream}
}

func (p PollingProvider) Init() error {
	return nil
}

func (p PollingProvider) Provide(configurationChan chan<- dynamic.Message, pool *safe.Pool) error {
	logrus.Infof("starting polling provider with %s interval", p.refreshInterval.String())
	ticker := time.NewTicker(p.refreshInterval)

	pool.GoCtx(func(ctx context.Context) {
		ctx = log.With(ctx, log.Str(log.ProviderName, "docker"))

		for {
			select {
			case <-ticker.C:
				logrus.Debugln("tick")
				p.upstreamProvider.Provide(configurationChan, pool)

			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	})

	return nil
}
