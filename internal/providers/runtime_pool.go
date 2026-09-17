package providers

import (
	"context"
	"fmt"
	"sync"
)

type runtimeInstance interface {
	alive() bool
	stop()
}
type runtimeEntry struct {
	ready    chan struct{}
	instance runtimeInstance
	err      error
	users    int
}

// A credential change must never kill another request. Running credentials own
// leases; at most one idle process per provider is retained for warm reuse.
type credentialRuntimes struct {
	mu      sync.Mutex
	entries map[string]*runtimeEntry
	closed  bool
}

func (p *credentialRuntimes) acquire(ctx context.Context, key string, create func(context.Context, string) (runtimeInstance, error)) (runtimeInstance, func(), error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("OpenCode runtime pool is closed")
	}
	if p.entries == nil {
		p.entries = map[string]*runtimeEntry{}
	}
	e, found := p.entries[key]
	if found {
		select {
		case <-e.ready:
			if e.users == 0 && (e.err != nil || !e.instance.alive()) {
				delete(p.entries, key)
				if e.instance != nil {
					e.instance.stop()
				}
				found = false
			}
		default:
		}
	}
	if !found {
		e = &runtimeEntry{ready: make(chan struct{})}
		p.entries[key] = e
	}
	e.users++
	p.mu.Unlock()
	if !found {
		instance, err := create(ctx, key)
		p.mu.Lock()
		if p.closed && err == nil {
			err = fmt.Errorf("OpenCode runtime pool is closed")
		}
		e.instance, e.err = instance, err
		close(e.ready)
		p.mu.Unlock()
	}
	select {
	case <-e.ready:
		if e.err != nil {
			p.release(key, e)
			return nil, nil, e.err
		}
	case <-ctx.Done():
		p.release(key, e)
		return nil, nil, ctx.Err()
	}
	if ctx.Err() != nil {
		p.release(key, e)
		return nil, nil, ctx.Err()
	}
	var once sync.Once
	return e.instance, func() { once.Do(func() { p.release(key, e) }) }, nil
}

func (p *credentialRuntimes) release(key string, e *runtimeEntry) {
	var stop []runtimeInstance
	p.mu.Lock()
	e.users--
	if e.users == 0 {
		select {
		case <-e.ready:
			if p.closed || e.err != nil {
				delete(p.entries, key)
				if e.instance != nil {
					stop = append(stop, e.instance)
				}
			} else {
				for other, v := range p.entries {
					if other == key || v.users != 0 {
						continue
					}
					select {
					case <-v.ready:
						delete(p.entries, other)
						if v.instance != nil {
							stop = append(stop, v.instance)
						}
					default:
					}
				}
			}
		default:
		}
	}
	p.mu.Unlock()
	for _, v := range stop {
		v.stop()
	}
}
func (p *credentialRuntimes) close() {
	p.mu.Lock()
	p.closed = true
	var stop []runtimeInstance
	for key, e := range p.entries {
		// Active requests release and stop their own process. Shutdown does not race
		// with startup or mutate cmd while another lease is inspecting its state.
		if e.users == 0 {
			select {
			case <-e.ready:
				delete(p.entries, key)
				if e.instance != nil {
					stop = append(stop, e.instance)
				}
			default:
			}
		}
	}
	p.mu.Unlock()
	for _, v := range stop {
		v.stop()
	}
}
