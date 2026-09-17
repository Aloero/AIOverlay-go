package catalog

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRefreshPersistsOnlyValidSnapshot(t *testing.T) {
	p := filepath.Join(t.TempDir(), "catalog.json")
	good := `{"p":{"name":"P","models":{"m":{"name":"M"}}}}`
	os.WriteFile(p, []byte(good), 0600)
	s, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"p":{"name":"P","models":{"new":{"name":"New"}}}}`
	s.client = &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	if err = s.RefreshContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Find("p/new"); !ok {
		t.Fatal("refresh lost after restart")
	}
	body = "{}"
	if err = s.RefreshContext(context.Background()); err == nil {
		t.Fatal("empty catalog accepted")
	}
	again, _ = New(p)
	if _, ok := again.Find("p/new"); !ok {
		t.Fatal("last good catalog lost")
	}
}
