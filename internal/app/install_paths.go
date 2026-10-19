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

// Retain downloads for inspection; always fetch again rather than trust an unchecked cache.
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
	e = download(ctx, source, io.MultiWriter(dst, f))
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
			target = f.Name() + "-" + name
		}
	}
	return os.Rename(f.Name(), target)
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
		return finishToolPath(dir, tool, out)
	}
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	if info, e := os.Lstat(dest); e == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			if runtime.GOOS == "windows" && info.Mode().IsRegular() {
				a, ea := os.ReadFile(dest)
				b, eb := os.ReadFile(target)
				if ea == nil && eb == nil && bytes.Equal(a, b) {
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
	if runtime.GOOS == "windows" {
		b, e := os.ReadFile(target)
		if e != nil {
			return e
		}
		if e = writeRuntimeNew(dest, b, 0755); e != nil {
			return e
		}
	} else {
		f, e := os.CreateTemp(dir, ".opskit-"+tool+"-*")
		if e != nil {
			return e
		}
		temp := f.Name()
		if e = f.Close(); e != nil {
			return e
		}
		if e = os.Remove(temp); e != nil {
			return e
		}
		if e = os.Symlink(target, temp); e != nil {
			return e
		}
		defer os.Remove(temp)
		if e = os.Rename(temp, dest); e != nil {
			return e
		}
	}
	fmt.Fprintln(out, "命令入口:", dest)
	return finishToolPath(dir, tool, out)
}
