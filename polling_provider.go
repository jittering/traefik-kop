package traefikkop

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/observability/logs"
	"github.com/traefik/traefik/v3/pkg/provider"
	"github.com/traefik/traefik/v3/pkg/safe"
)

// PollingProvider simply wraps the target upstream provider with a poller.
type PollingProvider struct {
	refreshInterval  time.Duration
	upstreamProvider provider.Provider
	store            TraefikStore

	mu                  sync.Mutex
	activeAttempt       *safe.Pool
	activeAttemptCancel context.CancelFunc
	attemptNumber       uint64
}

func NewPollingProvider(refreshInterval time.Duration, upstream provider.Provider, store TraefikStore) *PollingProvider {
	return &PollingProvider{
		refreshInterval:  refreshInterval,
		upstreamProvider: upstream,
		store:            store,
	}
}

func (p *PollingProvider) Init() error {
	return p.upstreamProvider.Init()
}

func (p *PollingProvider) Provide(configurationChan chan<- dynamic.Message, pool *safe.Pool) error {
	if p.refreshInterval == 0 {
		log.Info().Msg("Disabling polling provider (interval=0)")
		return nil
	}

	log.Info().Msgf("starting polling provider with %s interval", p.refreshInterval.String())
	ticker := time.NewTicker(p.refreshInterval)

	pool.GoCtx(func(ctx context.Context) {
		logger := log.With().Str(logs.ProviderName, "docker").Logger()
		defer p.cancelActiveAttempt(logger)

		for {
			select {
			case <-ticker.C:
				logger.Debug().Msg("tick")
				p.startPollAttempt(ctx, configurationChan, logger)

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

// startPollAttempt starts a new polling attempt, cancelling the previous one if it is still active.
func (p *PollingProvider) startPollAttempt(parentCtx context.Context, configurationChan chan<- dynamic.Message, logger zerolog.Logger) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.activeAttemptCancel != nil {
		logger.Debug().Uint64("attempt", p.attemptNumber).Msg("cancelling previous polling attempt")
		p.activeAttemptCancel()
	}
	if p.activeAttempt != nil {
		p.activeAttempt.Stop()
	}

	attemptCtx, cancel := context.WithCancel(parentCtx)
	attemptPool := safe.NewPool(attemptCtx)
	p.attemptNumber++
	p.activeAttempt = attemptPool
	p.activeAttemptCancel = cancel

	logger.Debug().Uint64("attempt", p.attemptNumber).Msg("starting polling attempt")
	if err := p.upstreamProvider.Provide(configurationChan, attemptPool); err != nil {
		logger.Warn().Err(err).Uint64("attempt", p.attemptNumber).Msg("polling attempt failed to start")
	}
}

func (p *PollingProvider) cancelActiveAttempt(logger zerolog.Logger) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.activeAttemptCancel != nil {
		logger.Debug().Uint64("attempt", p.attemptNumber).Msg("stopping active polling attempt")
		p.activeAttemptCancel()
		p.activeAttemptCancel = nil
	}
	if p.activeAttempt != nil {
		p.activeAttempt.Stop()
		p.activeAttempt = nil
	}
}
