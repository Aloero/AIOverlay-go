package platform

import (
	"strings"
	"testing"
)

func TestDisplayTextRejectsNULWithoutJoiningDigits(t *testing.T) {
	s := displayText("1\x002")
	if strings.ContainsRune(s, 0) || s == "12" {
		t.Fatal(s)
	}
}
