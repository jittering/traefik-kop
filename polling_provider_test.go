package traefikkop

import (
	"context"
	"sync"
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

type blockingProvider struct {
	started  chan struct{}
	canceled chan struct{}
	active   atomic.Int32
	maxAlive atomic.Int32
	once     sync.Once
}

func (b *blockingProvider) Init() error {
	return nil
}

func (b *blockingProvider) Provide(configurationChan chan<- dynamic.Message, pool *safe.Pool) error {
	pool.GoCtx(func(ctx context.Context) {
		current := b.active.Add(1)
		for {
			maxAlive := b.maxAlive.Load()
			if current <= maxAlive || b.maxAlive.CompareAndSwap(maxAlive, current) {
				break
			}
		}

		select {
		case b.started <- struct{}{}:
		default:
		}

		<-ctx.Done()
		b.active.Add(-1)
		b.once.Do(func() {
			close(b.canceled)
		})
	})
	return nil
}

func TestPollingProvider_CancelsPreviousAttemptOnNextTick(t *testing.T) {
	upstream := &blockingProvider{
		started:  make(chan struct{}, 4),
		canceled: make(chan struct{}),
	}
	store := &testStore{}
	pp := NewPollingProvider(20*time.Millisecond, upstream, store)

	poolCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := safe.NewPool(poolCtx)
	defer pool.Stop()

	ch := make(chan dynamic.Message)
	if err := pp.Provide(ch, pool); err != nil {
		t.Fatalf("Provide() error = %v", err)
	}

	select {
	case <-upstream.started:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("first polling attempt did not start")
	}

	select {
	case <-upstream.started:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("second polling attempt did not start")
	}

	select {
	case <-upstream.canceled:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("previous polling attempt was not canceled on next tick")
	}

	if maxAlive := upstream.maxAlive.Load(); maxAlive > 1 {
		t.Fatalf("expected at most one active polling attempt, got %d", maxAlive)
	}

	cancel()
	pool.Stop()
	if active := upstream.active.Load(); active != 0 {
		t.Fatalf("expected no active attempts after shutdown, got %d", active)
	}
}
