package childprocess

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestProcessExitIsObservedAndStopIsIdempotent(t *testing.T) {
	if os.Getenv("AIO_TEST_CHILD") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestProcessExitIsObservedAndStopIsIdempotent")
	cmd.Env = append(os.Environ(), "AIO_TEST_CHILD=1")
	p, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("child did not exit")
	}
	if p.Alive() {
		t.Fatal("dead process reused")
	}
	p.Stop()
	p.Stop()
}
