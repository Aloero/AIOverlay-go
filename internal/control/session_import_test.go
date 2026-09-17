package control

import (
	"aioverlay/internal/domain"
	"testing"
)

func TestImportDisablesSensitiveSourceAutoStart(t *testing.T) {
	c := domain.Config{Pipelines: []domain.Pipeline{{Sources: []domain.EventSource{{ID: "a", AutoStart: true}, {ID: "b", AutoStart: false}}}}}
	if n := disableImportedAutoStart(&c); n != 1 || c.Pipelines[0].Sources[0].AutoStart {
		t.Fatalf("n=%d %+v", n, c.Pipelines[0].Sources)
	}
}
