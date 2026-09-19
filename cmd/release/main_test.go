package main

import "testing"

func TestReleaseVerificationSummaryKeepsWindowsVetOutOfDefaultGate(t *testing.T) {
	verification := releaseVerificationSummary(options{})
	if verification["windows_vet"] != "not run (out of scope)" {
		t.Fatalf("default Windows vet status=%v", verification["windows_vet"])
	}
	if verification["windows_cross_build"] != "PASS (artifact build)" {
		t.Fatalf("artifact build status=%v", verification["windows_cross_build"])
	}
	verification = releaseVerificationSummary(options{windowsVerify: true})
	if verification["windows_vet"] != "PASS" {
		t.Fatalf("opt-in Windows vet status=%v", verification["windows_vet"])
	}
}
