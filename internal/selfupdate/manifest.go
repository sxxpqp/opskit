// Package selfupdate defines the signed release index shared by CI and clients.
package selfupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Settings struct {
	DownloadBase string `yaml:"download_base"`
	PublicKey    string `yaml:"public_key"`
}

type Asset struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion int              `json:"schema_version"`
	Version       string           `json:"version"`
	BuildVersion  string           `json:"build_version"`
	Assets        map[string]Asset `json:"assets"`
}

// Payload is base64 rather than RawMessage so formatting cannot change signed bytes.
type envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

var versionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func Compare(a, b string) (int, error) {
	aa, bb := versionRE.FindStringSubmatch(a), versionRE.FindStringSubmatch(b)
	if aa == nil || bb == nil {
		return 0, fmt.Errorf("版本必须为完整正式版本 vX.Y.Z: %q / %q", a, b)
	}
	for i := 1; i <= 3; i++ {
		x, err := strconv.ParseUint(aa[i], 10, 64)
		if err != nil {
			return 0, err
		}
		y, err := strconv.ParseUint(bb[i], 10, 64)
		if err != nil {
			return 0, err
		}
		if x < y {
			return -1, nil
		}
		if x > y {
			return 1, nil
		}
	}
	return 0, nil
}

func AssetName(platform, arch string) (string, error) {
	if (platform != "linux" && platform != "darwin" && platform != "windows") || (arch != "amd64" && arch != "arm64") {
		return "", fmt.Errorf("不支持的平台: %s/%s", platform, arch)
	}
	name := "opskit_" + platform + "_" + arch
	if platform == "windows" {
		name += ".exe"
	}
	return name, nil
}

func (m Manifest) Validate() error {
	if _, err := Compare(m.Version, m.Version); err != nil {
		return err
	}
	if m.SchemaVersion != 1 || (m.BuildVersion != m.Version && !strings.HasPrefix(m.BuildVersion, m.Version+"-")) {
		return fmt.Errorf("版本清单 schema 或 build_version 无效")
	}
	for name, asset := range m.Assets {
		if name == "" || strings.ContainsAny(name, "/\\") || asset.Size <= 0 || asset.Size > 512<<20 {
			return fmt.Errorf("版本资产无效: %s", name)
		}
		sum, err := hex.DecodeString(asset.SHA256)
		if err != nil || len(sum) != 32 {
			return fmt.Errorf("资产 SHA-256 无效: %s", name)
		}
	}
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := AssetName(platform, arch)
			if _, ok := m.Assets[name]; !ok {
				return fmt.Errorf("版本缺少资产: %s", name)
			}
		}
	}
	return nil
}

func Sign(m Manifest, key ed25519.PrivateKey) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("Ed25519 私钥长度无效")
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
}

func Verify(data []byte, publicKey string) (Manifest, error) {
	var m Manifest
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return m, fmt.Errorf("请配置可信的 update.public_key（base64 Ed25519 公钥）")
	}
	var e envelope
	if err = json.Unmarshal(data, &e); err != nil {
		return m, err
	}
	payload, err := base64.StdEncoding.DecodeString(e.Payload)
	if err != nil {
		return m, err
	}
	sig, err := base64.StdEncoding.DecodeString(e.Signature)
	if err != nil || !ed25519.Verify(key, payload, sig) {
		return m, fmt.Errorf("版本清单签名校验失败")
	}
	if err = json.Unmarshal(payload, &m); err != nil {
		return m, err
	}
	return m, m.Validate()
}

func URL(base, key string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("update.download_base 必须是无凭据、查询参数或片段的 HTTPS 根地址")
	}
	return strings.TrimRight(base, "/") + "/" + key, nil
}
