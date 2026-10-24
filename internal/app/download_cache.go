package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// The index is shared across component commands; each URL identifies its own bytes.
func retainedCacheIndex(root, source string) string {
	key := sha256.Sum256([]byte(source))
	return filepath.Join(root, ".cache", hex.EncodeToString(key[:])+".json")
}

func reuseRetainedDownload(ctx context.Context, root, source string, dst io.Writer) (bool, error) {
	if err := ctx.Err(); err != nil {
		return true, err
	}
	raw, err := os.ReadFile(retainedCacheIndex(root, source))
	var entry clusterCacheEntry
	if err != nil || json.Unmarshal(raw, &entry) != nil || !filepath.IsLocal(entry.File) {
		return false, nil
	}
	f, err := os.Open(filepath.Join(root, entry.File))
	if err != nil {
		return false, nil
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return false, nil
	}
	if err = ctx.Err(); err != nil {
		return true, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	_, err = io.Copy(dst, f)
	return true, err
}

func indexRetainedDownload(root, source, target, digest string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(clusterCacheEntry{File: relative, SHA256: digest})
	if err != nil {
		return err
	}
	index := retainedCacheIndex(root, source)
	if err = os.MkdirAll(filepath.Dir(index), 0755); err != nil {
		return err
	}
	// A missing or interrupted index is a cache miss, never a usable download.
	return os.WriteFile(index, raw, 0600)
}
