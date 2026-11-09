package hotkey

import (
	"errors"
	"testing"
)

func TestCanonicalCombinations(t *testing.T) {
	a, _ := Parse("Ctrl+Shift+S")
	b, _ := Parse("Shift+Control+S")
	if a != b {
		t.Fatal(a, b)
	}
	if Validate(map[string]string{"control": "Ctrl+Shift+S", "analyze": "Shift+Control+S"}) == nil {
		t.Fatal("duplicate")
	}
	if err := Validate(map[string]string{"control": "Ctrl+Shift+O", "quit": "Ctrl+Shift+Q"}); err != nil {
		t.Fatalf("quit action rejected: %v", err)
	}
	if Validate(map[string]string{"control": "Ctrl+Shift+Q", "quit": "Ctrl+Shift+Q"}) == nil {
		t.Fatal("duplicate with quit accepted")
	}
	for _, s := range []string{"garbage", "Ctrl+S+A", "Ctrl+Ctrl+O", "Ctrl+F12"} {
		if _, e := Parse(s); e == nil {
			t.Fatal(s)
		}
	}
}
func TestRegistrationFailureRollsBack(t *testing.T) {
	old := map[string]string{"control": "Ctrl+O"}
	next := map[string]string{"control": "Ctrl+S"}
	m := map[int]Binding{}
	bad, _ := Parse("Ctrl+S")
	err := Replace(old, next, func(i int, b Binding) error {
		if b == bad {
			return errors.New("occupied")
		}
		m[i] = b
		return nil
	}, func(i int) { delete(m, i) })
	if err == nil {
		t.Fatal("accepted")
	}
	b, _ := Parse("Ctrl+O")
	if m[4] != b {
		t.Fatal("old recovery not restored")
	}
}
