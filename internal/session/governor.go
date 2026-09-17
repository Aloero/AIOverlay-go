package session

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrSessionRateLimit = errors.New("session call rate limit reached")

type Governor struct {
	mu          sync.Mutex
	active      int
	maxParallel int
	callsPerMin int
	calls       []time.Time
	waitingHigh int
	changed     chan struct{}
}

func NewGovernor() *Governor {
	return &Governor{maxParallel: 3, callsPerMin: 60, changed: make(chan struct{})}
}
func (g *Governor) notifyLocked() { close(g.changed); g.changed = make(chan struct{}) }
func (g *Governor) Configure(maxParallel, callsPerMin int) {
	if maxParallel < 1 {
		maxParallel = 1
	}
	if maxParallel > 64 {
		maxParallel = 64
	}
	if callsPerMin < 1 {
		callsPerMin = 1
	}
	if callsPerMin > 10000 {
		callsPerMin = 10000
	}
	g.mu.Lock()
	g.maxParallel = maxParallel
	g.callsPerMin = callsPerMin
	g.pruneLocked(time.Now())
	g.notifyLocked()
	g.mu.Unlock()
}
func (g *Governor) pruneLocked(now time.Time) {
	cut := now.Add(-time.Minute)
	i := 0
	for i < len(g.calls) && g.calls[i].Before(cut) {
		i++
	}
	if i > 0 {
		g.calls = append([]time.Time(nil), g.calls[i:]...)
	}
}
func (g *Governor) Acquire(ctx context.Context, high bool) (func(), error) {
	registered := false
	defer func() {
		if registered {
			g.mu.Lock()
			g.waitingHigh--
			g.notifyLocked()
			g.mu.Unlock()
		}
	}()
	for {
		now := time.Now()
		g.mu.Lock()
		g.pruneLocked(now)
		if high && !registered {
			g.waitingHigh++
			registered = true
			g.notifyLocked()
		}
		canParallel := g.active < g.maxParallel
		canPriority := high || g.waitingHigh == 0
		canRate := len(g.calls) < g.callsPerMin
		if canParallel && canPriority && canRate {
			g.active++
			g.calls = append(g.calls, now)
			if registered {
				g.waitingHigh--
				registered = false
			}
			g.notifyLocked()
			g.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					g.mu.Lock()
					if g.active > 0 {
						g.active--
					}
					g.notifyLocked()
					g.mu.Unlock()
				})
			}, nil
		}
		changed := g.changed
		var wait time.Duration
		if !canRate && len(g.calls) > 0 {
			wait = time.Until(g.calls[0].Add(time.Minute))
			if wait < time.Millisecond {
				wait = time.Millisecond
			}
		}
		g.mu.Unlock()
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, ctx.Err()
			case <-changed:
				t.Stop()
			case <-t.C:
			}
		} else {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-changed:
			}
		}
	}
}
func (g *Governor) Snapshot() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneLocked(time.Now())
	return map[string]int{"active": g.active, "max_parallel": g.maxParallel, "calls_last_minute": len(g.calls), "calls_per_minute": g.callsPerMin, "manual_waiters": g.waitingHigh}
}
