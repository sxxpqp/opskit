package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/sxxpqp/opskit/internal/selfupdate"
)

func TestPublicationConfigurationFailsBeforeNetwork(t *testing.T) {
	t.Setenv("MINIO_PATH_STYLE", "")
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"MINIO_ENDPOINT": "https://s3.example.com", "MINIO_BUCKET": "opskit",
		"MINIO_ACCESS_KEY": "test", "MINIO_SECRET_KEY": "test",
		"UPDATE_PUBLIC_KEY":          base64.StdEncoding.EncodeToString(public),
		"UPDATE_SIGNING_PRIVATE_KEY": base64.StdEncoding.EncodeToString(private),
	} {
		t.Setenv(name, value)
	}
	if _, err := validateConfiguration(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINIO_ENDPOINT", "https://example.com/opskit")
	if _, err := validateConfiguration(); err == nil {
		t.Fatal("bucket URL accepted as S3 API root")
	}
	t.Setenv("MINIO_ENDPOINT", "https://s3.example.com")
	t.Setenv("UPDATE_PUBLIC_KEY", "incorrect")
	if _, err := validateConfiguration(); err == nil {
		t.Fatal("mismatched signing keys accepted")
	}
	t.Setenv("MINIO_SECRET_KEY", "")
	if _, err := validateConfiguration(); err == nil {
		t.Fatal("missing upload secret accepted")
	}
}

type requestTransport func(*http.Request) (*http.Response, error)

func (f requestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMinioBucketDomainAddressing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pathStyle  bool
		host, path string
	}{
		{"bucket-domain", false, "opskit.sxxpqp.top", "/releases/v1.2.3/file"},
		{"path-style", true, "sxxpqp.top", "/opskit/releases/v1.2.3/file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""), HTTPClient: &http.Client{Transport: requestTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != tc.host || req.URL.Path != tc.path {
					t.Errorf("unexpected S3 address: %s", req.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": []string{`"test"`}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})}}
			client := newMinioClient(cfg, "https://sxxpqp.top", tc.pathStyle)
			if err := putImmutable(context.Background(), client, "opskit", "releases/v1.2.3/file", []byte("binary"), "application/octet-stream"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMinioAddressingConfiguration(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		t.Setenv("MINIO_PATH_STYLE", value)
		got, err := minioPathStyle()
		if err != nil || got != (value == "true") {
			t.Fatalf("%q: %t %v", value, got, err)
		}
	}
	t.Setenv("MINIO_PATH_STYLE", "invalid")
	if _, err := minioPathStyle(); err == nil {
		t.Fatal("invalid addressing accepted")
	}
}

func TestMinioLargeUploadUsesPlainBody(t *testing.T) {
	data := []byte(strings.Repeat("x", 17<<20))
	cfg := aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""), HTTPClient: &http.Client{Transport: requestTransport(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.Header.Get("Content-Encoding"), "aws-chunked") || req.Header.Get("X-Amz-Trailer") != "" {
			t.Error("optional streaming checksum framing still enabled")
		}
		if req.ContentLength != int64(len(data)) {
			t.Errorf("Content-Length: %d", req.ContentLength)
		}
		n, err := io.Copy(io.Discard, req.Body)
		if err != nil || n != int64(len(data)) {
			t.Errorf("unexpected upload body: %d %v", n, err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}}
	client := newMinioClient(cfg, "https://sxxpqp.top", false)
	if err := putImmutable(context.Background(), client, "opskit", "releases/v1.2.3/file", data, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseRequiresCompleteMatchingArtifacts(t *testing.T) {
	dir := t.TempDir()
	var checksums strings.Builder
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := selfupdate.AssetName(platform, arch)
			data := []byte(name)
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256(data), name)
		}
	}
	data := []byte("config")
	if err := os.WriteFile(filepath.Join(dir, "config.example.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&checksums, "%x  config.example.yaml\n", sha256.Sum256(data))
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(checksums.String()), 0600); err != nil {
		t.Fatal(err)
	}
	m, files, err := releaseFiles(dir, "v1.2.3", "v1.2.3-abcdef0")
	if err != nil || len(files) != 8 || len(m.Assets) != 8 {
		t.Fatalf("%v %v", m, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opskit_linux_amd64"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := releaseFiles(dir, "v1.2.3", "v1.2.3-abcdef0"); err == nil {
		t.Fatal("corrupt release accepted")
	}
}

func TestImmutableUploadRetryNeverOverwrites(t *testing.T) {
	existing := []byte("original")
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("missing immutable write precondition")
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusPreconditionFailed)
			io.WriteString(w, "<Error><Code>PreconditionFailed</Code><Message>exists</Message></Error>")
			return
		}
		gets.Add(1)
		w.Write(existing)
	}))
	defer server.Close()
	client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""), RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired})
	if err := putImmutable(context.Background(), client, "opskit", "releases/v1.2.3/file", existing, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if err := putImmutable(context.Background(), client, "opskit", "releases/v1.2.3/file", []byte("different"), "application/octet-stream"); err == nil {
		t.Fatal("immutable object changed")
	}
	if gets.Load() != 2 {
		t.Fatal(gets.Load())
	}
}
