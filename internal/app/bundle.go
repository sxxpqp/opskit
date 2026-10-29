package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type bundleEntry struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type bundleManifest struct {
	SchemaVersion int           `json:"schema_version"`
	Files         []bundleEntry `json:"files"`
}

func newBundleCommand(ctx context.Context, action string, out io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: action, Short: "导出、导入或校验组件与镜像离线归档"}
	f := cmd.Flags()
	directory := f.String("directory", "", "export: k8s download 完成目录；import: 尚不存在的目标目录；verify: 已导入目录")
	archive := f.String("file", "", "tar.gz 归档文件，导出拒绝覆盖")
	images := f.StringSlice("image-archive", nil, "导出附带的已有容器镜像 tar，可重复")
	packages := f.StringSlice("package-file", nil, "导出附带的已有系统软件包，可重复")
	maxBytes := f.Int64("max-bytes", 50<<30, "导入最大未压缩文件总字节数")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *directory == "" || *maxBytes <= 0 || (action != "verify" && *archive == "") {
			return 2, errors.New("需要 directory 和 file（verify 除外），max-bytes 必须为正")
		}
		var err error
		switch action {
		case "export":
			err = exportBundle(ctx, *directory, *archive, *images, *packages)
		case "import":
			err = importBundle(ctx, *archive, *directory, *maxBytes)
		case "verify":
			_, err = verifyBundle(ctx, *directory)
		}
		if err != nil {
			return taskError(ctx, err)
		}
		return 0, json.NewEncoder(out).Encode(struct {
			SchemaVersion int    `json:"schema_version"`
			Action        string `json:"action"`
			Status        string `json:"status"`
		}{1, action, "verified"})
	})
	return cmd, nil
}

func localBundlePath(root, relative string) (string, error) {
	if strings.Contains(relative, "\\") || strings.Contains(relative, ":") || !filepath.IsLocal(relative) || relative == "." {
		return "", errors.New("归档路径不安全")
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil || resolved != abs {
		return "", errors.New("归档文件不能经过符号链接")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("归档只接受普通文件")
	}
	return path, nil
}

func exportBundle(ctx context.Context, directory, destination string, images, packages []string) error {
	root, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(root, "downloads.json"))
	if err != nil {
		return fmt.Errorf("需要 k8s download 完成目录: %w", err)
	}
	var records []componentDownloadRecord
	if err := json.Unmarshal(raw, &records); err != nil || len(records) == 0 {
		return errors.New("无效的 downloads.json")
	}
	allowed := map[string]bool{"downloads.json": true}
	for _, record := range records {
		allowed[filepath.ToSlash(record.File)] = true
		dir := filepath.ToSlash(filepath.Dir(record.File))
		for _, checksum := range []string{filepath.Base(record.File) + ".sha256", filepath.Base(record.File) + ".sha256sum", "runc.sha256sum", "SHA256SUMS", "checksums.txt"} {
			allowed[filepath.ToSlash(filepath.Join(dir, checksum))] = true
		}
		path, err := localBundlePath(root, record.File)
		if err != nil {
			return err
		}
		m, err := inspect(path)
		if err != nil || m.SHA256 != record.SHA256 {
			return errors.New("组件摘要不匹配")
		}
	}
	files := map[string]string{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := localBundlePath(root, filepath.ToSlash(rel)); err != nil {
			return err
		}
		if !allowed[filepath.ToSlash(rel)] {
			return nil
		}
		files["components/"+filepath.ToSlash(rel)] = path
		return nil
	})
	if err != nil {
		return err
	}
	for group, paths := range map[string][]string{"images": images, "packages": packages} {
		for i, path := range paths {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("附带的镜像/软件包必须是普通文件")
			}
			files[fmt.Sprintf("%s/%03d-%s", group, i, filepath.Base(path))] = path
		}
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return errors.New("导出目标存在或不可访问")
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".bundle-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	manifest := bundleManifest{SchemaVersion: 1}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := inspect(files[name])
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: m.Size, Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		input, err := os.Open(files[name])
		if err != nil {
			return err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(tw, hash), input)
		input.Close()
		if copyErr != nil {
			return copyErr
		}
		if n != m.Size || hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
			return errors.New("导出期间源文件发生变化")
		}
		manifest.Files = append(manifest.Files, bundleEntry{name, m.SHA256, m.Size})
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "bundle.json", Size: int64(len(raw)), Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(raw); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Link(file.Name(), destination)
}

func importBundle(ctx context.Context, archive, destination string, maxBytes int64) error {
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return errors.New("导入目标必须尚不存在")
	}
	work, err := os.MkdirTemp(filepath.Dir(destination), ".bundle-import-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	input, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer input.Close()
	gz, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > 100000 || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxBytes-total || strings.ContainsAny(header.Name, "\\:") || !filepath.IsLocal(header.Name) {
			return errors.New("归档包含危险路径、特殊文件或超过大小/文件数量限制")
		}
		total += header.Size
		path := filepath.Join(work, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
	}
	// Consume gzip trailer so CRC/truncation errors cannot be hidden by the tar end marker.
	if n, err := io.Copy(io.Discard, io.LimitReader(gz, (1<<20)+1)); err != nil {
		return err
	} else if n > 1<<20 {
		return errors.New("归档尾部数据超过限制")
	}
	if _, err := verifyBundle(ctx, work); err != nil {
		return err
	}
	return os.Rename(work, destination)
}

func verifyBundle(ctx context.Context, directory string) (bundleManifest, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return bundleManifest{}, err
	}
	manifestPath, err := localBundlePath(root, "bundle.json")
	if err != nil {
		return bundleManifest{}, err
	}
	info, err := os.Stat(manifestPath)
	if err != nil || info.Size() > 32<<20 {
		return bundleManifest{}, errors.New("归档清单超过 32 MiB 或不可读取")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return bundleManifest{}, err
	}
	var manifest bundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.SchemaVersion != 1 || len(manifest.Files) == 0 {
		return manifest, errors.New("无效 bundle.json")
	}
	seen := map[string]bool{"bundle.json": true}
	for _, entry := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return manifest, err
		}
		if seen[entry.File] {
			return manifest, errors.New("重复的归档清单路径")
		}
		seen[entry.File] = true
		path, err := localBundlePath(root, entry.File)
		if err != nil {
			return manifest, err
		}
		actual, err := inspect(path)
		if err != nil || actual.Size != entry.Size || actual.SHA256 != entry.SHA256 {
			return manifest, fmt.Errorf("归档文件校验失败: %s", entry.File)
		}
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !seen[filepath.ToSlash(rel)] {
			return errors.New("归档有未登记文件")
		}
		return nil
	})
	return manifest, err
}
