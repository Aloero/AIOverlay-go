package control

import (
	"aioverlay/internal/domain"
	"errors"
	"net/http"
)

var ErrRunActive = errors.New("Запрос уже выполняется. Настройки можно применить после его завершения или отмены.")

// Admission and configuration writes share a gate from BEFORE capture starts.
// Holding this gate never prevents cancellation or progress/trace snapshots.
func (j *JobManager) WhenIdle(change func() error) error {
	j.admission.Lock()
	defer j.admission.Unlock()
	j.mu.Lock()
	running := j.job.Running
	j.mu.Unlock()
	if running {
		return ErrRunActive
	}
	return change()
}
func (s *Server) saveConfiguration(cfg domain.Config) error {
	return s.jobs.WhenIdle(func() error { return s.backend.SaveConfig(cfg) })
}

type configImportBackend interface {
	ImportConfig(domain.Config) error
}

func (s *Server) importConfiguration(cfg domain.Config) (bool, error) {
	clearsRuntimeOverrides := false
	err := s.jobs.WhenIdle(func() error {
		if importer, ok := s.backend.(configImportBackend); ok {
			clearsRuntimeOverrides = true
			return importer.ImportConfig(cfg)
		}
		return s.backend.SaveConfig(cfg)
	})
	return clearsRuntimeOverrides, err
}
func settingsError(w http.ResponseWriter, err error, fallback int) {
	if errors.Is(err, ErrRunActive) {
		fallback = http.StatusConflict
	}
	http.Error(w, err.Error(), fallback)
}
