package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Retain complete downloads and verify their digest before reusing them.
func downloadRetained(ctx context.Context, source string, dst io.Writer, root, tool, version string) error {
	if cached, err := cachedClusterDownload(ctx, source, dst); cached {
		return err
	}
	if root == "" {
		return download(ctx, source, dst)
	}
	u, e := url.Parse(source)
	if e != nil {
		return e
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		name = "install.sh"
	}
	// Release metadata and installer scripts may change at the same URL.
	cacheable := !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".sh")
	if cacheable {
		if reused, err := reuseRetainedDownload(ctx, root, source, dst); reused {
			return err
		}
	}
	sum := sha256.Sum256([]byte(source))
	dir := filepath.Join(root, tool, version, fmt.Sprintf("%x", sum[:6]))
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".download-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	hash := sha256.New()
	e = download(ctx, source, io.MultiWriter(dst, f, hash))
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	target := filepath.Join(dir, name)
	// Windows rename cannot replace an existing regular file. Preserve the prior download.
	if runtime.GOOS == "windows" {
		if _, e = os.Lstat(target); e == nil {
			target = filepath.Join(dir, strings.TrimPrefix(filepath.Base(f.Name()), ".download-")+"-"+name)
		}
	}
	if e = os.Rename(f.Name(), target); e != nil {
		return e
	}
	if cacheable {
		return indexRetainedDownload(root, source, target, fmt.Sprintf("%x", hash.Sum(nil)))
	}
	return nil
}

func defaultDownloadDir() (string, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(home, ".opskit", "downloads"), nil
}
func defaultKindLinkDir() (string, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	preferred := kindLinkCandidates(runtime.GOOS, home, os.Getenv("LOCALAPPDATA"), os.Geteuid() == 0)
	return chooseKindLinkDir(os.Getenv("PATH"), preferred)
}

// Prefer conventional directories already on PATH, then other existing PATH directories.
// Empty/relative entries depend on the working directory and are not stable install locations.
func chooseKindLinkDir(pathList string, preferred []string) (string, error) {
	candidates := []string{}
	for _, dir := range preferred {
		if pathIncludes(pathList, dir) {
			candidates = append(candidates, dir)
		}
	}
	candidates = append(candidates, filepath.SplitList(pathList)...)
	seen := map[string]bool{}
	for _, dir := range candidates {
		if !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		key := dir
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		info, e := os.Stat(dir)
		if e != nil || !info.IsDir() {
			continue
		}
		f, e := os.CreateTemp(dir, ".opskit-path-check-*")
		if e != nil {
			continue
		}
		name := f.Name()
		ce := f.Close()
		re := os.Remove(name)
		if ce != nil {
			return "", ce
		}
		if re != nil {
			return "", re
		}
		return dir, nil
	}
	return preferred[len(preferred)-1], nil
}

func exposeKind(target, dir string, out io.Writer) error { return exposeTool(target, dir, "kind", out) }
func exposeTool(target, dir, tool string, out io.Writer) error {
	target, e := filepath.Abs(target)
	if e != nil {
		return e
	}
	dir, e = filepath.Abs(dir)
	if e != nil {
		return e
	}
	dest := filepath.Join(dir, filepath.Base(target))
	if dest == target {
		if e = os.Chmod(dest, 0777); e != nil {
			return e
		}
		return finishToolPath(dir, tool, out)
	}
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	if info, e := os.Lstat(dest); e == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			if info.Mode().IsRegular() {
				a, ea := os.ReadFile(dest)
				b, eb := os.ReadFile(target)
				if ea == nil && eb == nil && bytes.Equal(a, b) {
					if e = os.Chmod(dest, 0777); e != nil {
						return e
					}
					if e = os.Remove(target); e != nil {
						return e
					}
					fmt.Fprintln(out, "命令入口:", dest)
					return finishToolPath(dir, tool, out)
				}
			}

			return fmt.Errorf("%s 已安装到 %s，但 PATH 入口 %s 已存在，未覆盖", tool, target, dest)
		}
		old, e := os.Readlink(dest)
		if e != nil {
			return e
		}
		if !filepath.IsAbs(old) {
			old = filepath.Join(dir, old)
		}
		if old != target {
			home, e := os.UserHomeDir()
			if e != nil {
				return e
			}
			managed := filepath.Join(home, ".local", "share", "opskit", tool) + string(os.PathSeparator)
			if !strings.HasPrefix(filepath.Clean(old), managed) {
				return fmt.Errorf("PATH 入口指向其他安装 %s，未覆盖", old)
			}
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	// Stage on the destination filesystem before replacing the managed link.
	b, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".opskit-"+tool+"-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Chmod(temp, 0777); err != nil {
		return err
	}
	if err = os.Rename(temp, dest); err != nil {
		return err
	}
	if err = os.Remove(target); err != nil {
		return err
	}

	fmt.Fprintln(out, "命令入口:", dest)
	return finishToolPath(dir, tool, out)
}
