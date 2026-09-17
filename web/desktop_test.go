package webassets

import (
	"strings"
	"testing"
)

func TestDesktopBundleIsSelfContained(t *testing.T) {
	html, e := DesktopHTML()
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"studioDispatch", "__studioReply", "<style>", "<script>", "DOMContentLoaded"} {
		if !strings.Contains(html, s) {
			t.Error(s)
		}
	}
	for _, s := range []string{`src="/assets/`, `href="/assets/`, `https://cdn.`} {
		if strings.Contains(html, s) {
			t.Error("external reference", s)
		}
	}
}
