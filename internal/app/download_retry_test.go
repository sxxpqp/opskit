package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDownloadRetriesGatewayBeforeWriting(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "gateway error body", 504)
			return
		}
		_, _ = io.WriteString(w, "verified artifact")
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := download(context.Background(), server.URL, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || out.String() != "verified artifact" {
		t.Fatalf("calls=%d, body=%q", calls, out.String())
	}
}
func TestDownloadRetryPolicyAndCancellation(t *testing.T) {
	for _, status := range []int{401, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(status) }))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := download(ctx, server.URL, io.Discard)
			if err == nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if status == 503 && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}
