package traefikkop

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/logs"
	"github.com/traefik/traefik/v3/pkg/provider"
	"github.com/traefik/traefik/v3/pkg/safe"
)

// PollingProvider simply wraps the target upstream provider with a poller.
type PollingProvider struct {
	refreshInterval  time.Duration
	upstreamProvider provider.Provider
	store            TraefikStore
}

func NewPollingProvider(refreshInterval time.Duration, upstream provider.Provider, store TraefikStore) *PollingProvider {
	return &PollingProvider{refreshInterval, upstream, store}
}

func (p PollingProvider) Init() error {
	return p.upstreamProvider.Init()
}

func (p PollingProvider) Provide(configurationChan chan<- dynamic.Message, pool *safe.Pool) error {
	if p.refreshInterval == 0 {
		log.Info().Msg("Disabling polling provider (interval=0)")
		return nil
	}

	log.Info().Msgf("starting polling provider with %s interval", p.refreshInterval.String())
	ticker := time.NewTicker(p.refreshInterval)

	pool.GoCtx(func(ctx context.Context) {
		logger := log.With().Str(logs.ProviderName, "docker").Logger()

		for {
			select {
			case <-ticker.C:
				logger.Debug().Msg("tick")
				p.upstreamProvider.Provide(configurationChan, pool)

				// Try to push the last config if Redis restarted
				err := p.store.KeepConfAlive()
				if err != nil {
					logger.Warn().Msgf("Failed to push cached config: %s", err)
				}

			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	})

	return nil
}
