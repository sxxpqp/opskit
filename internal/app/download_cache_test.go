package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRetainedCacheIsolationAndMutableMetadata(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s:%d", r.URL.Path, requests.Add(1))
	}))
	defer server.Close()
	root := t.TempDir()
	read := func(name string) string {
		t.Helper()
		var dst bytes.Buffer
		if err := downloadRetained(context.Background(), server.URL+name, &dst, root, "test", "v1.0.0"); err != nil {
			t.Fatal(err)
		}
		return dst.String()
	}
	first := read("/v1/amd64/tool")
	if got := read("/v1/amd64/tool"); got != first || requests.Load() != 1 {
		t.Fatal("valid download was fetched again", got, requests.Load())
	}
	if read("/v1/arm64/tool") == first || read("/v2/amd64/tool") == first || requests.Load() != 3 {
		t.Fatal("different URLs shared cached data")
	}
	for _, name := range []string{"/metadata.json", "/install.sh"} {
		if read(name) == read(name) {
			t.Fatal("mutable metadata or script reused", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := downloadRetained(ctx, server.URL+"/v1/amd64/tool", io.Discard, root, "test", "v1.0.0"); err != context.Canceled {
		t.Fatal("cache hit ignored cancellation", err)
	}
}

func TestRetainedCacheDoesNotPublishPartialResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Length", "100")
			fmt.Fprint(w, "partial")
			return
		}
		fmt.Fprint(w, "complete")
	}))
	defer server.Close()
	root := t.TempDir()
	if err := downloadRetained(context.Background(), server.URL+"/binary", io.Discard, root, "test", "v1"); err == nil {
		t.Fatal("partial response accepted")
	}
	var dst bytes.Buffer
	if err := downloadRetained(context.Background(), server.URL+"/binary", &dst, root, "test", "v1"); err != nil || dst.String() != "complete" || requests.Load() != 2 {
		t.Fatal("partial response cached", err, dst.String(), requests.Load())
	}
}
