package traefikkop

import (
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/provider"
	"github.com/traefik/traefik/v2/pkg/safe"
)

// MultiProvider simply wraps an array of providers (more generic than
// ProviderAggregator)
type MultiProvider struct {
	upstreamProviders []provider.Provider
}

func newMultiProvider(upstream []provider.Provider) *MultiProvider {
	return &MultiProvider{upstream}
}

func (p MultiProvider) Init() error {
	return nil
}

func (p MultiProvider) Provide(configurationChan chan<- dynamic.Message, pool *safe.Pool) error {
	for _, provider := range p.upstreamProviders {
		provider.Provide(configurationChan, pool)
	}
	return nil
}
