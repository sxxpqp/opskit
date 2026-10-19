package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
)

func newGoInstallCommand(parent context.Context, out, diag io.Writer) (*cobra.Command, error) {
	var defaults fileConfig
	if err := yaml.Unmarshal(opskit.ConfigTemplate, &defaults); err != nil {
		return nil, err
	}
	cmd := &cobra.Command{Use: "install"}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一配置文件")
	version := f.String("version", defaults.Go.Version, "Go 完整版本 1.X.Y；配置 go.version")
	base := f.String("go-download-base", defaults.Install.Go.DownloadBase, "SDK 下载根地址；GO_DOWNLOAD_BASE / install.go.download_base")
	metadata := f.String("checksum-url", defaults.Install.Go.ChecksumURL, "Go 发布元数据 JSON 地址；install.go.checksum_url")
	proxy := f.String("proxy", defaults.Mirrors.Go, "安装后写入 GOPROXY；配置 mirrors.go")
	dir := f.String("install-dir", "", "完整 SDK 目录；默认 ~/.local/share/opskit/go/go<版本>")
	links := f.String("link-dir", "", "go/gofmt 入口目录；优先复用 PATH 中可写目录")
	downloads := f.String("download-dir", "", "统一下载目录；默认 ~/.opskit/downloads")
	dry := f.Bool("dry-run", false, "仅预览，不下载、安装或修改配置")
	timeout := f.Duration("timeout", 15*time.Minute, "安装总超时")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("多余参数或 timeout 无效")
		}
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		setConfigFlag(f, "version", c.Go.Version)
		setEnvFlag(f, "go-download-base", "GO_DOWNLOAD_BASE")
		setConfigFlag(f, "go-download-base", c.Install.Go.DownloadBase)
		setConfigFlag(f, "checksum-url", c.Install.Go.ChecksumURL)
		setConfigFlag(f, "proxy", c.Mirrors.Go)
		setConfigFlag(f, "download-dir", c.Install.DownloadDir)
		*version = strings.TrimPrefix(strings.TrimPrefix(*version, "go"), "v")
		if !versionPattern.MatchString("v"+*version) || !strings.HasPrefix(*version, "1.") {
			return 2, errors.New("Go 版本必须是完整的 1.X.Y，不接受 latest")
		}
		if (runtime.GOOS != "linux" && runtime.GOOS != "darwin") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
			return 2, errors.New("go install 仅支持 Linux/macOS amd64、arm64")
		}
		for name, raw := range map[string]string{"go-download-base": *base, "checksum-url": *metadata} {
			u, e := url.Parse(raw)
			if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" || (name == "go-download-base" && u.RawQuery != "") {
				return 2, fmt.Errorf("%s 必须是无凭据或片段的 HTTP(S) 地址；下载根地址不能包含查询参数", name)
			}
		}
		if _, err = renderLanguage("go", nil, *proxy); err != nil {
			return 2, err
		}
		if *dir == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return 1, e
			}
			*dir = filepath.Join(home, ".local", "share", "opskit", "go", "go"+*version)
		}
		*dir, err = filepath.Abs(*dir)
		if err != nil {
			return 1, err
		}
		if *downloads == "" {
			*downloads, err = defaultDownloadDir()
			if err != nil {
				return 1, err
			}
		}
		asset := fmt.Sprintf("go%s.%s-%s.tar.gz", *version, runtime.GOOS, runtime.GOARCH)
		source := strings.TrimRight(*base, "/") + "/" + asset
		fmt.Fprintf(out, "go install: go%s (%s/%s)\n下载: %s\n校验元数据: %s\n下载目录: %s\n安装目录: %s\nGOPROXY: %s\n", *version, runtime.GOOS, runtime.GOARCH, source, *metadata, *downloads, *dir, *proxy)
		if *dry {
			fmt.Fprintln(out, "仅预览：未下载、安装或修改配置。")
			return 0, nil
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		if *links == "" {
			*links, err = defaultKindLinkDir()
			if err != nil {
				return 1, err
			}
		}
		if err = installGoSDK(ctx, source, *metadata, asset, *version, *dir, *downloads); err != nil {
			return 1, err
		}
		goBin := filepath.Join(*dir, "bin", "go")
		if err = configureGoProxy(ctx, goBin, *proxy, out); err != nil {
			return 1, fmt.Errorf("Go SDK 已安装，但代理设置失败: %w", err)
		}
		for _, tool := range []string{"go", "gofmt"} {
			// Both entries belong to the same managed Go SDK tree.
			if err = exposeTool(filepath.Join(*dir, "bin", tool), *links, "go", out); err != nil {
				return 1, err
			}
		}
		if err = verifyToolCommand(ctx, *links, "go", "go", *version, verifyGo, out); err != nil {
			return 1, err
		}
		fmt.Fprintf(out, "Go go%s 安装完成；GOPROXY=%s\n", *version, *proxy)
		return 0, nil
	})
	return cmd, nil
}

