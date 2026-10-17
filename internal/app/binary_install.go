package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type binaryInstallOptions struct {
	kindOptions
	tool, binary, source, checksumSuffix string
	verify                               func(context.Context, string, string) error
}

func installVerifiedBinary(ctx context.Context, o binaryInstallOptions, out io.Writer) error {
	if !versionPattern.MatchString(o.version) {
		return errors.New("版本须为 vX.Y.Z")
	}
	u, err := url.Parse(o.source)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New("download-base 必须是无用户凭据的 HTTP(S) 地址")
	}
	binary := o.binary
	dir, err := filepath.Abs(o.binDir)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, binary)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("安装目标不是普通文件: %s", target)
		}
		if o.verify(ctx, target, o.version) == nil {
			fmt.Fprintf(out, "%s 已安装: %s (%s)\n", o.tool, target, o.version)
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(dir, "."+o.tool+"-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	source := o.source
	fmt.Fprintf(out, "下载 %s %s (%s/%s)\n", o.tool, o.version, o.platform, o.arch)
	var checksum strings.Builder
	if err := downloadRetained(ctx, source+o.checksumSuffix, &checksum, o.downloadDir, o.tool, o.version); err != nil {
		return err
	}
	fields := strings.Fields(checksum.String())
	if len(fields) == 0 {
		return errors.New("SHA256 文件为空")
	}
	expected, err := hex.DecodeString(fields[0])
	if err != nil || len(expected) != sha256.Size {
		return errors.New("SHA256 文件格式错误")
	}
	file := filepath.Join(work, binary)
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	hash := sha256.New()
	downloadErr := downloadRetained(ctx, source, io.MultiWriter(f, hash), o.downloadDir, o.tool, o.version)
	closeErr := f.Close()
	if downloadErr != nil {
		return downloadErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != hex.EncodeToString(expected) {
		return errors.New("SHA256 校验失败，未安装，原文件保持不变")
	}
	if err := o.verify(ctx, file, o.version); err != nil {
		return err
	}
	// 验证成功后再替换；备份保留在同一目录，失败时恢复。
	backup := ""
	if _, err := os.Stat(target); err == nil {
		b, err := os.CreateTemp(dir, o.tool+".backup-*")
		if err != nil {
			return err
		}
		backup = b.Name()
		if err := b.Close(); err != nil {
			return err
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
		if err := os.Rename(target, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(file, target); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				return fmt.Errorf("安装失败: %v；恢复失败: %v；原文件在 %s", err, restoreErr, backup)
			}
		}
		return err
	}
	fmt.Fprintf(out, "安装完成: %s (%s)\n", target, o.version)
	if backup != "" {
		fmt.Fprintln(out, "原版本备份:", backup)
	}
	return nil
}
