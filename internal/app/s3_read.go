package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
)

func newS3ReadCommand(parent context.Context, action string, out io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: action}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置")
	var store storage
	store.flags(f)
	prefix := f.String("prefix", "", "列举对象的前缀")
	limit := f.Int32("limit", 1000, "列举数量上限，1～10000")
	destination := f.String("file", "", "下载目标文件，拒绝覆盖；失败不保留未校验文件")
	expected := f.String("sha256", "", "预期 SHA-256；优先于对象的 sha256 元数据")
	timeout := f.Duration("timeout", 10*time.Minute, "任务总超时")
	bindCommand(cmd, func() (int, error) {
		if err := loadConfig(*config, explicitFlag(f, "config"), f, &store); err != nil {
			return 2, err
		}
		if f.NArg() != 0 || *timeout <= 0 || store.bucket == "" || *limit < 1 || *limit > 10000 {
			return 2, errors.New("需要 bucket、正 timeout、有效 limit，且不能有位置参数")
		}
		if (store.accessKey == "") != (store.secretKey == "") || (store.sessionToken != "" && store.accessKey == "") {
			return 2, errors.New("S3 access_key 和 secret_key 必须成对填写；session_token 需要两者")
		}
		if (store.provider == "minio" || store.provider == "custom") && store.endpoint == "" {
			return 2, errors.New("所选 S3 厂家需要 endpoint")
		}
		if action != "list" && store.key == "" {
			return 2, errors.New("需要 --key 指定完整对象名称")
		}
		if action == "download" && *destination == "" {
			return 2, errors.New("需要 --file 指定下载目标")
		}
		if *expected != "" && !validSHA256(*expected) {
			return 2, errors.New("sha256 必须是 64 位十六进制摘要")
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		client, err := store.client(ctx)
		if err != nil {
			return taskError(ctx, err)
		}
		if action == "list" {
			return taskError(ctx, listS3Objects(ctx, client, store.bucket, *prefix, *limit, out))
		}
		return taskError(ctx, readS3Object(ctx, client, store, action, *destination, *expected, out))
	})
	return cmd, nil
}

func validSHA256(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

func listS3Objects(ctx context.Context, client *s3.Client, bucket, prefix string, limit int32, out io.Writer) error {
	type object struct {
		Key  string `json:"key"`
		Size int64  `json:"size"`
	}
	result := struct {
		SchemaVersion int      `json:"schema_version"`
		Objects       []object `json:"objects"`
		Truncated     bool     `json:"truncated"`
	}{SchemaVersion: 1, Objects: []object{}}
	pager := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(min(limit, 1000))})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, item := range page.Contents {
			if int32(len(result.Objects)) == limit {
				result.Truncated = true
				break
			}
			result.Objects = append(result.Objects, object{aws.ToString(item.Key), aws.ToInt64(item.Size)})
		}
		if int32(len(result.Objects)) >= limit {
			result.Truncated = result.Truncated || pager.HasMorePages()
			break
		}
	}
	return json.NewEncoder(out).Encode(result)
}

func readS3Object(ctx context.Context, client *s3.Client, store storage, action, destination, expected string, out io.Writer) error {
	response, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(store.key)})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if expected == "" {
		expected = response.Metadata["sha256"]
	}
	if !validSHA256(expected) {
		return errors.New("对象缺少有效 sha256 元数据；请用 --sha256 提供可信摘要")
	}
	hash := sha256.New()
	var temporary *os.File
	var writer io.Writer = hash
	if action == "download" {
		if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("目标已存在或不可访问，拒绝覆盖: %s", destination)
		}
		temporary, err = os.CreateTemp(filepath.Dir(destination), ".opskit-download-*")
		if err != nil {
			return err
		}
		defer os.Remove(temporary.Name())
		defer temporary.Close()
		writer = io.MultiWriter(temporary, hash)
	}
	size, err := io.Copy(writer, response.Body)
	if err != nil {
		return err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(digest, expected) || (response.ContentLength != nil && size != *response.ContentLength) {
		return errors.New("对象内容 SHA-256 或长度不匹配")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if temporary != nil {
		if err := temporary.Sync(); err != nil {
			return err
		}
		if err := temporary.Close(); err != nil {
			return err
		}
		// Linking publishes the verified file atomically without replacing an existing path.
		if err := os.Link(temporary.Name(), destination); err != nil {
			return fmt.Errorf("保存校验后的文件（拒绝覆盖）: %w", err)
		}
	}
	return json.NewEncoder(out).Encode(struct {
		SchemaVersion int    `json:"schema_version"`
		Key           string `json:"key"`
		Size          int64  `json:"size"`
		SHA256        string `json:"sha256"`
		Status        string `json:"status"`
	}{1, store.key, size, digest, "verified-content"})
}
