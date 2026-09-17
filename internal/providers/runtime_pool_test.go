package providers

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"aioverlay/internal/domain"
)

type fakeRuntime struct{ stops atomic.Int32 }

func (f *fakeRuntime) alive() bool { return f.stops.Load() == 0 }
func (f *fakeRuntime) stop()       { f.stops.Add(1) }
func TestCredentialRuntimeLeasesNeverKillOtherKeys(t *testing.T) {
	var pool credentialRuntimes
	created := map[string]*fakeRuntime{}
	create := func(_ context.Context, key string) (runtimeInstance, error) {
		v := &fakeRuntime{}
		created[key] = v
		return v, nil
	}
	a, releaseA, err := pool.acquire(context.Background(), "a", create)
	if err != nil {
		t.Fatal(err)
	}
	again, releaseAgain, _ := pool.acquire(context.Background(), "a", create)
	if a != again {
		t.Fatal("same credential did not reuse its process")
	}
	_, releaseB, _ := pool.acquire(context.Background(), "b", create)
	releaseAgain()
	releaseB()
	if created["a"].stops.Load() != 0 {
		t.Fatal("switching keys killed an active request")
	}
	_, releaseC, _ := pool.acquire(context.Background(), "c", create)
	releaseC()
	if created["b"].stops.Load() != 1 {
		t.Fatal("excess idle process retained")
	}
	if created["a"].stops.Load() != 0 {
		t.Fatal("idle eviction killed an active request")
	}
	releaseA()
	pool.close()
	for _, r := range created {
		if r.stops.Load() != 1 {
			t.Fatal("runtime not stopped exactly once")
		}
	}
}
func TestCredentialRuntimeConcurrentSameKeyStartsOnce(t *testing.T) {
	var pool credentialRuntimes
	var starts atomic.Int32
	entered, proceed := make(chan struct{}), make(chan struct{})
	create := func(context.Context, string) (runtimeInstance, error) {
		if starts.Add(1) == 1 {
			close(entered)
		}
		<-proceed
		return &fakeRuntime{}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, release, err := pool.acquire(context.Background(), "same", create)
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
	pool.close()
	if starts.Load() != 1 {
		t.Fatalf("starts=%d", starts.Load())
	}
}
func TestOpenCodeErrorEnvelopeIsStructuredAndDoesNotEchoPayload(t *testing.T) {
	for _, c := range []struct {
		status int
		out    domain.Outcome
	}{{401, domain.OutcomeAuthFailed}, {429, domain.OutcomeRateLimited}, {503, domain.OutcomeProviderUnavailable}} {
		raw, _ := json.Marshal(map[string]any{"name": "APIError", "data": map[string]any{"statusCode": c.status, "message": "SECRET", "responseBody": "SECRET"}})
		got := openCodeError(raw)
		if got.Outcome != c.out || got.StatusCode != c.status || got.Message != "OpenCode provider request failed" {
			t.Fatal(got)
		}
	}
}
