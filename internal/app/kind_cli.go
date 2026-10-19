package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

func newKindInstallCommand(parent context.Context, out, diag io.Writer) (*cobra.Command, error) {
	defaults, err := defaultInstallSettings()
	if err != nil {
		return nil, err
	}
	cmd := &cobra.Command{Use: "install"}
	f := cmd.Flags()
	configPath := f.String("config", defaultConfigPath, "统一配置文件")
	version := f.String("version", "", "kind 版本，例如 v0.17.0；也可由 kind.version 提供")
	downloadDir := f.String("download-dir", "", "统一下载目录，默认 ~/.opskit/downloads")
	linkDir := f.String("link-dir", "", "kind 命令入口目录；优先复用 PATH 已有可写目录，否则配置用户目录")
	binDir := f.String("bin-dir", "", "安装目录，默认按版本隔离")
	base := f.String("download-base", defaults.Kind.DownloadBase, "发布包下载源；对应 KIND_DOWNLOAD_BASE 和 install.kind.download_base")
	bindCommand(cmd, func() (int, error) {
		err := func() error {
			if f.NArg() != 0 {
				return errors.New("存在多余参数，请使用 --version 指定版本")
			}
			c, err := commandConfig(f, *configPath)
			if err != nil {
				return err
			}
			setEnvFlag(f, "download-base", "KIND_DOWNLOAD_BASE")
			setConfigFlag(f, "download-base", c.Install.Kind.DownloadBase)
			setConfigFlag(f, "download-dir", c.Install.DownloadDir)
			if *downloadDir == "" {
				*downloadDir, err = defaultDownloadDir()
				if err != nil {
					return err
				}
			}
			if *linkDir == "" {
				*linkDir, err = defaultKindLinkDir()
				if err != nil {
					return err
				}
			}
			setEnvFlag(f, "version", "OPSKIT_KIND_VERSION")
			setConfigFlag(f, "version", c.Versions.Kind)
			if !versionPattern.MatchString(*version) {
				return errors.New("必须指定 --version vX.Y.Z 或配置 kind.version")
			}
			if *binDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				*binDir = filepath.Join(home, ".local", "share", "opskit", "kind", *version)
			}
			ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
			defer cancel()
			fmt.Fprintln(out, "下载目录:", *downloadDir)
			if err := installKind(ctx, kindOptions{*version, *binDir, *base, runtime.GOOS, runtime.GOARCH, *downloadDir}, out); err != nil {
				return err
			}
			_, binary, err := kindAsset(runtime.GOOS, runtime.GOARCH)
			if err != nil {
				return err
			}
			if err := exposeKind(filepath.Join(*binDir, binary), *linkDir, out); err != nil {
				return err
			}
			return verifyKindCommand(ctx, *linkDir, *version, out)
		}()
		if err != nil {
			return 1, err
		}
		return 0, nil
	})
	return cmd, nil
}
