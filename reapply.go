package traefikkop

import (
	"context"
	"sync"
	"time"

	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

// configReapplier remembers the last configuration Traefik handed to traefik-kop
// and runs the config handler on it again at every poll interval.
//
// Traefik's configuration watcher drops a configuration that is identical to the
// previous one. That configuration holds the container IP and the *container*
// port, not the host port. After `docker restart` the container usually keeps its
// IP while Docker assigns a new random host port, so the configuration is
// unchanged, the handler is never called and Redis keeps the old host port
// (502 until traefik-kop is restarted). Re-running the handler re-resolves the
// host ports from `docker inspect` and stores the result.
type configReapplier struct {
	mu     sync.Mutex
	last   *dynamic.Configuration
	handle func(dynamic.Configuration)
}

func newConfigReapplier(handle func(dynamic.Configuration)) *configReapplier {
	return &configReapplier{handle: handle}
}

// Handle is registered as the watcher listener: it keeps a pristine copy (the
// handler mutates the configuration) and processes the configuration.
func (r *configReapplier) Handle(conf dynamic.Configuration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = conf.DeepCopy()
	r.handle(conf)
}

// Reapply runs the handler again on a copy of the last configuration, if any.
func (r *configReapplier) Reapply() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		return
	}
	r.handle(*r.last.DeepCopy())
}

// Run calls Reapply every interval until ctx is done. interval <= 0 disables it.
func (r *configReapplier) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.Reapply()
		case <-ctx.Done():
			return
		}
	}
}
