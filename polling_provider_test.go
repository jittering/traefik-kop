package traefikkop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traefik/traefik/v3/pkg/config/dynamic"
	"github.com/traefik/traefik/v3/pkg/safe"
)

// noopProvider counts how many times Provide is called.
type noopProvider struct {
	calls *int32
}

func (n *noopProvider) Init() error {
	return nil
}

func (n *noopProvider) Provide(configurationChan chan<- dynamic.Message, pool *safe.Pool) error {
	atomic.AddInt32(n.calls, 1)
	return nil
}

func TestPollingProvider_CountsPolls(t *testing.T) {
	var callCount int32
	upstream := &noopProvider{calls: &callCount}
	store := &testStore{}
	interval := 50 * time.Millisecond
	pp := NewPollingProvider(interval, upstream, store)
	pp.Init()

	ch := make(chan dynamic.Message)
	pool := safe.NewPool(context.Background())
	defer pool.Stop()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run polling in a goroutine
	go func() {
		pp.Provide(ch, pool)
	}()

	// Let it poll a few times
	time.Sleep(220 * time.Millisecond)
	cancel()

	calls := atomic.LoadInt32(&callCount)
	if calls < 3 || calls > 6 {
		t.Errorf("expected 3-6 polls, got %d", calls)
	}
}