func goCommand(ctx context.Context, binary string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "GOROOT" && key != "GOTOOLCHAIN" {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
	return cmd
}
func verifyGo(ctx context.Context, binary, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, err := goCommand(ctx, binary, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("go version 失败: %w: %s", err, b)
	}
	expected := fmt.Sprintf("go version go%s %s/%s", version, runtime.GOOS, runtime.GOARCH)
	if strings.TrimSpace(string(b)) != expected {
		return fmt.Errorf("Go 版本或平台不匹配，要求 %s，返回 %s", expected, strings.TrimSpace(string(b)))
	}
	return nil
}
func goArchiveChecksum(raw []byte, asset, version string) (string, error) {
	var releases []struct {
		Version string `json:"version"`
		Files   []struct {
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &releases); err != nil {
		return "", fmt.Errorf("Go 校验元数据无效: %w", err)
	}
	for _, release := range releases {
		if release.Version != "go"+version {
			continue
		}
		for _, file := range release.Files {
			if file.Filename != asset {
				continue
			}
			sum, err := hex.DecodeString(file.SHA256)
			if err != nil || len(sum) != sha256.Size {
				return "", errors.New("Go SHA-256 无效")
			}
			return hex.EncodeToString(sum), nil
		}
	}
	return "", fmt.Errorf("Go 发布元数据未找到 %s；请检查版本或 checksum-url", asset)
}
func installGoSDK(ctx context.Context, source, metadata, asset, version, dir, downloads string) error {
	// Never merge a distribution into an existing tree.
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("安装目录已存在，未覆盖: %s；请使用新的 --install-dir", dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".opskit-go-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var metadataBody bytes.Buffer
	if err = downloadRetained(ctx, metadata, &metadataBody, downloads, "go", version); err != nil {
		return err
	}
	expected, err := goArchiveChecksum(metadataBody.Bytes(), asset, version)
	if err != nil {
		return err
	}
	archive, err := os.CreateTemp(stage, "sdk-*.tar.gz")
	if err != nil {
		return err
	}
	defer archive.Close()
	hash := sha256.New()
	if err = downloadRetained(ctx, source, io.MultiWriter(archive, hash), downloads, "go", version); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("Go SDK SHA-256 不匹配；未安装")
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err = extractGoSDK(ctx, archive, stage); err != nil {
		return err
	}
	root := filepath.Join(stage, "go")
	for _, binary := range []string{"go", "gofmt"} {
		info, e := os.Lstat(filepath.Join(root, "bin", binary))
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("Go SDK 缺少可执行文件 %s", binary)
		}
	}
	if err = verifyGo(ctx, filepath.Join(root, "bin", "go"), version); err != nil {
		return err
	}
	if _, err = os.Lstat(dir); !os.IsNotExist(err) {
		return fmt.Errorf("安装目录已被创建，未覆盖: %s", dir)
	}
	return os.Rename(root, dir)
}
func extractGoSDK(ctx context.Context, archive io.Reader, destination string) error {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !fs.ValidPath(name) || strings.Contains(name, "\\") || (name != "go" && !strings.HasPrefix(name, "go/")) {
			return fmt.Errorf("Go 归档路径无效: %s", h.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644|os.FileMode(h.Mode)&0111)
			if e != nil {
				return e
			}
			_, err = io.Copy(f, tr)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("Go 归档不支持链接或特殊文件: %s", h.Name)
		}
	}
}
func configureGoProxy(ctx context.Context, binary, proxy string, out io.Writer) error {
	b, err := goCommand(ctx, binary, "env", "GOENV").Output()
	if err != nil {
		return err
	}
	file := strings.TrimSpace(string(b))
	if file == "" || file == "off" || !filepath.IsAbs(file) {
		return errors.New("GOENV 已关闭或路径无效，无法持久设置 GOPROXY")
	}
	old, err := readOptional(file)
	if err != nil {
		return err
	}
	data, err := renderLanguage("go", old, proxy)
	if err != nil {
		return err
	}
	if err = writeConfigFile(file, data, out); err != nil {
		return err
	}
	fmt.Fprintln(out, "Go 代理配置:", file)
	if inherited := os.Getenv("GOPROXY"); inherited != "" && inherited != proxy {
		fmt.Fprintln(out, "当前 GOPROXY 环境变量优先于持久配置，请同步调整或 unset GOPROXY。")
	}
	if os.Getenv("GOROOT") != "" {
		fmt.Fprintln(out, "检测到 GOROOT 环境变量，请移除或调整为新 SDK 目录。")
	}
	return nil
}
