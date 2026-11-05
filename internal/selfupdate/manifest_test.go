package selfupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testManifest(data []byte) Manifest {
	m := Manifest{SchemaVersion: 1, Version: "v1.2.3", BuildVersion: "v1.2.3-abcdef0", Assets: map[string]Asset{}}
	sum := sha256.Sum256(data)
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := AssetName(platform, arch)
			m.Assets[name] = Asset{hex.EncodeToString(sum[:]), int64(len(data))}
		}
	}
	return m
}

func TestManifestTrustAndValidation(t *testing.T) {
	pub, private, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	m := testManifest([]byte("binary"))
	data, err := Sign(m, private)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Verify(data, key); err != nil || got.Version != m.Version {
		t.Fatalf("%v %v", got, err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	for _, key := range []string{"", "invalid", base64.StdEncoding.EncodeToString(other)} {
		if _, err := Verify(data, key); err == nil {
			t.Fatal("untrusted key accepted")
		}
	}
	var envelope envelope
	json.Unmarshal(data, &envelope)
	envelope.Payload = base64.StdEncoding.EncodeToString([]byte(`{"version":"v9.9.9"}`))
	changed, _ := json.Marshal(envelope)
	if _, err := Verify(changed, key); err == nil {
		t.Fatal("tampered payload accepted")
	}
	delete(m.Assets, "opskit_windows_arm64.exe")
	if _, err := Sign(m, private); err == nil {
		t.Fatal("incomplete release signed")
	}
}

func TestCompareAndURL(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"v1.10.0", "v1.9.9", 1}, {"v1.2.3", "v1.2.3", 0}, {"v0.9.0", "v1.0.0", -1}} {
		if got, err := Compare(tc.a, tc.b); err != nil || got != tc.want {
			t.Fatalf("%+v: %d %v", tc, got, err)
		}
	}
	for _, bad := range []string{"latest", "v01.2.3", "v1.2.3-beta", "v999999999999999999999.0.0"} {
		if _, err := Compare(bad, "v1.0.0"); err == nil {
			t.Fatal(bad)
		}
	}
	for _, bad := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com?x=1", "https://example.com/#x"} {
		if _, err := URL(bad, "channels/stable.json"); err == nil {
			t.Fatal(bad)
		}
	}
}

func TestDownloadLimitsAndChecksum(t *testing.T) {
	data := []byte("signed-binary")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer server.Close()
	asset := testManifest(data).Assets["opskit_linux_amd64"]
	var dst bytes.Buffer
	if err := DownloadAsset(context.Background(), server.Client(), server.URL, asset, &dst); err != nil || !bytes.Equal(dst.Bytes(), data) {
		t.Fatalf("%s %v", dst.Bytes(), err)
	}
	for _, invalid := range []Asset{{asset.SHA256, asset.Size - 1}, {asset.SHA256, asset.Size + 1}, {hex.EncodeToString(make([]byte, 32)), asset.Size}} {
		if err := DownloadAsset(context.Background(), server.Client(), server.URL, invalid, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid asset accepted")
		}
	}
	if err := Fetch(context.Background(), server.Client(), server.URL+"/missing", 100, &bytes.Buffer{}); err == nil {
		t.Fatal("404 ignored")
	}
}
