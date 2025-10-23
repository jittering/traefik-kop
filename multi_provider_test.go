package traefikkop

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/provider"
	"github.com/traefik/traefik/v3/pkg/safe"
)

func TestMultiProvider_ProvideCounts(t *testing.T) {
	var count1, count2 int32
	p1 := &noopProvider{calls: &count1}
	p2 := &noopProvider{calls: &count2}
	mp := NewMultiProvider([]provider.Provider{p1, p2})
	mp.Init()

	ch := make(chan dynamic.Message)
	pool := safe.NewPool(context.Background())
	defer pool.Stop()

	mp.Provide(ch, pool)

	if atomic.LoadInt32(&count1) != 1 {
		t.Errorf("expected p1 to be called once, got %d", count1)
	}
	if atomic.LoadInt32(&count2) != 1 {
		t.Errorf("expected p2 to be called once, got %d", count2)
	}
}
