package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sxxpqp/opskit"
	"github.com/sxxpqp/opskit/internal/selfupdate"
	"gopkg.in/yaml.v3"
)

// Set by the release build. A configured key can explicitly override this trust root.
var updatePublicKey string

func newUpdateCommand(parent context.Context, out io.Writer, current string) (*cobra.Command, error) {
	var defaults fileConfig
	if err := yaml.Unmarshal(opskit.ConfigTemplate, &defaults); err != nil {
		return nil, err
	}
	cmd := &cobra.Command{Use: "update", Short: "从 MinIO 检查或更新 opskit 自身", Args: cobra.NoArgs}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一配置文件")
	base := f.String("update-download-base", defaults.Update.DownloadBase, "更新下载根地址；update.download_base / OPSKIT_UPDATE_DOWNLOAD_BASE")
	key := f.String("public-key", "", "可信 Ed25519 公钥（base64）；update.public_key，默认使用发布时嵌入公钥")
	version := f.String("version", "", "指定正式版本 vX.Y.Z；默认 channels/stable.json")
	check := f.Bool("check", false, "仅检查，不下载程序或安装")
	downloadOnly := f.Bool("download-only", false, "只下载并校验到 --download-dir，不替换程序")
	downloadDir := f.String("download-dir", ".", "仅下载时保存目录，已有同名文件不覆盖")
	installDir := f.String("install-dir", "", "最终实体文件目录；默认当前 opskit 所在目录")
	timeout := f.Duration("timeout", 5*time.Minute, "检查及升级总超时")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 || (*check && *downloadOnly) {
			return 2, errors.New("参数组合或 timeout 无效")
		}
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		setEnvFlag(f, "update-download-base", "OPSKIT_UPDATE_DOWNLOAD_BASE")
		setConfigFlag(f, "update-download-base", c.Update.DownloadBase)
		setConfigFlag(f, "public-key", c.Update.PublicKey)
		if *key == "" {
			*key = updatePublicKey
		}
		index := "channels/stable.json"
		if *version != "" {
			if _, err = selfupdate.Compare(*version, *version); err != nil {
				return 2, err
			}
			index = "releases/" + *version + "/manifest.json"
		}
		source, err := selfupdate.URL(*base, index)
		if err != nil {
			return 2, err
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		client := selfupdate.NewHTTPClient()
		var data bytes.Buffer
		if err = selfupdate.Fetch(ctx, client, source, 1<<20, &data); err != nil {
			return 1, err
		}
		manifest, err := selfupdate.Verify(data.Bytes(), *key)
		if err != nil {
			return 1, err
		}
		if *version != "" && manifest.Version != *version {
			return 1, errors.New("指定版本与签名清单不匹配")
		}
		installed := strings.SplitN(current, "-", 2)[0]
		comparison, err := selfupdate.Compare(manifest.Version, installed)
		if err != nil {
			return 2, fmt.Errorf("当前版本 %q 不是可升级的正式版本: %w", current, err)
		}
		fmt.Fprintf(out, "当前版本: %s；目标版本: %s\n", current, manifest.BuildVersion)
		if comparison < 0 {
			return 1, errors.New("拒绝降级，当前版本高于目标版本")
		}
		if comparison == 0 {
			fmt.Fprintln(out, "已是目标版本。")
			return 0, nil
		}
		if *check {
			fmt.Fprintln(out, "有可用更新；执行 opskit update 进行升级。")
			return 0, nil
		}
		name, err := selfupdate.AssetName(runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return 2, err
		}
		if runtime.GOOS == "windows" && !*downloadOnly {
			return 2, errors.New("Windows 请使用 opskit update --download-only，再退出程序并手动替换 opskit.exe")
		}
		executable, err := os.Executable()
		if err != nil {
			return 1, err
		}
		dir := *installDir
		if *downloadOnly {
			dir = *downloadDir
		} else if dir == "" {
			dir = filepath.Dir(executable)
		}
		dir, err = filepath.Abs(dir)
		if err != nil {
			return 1, err
		}
		if err = os.MkdirAll(dir, 0755); err != nil {
			return 1, err
		}
		lock, err := os.OpenFile(filepath.Join(dir, ".opskit-update.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return 1, fmt.Errorf("无法获取更新锁（检查目录写权限或已有更新进程）: %w", err)
		}
		defer os.Remove(lock.Name())
		if err = lock.Close(); err != nil {
			return 1, err
		}
		pattern := ".opskit-update-*"
		if runtime.GOOS == "windows" {
			pattern += ".exe"
		}
		stage, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return 1, err
		}
		defer os.Remove(stage.Name())
		source, _ = selfupdate.URL(*base, "releases/"+manifest.Version+"/"+name)
		err = selfupdate.DownloadAsset(ctx, client, source, manifest.Assets[name], stage)
		closeErr := stage.Close()
		if err != nil {
			return 1, err
		}
		if closeErr != nil {
			return 1, closeErr
		}
		if err = os.Chmod(stage.Name(), 0755); err != nil {
			return 1, err
		}
		verify := func(path string) error { return verifyUpdatedBinary(ctx, path, manifest.BuildVersion) }
		if err = verify(stage.Name()); err != nil {
			return 1, err
		}
		if *downloadOnly {
			dest := filepath.Join(dir, name)
			// Link a complete regular file without an overwrite window, then unlink staging.
			if err = os.Link(stage.Name(), dest); err != nil {
				return 1, err
			}
			fmt.Fprintln(out, "已下载并校验:", dest)
			return 0, nil
		}
		target := executable
		if *installDir != "" {
			target = filepath.Join(dir, "opskit")
		}
		if err = replaceUpdatedBinary(stage.Name(), target, verify); err != nil {
			return 1, err
		}
		fmt.Fprintln(out, "升级完成:", target, "（0755；配置保持不变）")
		return 0, finishToolPath(dir, "opskit", out)
	})
	return cmd, nil
}

func verifyUpdatedBinary(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil || strings.TrimSpace(string(output)) != version {
		return fmt.Errorf("更新程序版本验证失败（要求 %s）: %v", version, err)
	}
	return nil
}

func replaceUpdatedBinary(stage, target string, verify func(string) error) error {
	backup := ""
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("更新目标不是普通文件，请先将旧软链接迁移为实体文件: %s", target)
		}
		file, err := os.CreateTemp(filepath.Dir(target), "opskit.backup-*")
		if err != nil {
			return err
		}
		backup = file.Name()
		if err = file.Close(); err != nil {
			return err
		}
		// Preserve the old inode while atomic rename replaces the directory entry.
		if err = os.Remove(backup); err != nil {
			return err
		}
		if err = os.Link(target, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			os.Remove(backup)
		}
		return err
	}
	if err := verify(target); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				return fmt.Errorf("验证失败: %v；恢复失败: %v；原文件在 %s", err, restoreErr, backup)
			}
		} else if removeErr := os.Remove(target); removeErr != nil {
			return fmt.Errorf("验证失败: %v；清理失败: %v", err, removeErr)
		}
		return err
	}
	return nil
}
