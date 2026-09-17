package desktop

// sessionLifecycle is the small state machine behind lazy Studio sessions.
// It deliberately knows nothing about WebView2 so reopen/close behavior can be
// tested on every platform.
type sessionLifecycle struct {
	running bool
	closing bool
	reopen  bool
}

func (s *sessionLifecycle) requestOpen() (start, show bool) {
	switch {
	case !s.running:
		s.running = true
		s.closing = false
		s.reopen = false
		return true, false
	case s.closing:
		s.reopen = true
		return false, false
	default:
		return false, true
	}
}

func (s *sessionLifecycle) requestClose() bool {
	if !s.running || s.closing {
		return false
	}
	s.closing = true
	return true
}

// sessionEnded resets the old session. A queued reopen becomes the next
// running session and is returned to the manager as an action.
func (s *sessionLifecycle) sessionEnded() bool {
	restart := s.reopen
	s.running = restart
	s.closing = false
	s.reopen = false
	return restart
}
