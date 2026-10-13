package traefikkop

import (
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/provider"
	"github.com/traefik/traefik/v3/pkg/safe"
	"github.com/traefik/traefik/v3/pkg/server"
)

// newConfigurationWatcher leaves default entrypoint selection to the remote
// Traefik instance. Since 3.7, the watcher requires an entrypoint to retain TLS
// routers, so supply a temporary default and remove it before publishing.
func newConfigurationWatcher(pool *safe.Pool, p provider.Provider) *server.ConfigurationWatcher {
	const defaultEntryPoint = "traefik-kop-default-internal"
	watcher := server.NewConfigurationWatcher(pool, p, []string{defaultEntryPoint}, "docker", false)
	watcher.AddListener(func(conf dynamic.Configuration) {
		if conf.HTTP != nil {
			for _, router := range conf.HTTP.Routers {
				if len(router.EntryPoints) == 1 && router.EntryPoints[0] == defaultEntryPoint {
					router.EntryPoints = nil
				}
			}
		}
		if conf.TCP != nil {
			for _, router := range conf.TCP.Routers {
				if len(router.EntryPoints) == 1 && router.EntryPoints[0] == defaultEntryPoint {
					router.EntryPoints = nil
				}
			}
		}
	})
	return watcher
}
