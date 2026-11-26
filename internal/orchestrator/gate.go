package orchestrator

import (
	"context"
	"sync"
)

// Gate is a context-aware, resizable concurrency gate. Agents release their
// slot while waiting for child agents, which prevents a nested spawn from
// deadlocking at max_concurrency=1.
type Gate struct {
	mu     sync.Mutex
	cond   *sync.Cond
	limit  int
	active int
	peak   int
}

func NewGate(limit int) *Gate {
	if limit < 1 {
		limit = 1
	}
	g := &Gate{limit: limit}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func (g *Gate) Acquire(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			g.mu.Lock()
			g.cond.Broadcast()
			g.mu.Unlock()
		case <-stop:
		}
	}()

	for g.active >= g.limit {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.cond.Wait()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	g.active++
	if g.active > g.peak {
		g.peak = g.active
	}
	return nil
}

func (g *Gate) Release() {
	g.mu.Lock()
	if g.active > 0 {
		g.active--
	}
	g.cond.Broadcast()
	g.mu.Unlock()
}

func (g *Gate) SetLimit(limit int) {
	if limit < 1 {
		limit = 1
	}
	g.mu.Lock()
	g.limit = limit
	g.cond.Broadcast()
	g.mu.Unlock()
}

func (g *Gate) Limit() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.limit
}

func (g *Gate) Peak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}
