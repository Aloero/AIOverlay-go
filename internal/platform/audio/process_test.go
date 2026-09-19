package audio

import (
	"errors"
	"testing"
	"time"
)

func TestSelectProcessIdentityUsesExactExecutablePath(t *testing.T) {
	started := time.Unix(100, 0)
	processes := []Process{
		{PID: 10, Name: "Zoom.exe", Path: `C:\Program Files\Zoom\bin\Zoom.exe`, StartedAt: started},
		{PID: 11, Name: "Zoom.exe", Path: `C:\Users\user\Downloads\Zoom.exe`, StartedAt: started},
	}
	identity, err := selectProcessIdentity(`c:/program files/zoom/bin/ZOOM.EXE`, processes)
	if err != nil {
		t.Fatal(err)
	}
	if identity.PID != 10 || identity.Path != processes[0].Path || !identity.StartedAt.Equal(started) {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestSelectProcessIdentityNeverGuessesAmbiguousInstance(t *testing.T) {
	path := `C:\Program Files\Zoom\bin\Zoom.exe`
	started := time.Unix(100, 0)
	_, err := selectProcessIdentity(path, []Process{
		{PID: 10, ParentPID: 1, Path: path, StartedAt: started},
		{PID: 11, ParentPID: 2, Path: path, StartedAt: started.Add(time.Second)},
	})
	var processErr *ProcessCaptureError
	if !errors.As(err, &processErr) || processErr.Kind != ProcessCaptureAmbiguous {
		t.Fatalf("ambiguous process error=%v", err)
	}
}

func TestSelectProcessIdentityChoosesUniqueTreeRootAmongSameExecutableChildren(t *testing.T) {
	path := `C:\Program Files\Zoom\bin\Zoom.exe`
	started := time.Unix(100, 0)
	identity, err := selectProcessIdentity(path, []Process{
		{PID: 10, ParentPID: 1, Path: path, StartedAt: started},
		{PID: 11, ParentPID: 10, Path: path, StartedAt: started.Add(time.Second)},
		{PID: 12, ParentPID: 10, Path: path, StartedAt: started.Add(2 * time.Second)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.PID != 10 || !identity.StartedAt.Equal(started) {
		t.Fatalf("tree root identity=%+v", identity)
	}
}

func TestProcessIdentityRejectsPIDReuse(t *testing.T) {
	expected := processIdentity{PID: 42, Path: `C:\Program Files\Zoom\bin\Zoom.exe`, StartedAt: time.Unix(100, 0)}
	if !sameProcessIdentity(expected, expected) {
		t.Fatal("same process identity rejected")
	}
	reusedPID := expected
	reusedPID.StartedAt = time.Unix(200, 0)
	if sameProcessIdentity(expected, reusedPID) {
		t.Fatal("PID reuse was accepted as the original process")
	}
	wrongPath := expected
	wrongPath.Path = `C:\Other\Zoom.exe`
	if sameProcessIdentity(expected, wrongPath) {
		t.Fatal("same PID with a different executable was accepted")
	}
}

func TestRecoverableProcessTargetOnlyRetriesMissingOrChangedTarget(t *testing.T) {
	for _, kind := range []ProcessCaptureErrorKind{ProcessCaptureUnavailable, ProcessCaptureChanged} {
		if !RecoverableProcessTarget(&ProcessCaptureError{Kind: kind}) {
			t.Fatalf("recoverable process error %s rejected", kind)
		}
	}
	for _, kind := range []ProcessCaptureErrorKind{ProcessCaptureUnsupported, ProcessCaptureAmbiguous} {
		if RecoverableProcessTarget(&ProcessCaptureError{Kind: kind}) {
			t.Fatalf("non-recoverable process error %s accepted", kind)
		}
	}
}
