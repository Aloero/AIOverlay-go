package audio

import (
	"fmt"
	"testing"
)

func TestWASAPIErrorClassification(t *testing.T) {
	cases := map[uint32]CaptureErrorKind{
		audclntEDeviceInvalidated:    CaptureErrorDeviceInvalidated,
		audclntEResourcesInvalidated: CaptureErrorResourcesInvalidated,
		audclntEServiceNotRunning:    CaptureErrorServiceNotRunning,
		0x80004005:                   CaptureErrorWASAPI,
	}
	for code, want := range cases {
		if got := classifyWASAPIHRESULT(code); got != want {
			t.Fatalf("HRESULT 0x%08x kind=%q want=%q", code, got, want)
		}
	}
}

func TestRecoverableDefaultEndpointErrorsAreExplicit(t *testing.T) {
	for _, kind := range []CaptureErrorKind{CaptureErrorDeviceInvalidated, CaptureErrorResourcesInvalidated, CaptureErrorServiceNotRunning} {
		if !RecoverableDefaultEndpoint(&CaptureError{Kind: kind}) {
			t.Fatalf("%s should allow default-endpoint reinitialization", kind)
		}
	}
	if RecoverableDefaultEndpoint(&CaptureError{Kind: CaptureErrorWASAPI}) {
		t.Fatal("generic WASAPI error enabled automatic retry")
	}
	if RecoverableDefaultEndpoint(fmt.Errorf("generic failure")) {
		t.Fatal("generic error enabled automatic retry")
	}
}
