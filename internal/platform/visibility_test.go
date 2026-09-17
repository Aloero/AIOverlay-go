package platform

import "testing"

func TestVisibilityUserHideSurvivesTransientSuppression(t *testing.T) {
	var v visibilityState
	if !v.mayShow() {
		t.Fatal("initial")
	}
	release := v.suppress()
	if v.mayShow() {
		t.Fatal("suppression ignored")
	}
	if v.toggleUser() {
		t.Fatal("hide during suppression became visible")
	}
	release()
	if v.mayShow() {
		t.Fatal("user hide lost when suppression ended")
	}
	if !v.toggleUser() || !v.mayShow() {
		t.Fatal("explicit show failed")
	}
}
func TestVisibilitySuppressionsAreReferenceCounted(t *testing.T) {
	var v visibilityState
	a := v.suppress()
	b := v.suppress()
	a()
	if v.mayShow() {
		t.Fatal("second suppression lost")
	}
	b()
	if !v.mayShow() {
		t.Fatal("suppression never cleared")
	}
	b()
	if !v.mayShow() {
		t.Fatal("idempotent release broke state")
	}
}
