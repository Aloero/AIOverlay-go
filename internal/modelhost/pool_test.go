package modelhost

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeInstance struct{ stops atomic.Int32 }

func (f *fakeInstance) Alive() bool { return f.stops.Load() == 0 }
func (f *fakeInstance) Stop()       { f.stops.Add(1) }

func TestPoolLeasesNeverKillOtherKeys(t *testing.T) {
	var pool Pool
	var mu sync.Mutex
	created := map[string]*fakeInstance{}
	// The host composes its own keys and knows what makes instances incompatible, so the pool
	// hands the create closure no key: the test binds one per key.
	createFor := func(key string) func(context.Context) (Instance, error) {
		return func(context.Context) (Instance, error) {
			v := &fakeInstance{}
			mu.Lock()
			created[key] = v
			mu.Unlock()
			return v, nil
		}
	}
	a, releaseA, err := pool.Acquire(context.Background(), "a", createFor("a"))
	if err != nil {
		t.Fatal(err)
	}
	again, releaseAgain, _ := pool.Acquire(context.Background(), "a", createFor("a"))
	if a != again {
		t.Fatal("the same key did not reuse its instance")
	}
	_, releaseB, _ := pool.Acquire(context.Background(), "b", createFor("b"))
	releaseAgain()
	releaseB()
	if created["a"].stops.Load() != 0 {
		t.Fatal("switching keys killed an active request")
	}
	_, releaseC, _ := pool.Acquire(context.Background(), "c", createFor("c"))
	releaseC()
	if created["b"].stops.Load() != 1 {
		t.Fatal("excess idle instance retained")
	}
	if created["a"].stops.Load() != 0 {
		t.Fatal("idle eviction killed an active request")
	}
	releaseA()
	pool.Close()
	for _, r := range created {
		if r.stops.Load() != 1 {
			t.Fatal("instance not stopped exactly once")
		}
	}
}

func TestPoolConcurrentSameKeyStartsOnce(t *testing.T) {
	var pool Pool
	var starts atomic.Int32
	entered, proceed := make(chan struct{}), make(chan struct{})
	create := func(context.Context) (Instance, error) {
		if starts.Add(1) == 1 {
			close(entered)
		}
		<-proceed
		return &fakeInstance{}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, release, err := pool.Acquire(context.Background(), "same", create)
			if err != nil {
				t.Error(err)
				return
			}
			release()
		}()
	}
	<-entered
	close(proceed)
	wg.Wait()
	pool.Close()
	if starts.Load() != 1 {
		t.Fatalf("starts=%d", starts.Load())
	}
}

// Startup is infrastructure, not model latency: an attempt that gives up must not abort the boot,
// or a slow machine would never get a usable instance.
func TestPoolStartupOutlivesTheAttemptThatTriggeredIt(t *testing.T) {
	var pool Pool
	var starts atomic.Int32
	booting := make(chan context.Context, 1)
	finish := make(chan struct{})
	create := func(ctx context.Context) (Instance, error) {
		if starts.Add(1) == 1 {
			booting <- ctx
		}
		<-finish
		return &fakeInstance{}, nil
	}

	attempt, cancelAttempt := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelAttempt()
	if _, _, err := pool.Acquire(attempt, "key", create); err == nil {
		t.Fatal("the attempt must still end on its own budget")
	}
	startCtx := <-booting
	deadline, ok := startCtx.Deadline()
	if !ok {
		t.Fatal("the startup context must carry its own bound")
	}
	if left := time.Until(deadline); left < 5*time.Second {
		t.Fatalf("startup inherited the attempt budget: %s left", left)
	}
	if startCtx.Err() != nil {
		t.Fatal("abandoning the attempt cancelled the host startup")
	}

	close(finish)
	expiry := time.Now().Add(3 * time.Second)
	for {
		_, release, err := pool.Acquire(context.Background(), "key", create)
		if err == nil {
			if starts.Load() != 1 {
				t.Fatalf("the warmed instance was not reused: starts=%d", starts.Load())
			}
			release()
			break
		}
		if time.Now().After(expiry) {
			t.Fatalf("the warmed instance never became available: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	pool.Close()
}

func TestEndpointAuthorizationHidesPassword(t *testing.T) {
	if got := (Endpoint{BaseURL: "http://127.0.0.1:1"}).Authorization(); got != "" {
		t.Fatalf("an endpoint without a password must send no credentials: %q", got)
	}
	got := (Endpoint{Username: "opencode", Password: "secret"}).Authorization()
	if got == "" || got == "secret" {
		t.Fatalf("unexpected authorization %q", got)
	}
}
