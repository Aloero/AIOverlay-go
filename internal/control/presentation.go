package control

import (
	"aioverlay/internal/session"
	"time"
)

// PresentationArbiter decides which completed background answer may replace the
// current overlay content. It does not control inference, cancellation, or window
// visibility; those are separate responsibilities.
type PresentationArbiter struct {
	manualFence time.Time
	lastAutoID  uint64
}

func (a *PresentationArbiter) ObserveJob(job Job) {
	if !isManualPresentationJob(job) {
		return
	}
	if job.Started.After(a.manualFence) {
		a.manualFence = job.Started
	}
	if !job.Finished.IsZero() && job.Finished.After(a.manualFence) {
		a.manualFence = job.Finished
	}
}

func (a *PresentationArbiter) AcceptBackground(job Job, publication session.Publication) bool {
	if publication.ID == 0 || publication.ID <= a.lastAutoID {
		return false
	}
	a.lastAutoID = publication.ID
	a.ObserveJob(job)
	if job.Running || publication.Text == "" {
		return false
	}
	return publication.TriggeredAt.After(a.manualFence)
}

func isManualPresentationJob(job Job) bool {
	return job.ID != "" && job.Purpose != "connection_test"
}
