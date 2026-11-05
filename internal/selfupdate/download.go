package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NewHTTPClient refuses redirects that would downgrade HTTPS.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 10 {
			return fmt.Errorf("更新下载拒绝不安全或过多重定向")
		}
		return nil
	}}
}

func Fetch(ctx context.Context, client *http.Client, source string, limit int64, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("更新下载 HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("更新文件超过大小限制")
	}
	return nil
}

func DownloadAsset(ctx context.Context, client *http.Client, source string, asset Asset, dst io.Writer) error {
	hash := sha256.New()
	var size countingWriter
	size.Writer = io.MultiWriter(dst, hash)
	if err := Fetch(ctx, client, source, asset.Size, &size); err != nil {
		return err
	}
	if size.n != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return fmt.Errorf("更新文件大小或 SHA-256 校验失败")
	}
	return nil
}

type countingWriter struct {
	io.Writer
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.n += int64(n)
	return n, err
}
