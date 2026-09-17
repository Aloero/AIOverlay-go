package engine

import "aioverlay/internal/domain"

// Snapshot ordering for the next request without consuming live round-robin
// cursors or health. Weighted ordering remains conditional, not a prediction.
func (e *Engine) PreviewOrder(key string, p domain.ModelPoolConfig, models []domain.ModelSlot) []domain.ModelSlot {
	shadow := New(domain.Config{}, nil, nil, nil)
	shadow.ImportStatistics(e.ExportStatistics())
	return shadow.orderPoolModels(key, p, models)
}
