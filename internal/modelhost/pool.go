package modelhost

import (
	"context"
	"fmt"
	"sync"
)

// Pool retains at most one idle instance per key and never lets one key's change stop another
// key's running request. Keys are composed by the host, which alone knows what makes two of its
// instances incompatible (credential, egress proxy, catalog generation, boot-time options).
type Pool struct {
	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
}

type entry struct {
	ready    chan struct{}
	instance Instance
	err      error
	users    int
}

// Acquire returns a live instance for key plus its release func. A cold start runs in the
// background under StartupTimeout, so the caller's context bounds only how long this particular
// caller is willing to wait - an abandoned wait does not abort the boot.
func (p *Pool) Acquire(ctx context.Context, key string, create func(context.Context) (Instance, error)) (Instance, func(), error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("model host pool is closed")
	}
	if p.entries == nil {
		p.entries = map[string]*entry{}
	}
	e, found := p.entries[key]
	if found {
		select {
		case <-e.ready:
			if e.users == 0 && (e.err != nil || !e.instance.Alive()) {
				delete(p.entries, key)
				if e.instance != nil {
					e.instance.Stop()
				}
				found = false
			}
		default:
		}
	}
	if !found {
		e = &entry{ready: make(chan struct{})}
		p.entries[key] = e
	}
	e.users++
	p.mu.Unlock()
	if !found {
		go func() {
			createCtx, cancelCreate := context.WithTimeout(context.WithoutCancel(ctx), StartupTimeout)
			instance, err := create(createCtx)
			cancelCreate()
			p.mu.Lock()
			if p.closed && err == nil {
				err = fmt.Errorf("model host pool is closed")
			}
			if err != nil && instance != nil {
				// Nobody owns this instance any more: shutdown must not inherit a stray process.
				instance.Stop()
				instance = nil
			}
			e.instance, e.err = instance, err
			close(e.ready)
			p.mu.Unlock()
		}()
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

func (p *Pool) release(key string, e *entry) {
	var stop []Instance
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
				// Only one idle instance is worth keeping warm.
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
		v.Stop()
	}
}

func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	var stop []Instance
	for key, e := range p.entries {
		// Active requests release and stop their own instance. Shutdown does not race with a
		// startup or mutate a live process while another lease inspects it.
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
		v.Stop()
	}
}
