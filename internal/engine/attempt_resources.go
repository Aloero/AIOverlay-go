package engine

import (
	"context"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

const providerCleanupTimeout = 1500 * time.Millisecond

// Ownership is either local to singleAttempt or transferred to the teardown
// waiter. Never free capacity merely because the caller stopped waiting.
type attemptResources struct {
	sem   chan struct{}
	lease CallLease
}

func acquireAttemptResources(ctx context.Context, sem chan struct{}, beforeCall func(context.Context) (CallLease, error)) (*attemptResources, error) {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resources := &attemptResources{sem: sem}
	transferred := false
	defer func() {
		if !transferred {
			resources.release()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if beforeCall != nil {
		lease, err := beforeCall(ctx)
		if err != nil {
			return nil, err
		}
		resources.lease = lease
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	transferred = true
	return resources, nil
}

func (resources *attemptResources) commit() {
	if resources.lease != nil {
		resources.lease.Commit()
	}
}

func (resources *attemptResources) release() {
	sem, lease := resources.sem, resources.lease
	resources.sem, resources.lease = nil, nil
	if sem != nil {
		defer func() { <-sem }()
	}
	if lease != nil {
		lease.Release()
	}
}

func (a *modelAttempt) cleanupProvider(cancel context.CancelFunc, resources *attemptResources, stream *attemptStream) {
	cancel()
	events, errs, complete := drainProviderChannels(stream.events, stream.errors, providerCleanupTimeout)
	if complete {
		return
	}
	a.emit(domain.TraceEvent{
		Kind: "worker_cleanup_timeout", Outcome: domain.OutcomeProviderUnavailable,
		Message: "Provider did not acknowledge cancellation within 1500ms; concurrency remains reserved until local teardown completes",
	})
	sem, lease := resources.sem, resources.lease
	resources.sem, resources.lease = nil, nil
	go holdAttemptResourcesUntilProviderStops(sem, lease, events, errs)
}

func drainProviderChannels(events <-chan providers.Event, errs <-chan error, timeout time.Duration) (<-chan providers.Event, <-chan error, bool) {
	if events == nil && errs == nil {
		return nil, nil, true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for events != nil || errs != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		case <-timer.C:
			return events, errs, false
		}
	}
	return nil, nil, true
}

func holdAttemptResourcesUntilProviderStops(sem chan struct{}, lease CallLease, events <-chan providers.Event, errs <-chan error) {
	for events != nil || errs != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		}
	}
	<-sem
	if lease != nil {
		lease.Release()
	}
}
