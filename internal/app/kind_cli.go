package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func runKindInstall(parent context.Context, args []string, out, diag io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, "用法: opskit kind install --version v0.17.0 [--bin-dir DIR] [--download-base URL]")
		fmt.Fprintln(out, "默认安装到 ~/.local/share/opskit/kind/<版本>/，并创建 PATH 命令入口；下载保存到统一 downloads 目录。")
		return nil
	}
	if len(args) < 2 || args[0] != "kind" || args[1] != "install" {
		return errors.New("未知命令，请运行 opskit --help")
	}
	f := flag.NewFlagSet("kind install", flag.ContinueOnError)
	f.SetOutput(diag)
	configPath := f.String("config", defaultConfigPath, "统一配置文件")
	version := f.String("version", "", "kind 版本，例如 v0.17.0；也可由 versions.kind 提供")
	downloadDir := f.String("download-dir", "", "统一下载目录，默认 ./downloads")
	linkDir := f.String("link-dir", "", "kind 命令入口目录；root 默认 /usr/local/bin，其他用户 ~/.local/bin")
	binDir := f.String("bin-dir", "", "安装目录，默认按版本隔离")
	base := f.String("download-base", defaultDownloadBase(), "发布包下载源（默认 Nexus，支持 https://github.com）")
	if err := f.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return errors.New("存在多余参数，请使用 --version 指定版本")
	}
	c, err := commandConfig(f, *configPath)
	if err != nil {
		return err
	}
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
		return errors.New("必须指定 --version vX.Y.Z 或配置 versions.kind")
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
	return exposeKind(filepath.Join(*binDir, binary), *linkDir, out)
}

func defaultDownloadBase() string {
	if base := os.Getenv("KIND_DOWNLOAD_BASE"); base != "" {
		return base
	}
	return "https://nexus.ihome.sxxpqp.top:8443/repository/raw-github"
}
