package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sxxpqp/opskit/internal/k8s"
)

func newKubectlInstallCommand(parent context.Context, out, diag io.Writer) (*cobra.Command, error) {
	defaults, err := defaultInstallSettings()
	if err != nil {
		return nil, err
	}
	cmd := &cobra.Command{Use: "install"}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一配置文件")
	version := f.String("version", "", "kubectl 完整版本 vX.Y.Z；默认 k8s.kubernetes.version，否则跟随 k8s.kubernetes.version")
	base := f.String("kubectl-download-base", defaults.Kubectl.DownloadBase, "下载源根地址；对应 KUBECTL_DOWNLOAD_BASE 环境变量和 install.kubectl.download_base 配置")
	binDir := f.String("bin-dir", "", "安装目录；默认 ~/.local/share/opskit/kubectl/<版本>")
	linkDir := f.String("link-dir", "", "命令入口目录；优先复用 PATH 已有可写目录")
	downloads := f.String("download-dir", "", "统一下载目录；默认 ~/.opskit/downloads")
	dryRun := f.Bool("dry-run", false, "仅预览，不下载或安装")
	timeout := f.Duration("timeout", 5*time.Minute, "安装总超时")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 {
			return 2, errors.New("多余参数或 timeout 无效")
		}
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		setConfigFlag(f, "version", c.Versions.Kubectl)
		setConfigFlag(f, "version", c.Versions.Kubernetes)
		setEnvFlag(f, "kubectl-download-base", "KUBECTL_DOWNLOAD_BASE")
		setConfigFlag(f, "kubectl-download-base", c.Install.Kubectl.DownloadBase)
		setConfigFlag(f, "download-dir", c.Install.DownloadDir)
		if !versionPattern.MatchString(*version) {
			return 2, errors.New("必须指定 --version vX.Y.Z 或配置 k8s.kubernetes.version")
		}
		binary, err := k8s.KubectlAsset(runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return 2, err
		}
		u, err := url.Parse(*base)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return 2, errors.New("kubectl-download-base 必须是无凭据、查询参数或片段的 HTTP(S) 根地址")
		}
		if *downloads == "" {
			*downloads, err = defaultDownloadDir()
			if err != nil {
				return 1, err
			}
		}
		if *binDir == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return 1, e
			}
			*binDir = filepath.Join(home, ".local", "share", "opskit", "kubectl", *version)
		}
		source := fmt.Sprintf("%s/%s/bin/%s/%s/%s", strings.TrimRight(*base, "/"), *version, runtime.GOOS, runtime.GOARCH, binary)
		fmt.Fprintf(out, "kubectl install: %s (%s/%s)\n下载: %s\n下载目录: %s\n安装目录: %s\n", *version, runtime.GOOS, runtime.GOARCH, source, *downloads, *binDir)
		if *dryRun {
			fmt.Fprintln(out, "仅预览：未下载、安装或修改 PATH。")
			return 0, nil
		}
		if *linkDir == "" {
			*linkDir, err = defaultKindLinkDir()
			if err != nil {
				return 1, err
			}
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		o := binaryInstallOptions{kindOptions: kindOptions{*version, *binDir, *base, runtime.GOOS, runtime.GOARCH, *downloads}, tool: "kubectl", binary: binary, source: source, checksumSuffix: ".sha256", verify: k8s.VerifyKubectl}
		if err = installVerifiedBinary(ctx, o, out); err != nil {
			return 1, err
		}
		if err = exposeTool(filepath.Join(*binDir, binary), *linkDir, "kubectl", out); err != nil {
			return 1, err
		}
		if err = verifyToolCommand(ctx, *linkDir, "kubectl", binary, *version, k8s.VerifyKubectl, out); err != nil {
			return 1, err
		}
		return 0, nil
	})
	return cmd, nil
}
