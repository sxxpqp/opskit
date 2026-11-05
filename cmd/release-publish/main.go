// release-publish uploads immutable release assets before advancing stable.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/sxxpqp/opskit"
	"github.com/sxxpqp/opskit/internal/selfupdate"
	"gopkg.in/yaml.v3"
)

func main() {
	keygen := flag.Bool("generate-key", false, "本地生成 Ed25519 密钥；输出包含私钥，请勿放入日志或仓库")
	dist := flag.String("dist", "dist", "构建文件目录")
	validate := flag.Bool("validate-config", false, "仅校验发布配置和签名密钥，不连接 MinIO")
	flag.Parse()
	if *keygen {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fail(err)
		}
		if err = json.NewEncoder(os.Stdout).Encode(map[string]string{"UPDATE_PUBLIC_KEY": base64.StdEncoding.EncodeToString(pub), "UPDATE_SIGNING_PRIVATE_KEY": base64.StdEncoding.EncodeToString(key)}); err != nil {
			fail(err)
		}
		return
	}
	if *validate {
		if _, err := validateConfiguration(); err != nil {
			fail(err)
		}
		fmt.Println("发布配置校验通过。")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := publish(ctx, *dist); err != nil {
		fail(err)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func publish(ctx context.Context, dist string) error {
	key, err := validateConfiguration()
	if err != nil {
		return err
	}
	public := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	var defaults struct {
		Update selfupdate.Settings `yaml:"update"`
	}
	if err = yaml.Unmarshal(opskit.ConfigTemplate, &defaults); err != nil {
		return err
	}
	if _, err = selfupdate.URL(defaults.Update.DownloadBase, "channels/stable.json"); err != nil {
		return err
	}
	manifest, files, err := releaseFiles(dist, os.Getenv("RELEASE_TAG"), os.Getenv("BUILD_VERSION"))
	if err != nil {
		return err
	}
	signed, err := selfupdate.Sign(manifest, key)
	if err != nil {
		return err
	}
	region := os.Getenv("MINIO_REGION")
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY"), "")))
	if err != nil {
		return err
	}
	pathStyle, err := minioPathStyle()
	if err != nil {
		return err
	}
	client := newMinioClient(cfg, os.Getenv("MINIO_ENDPOINT"), pathStyle)
	bucket := os.Getenv("MINIO_BUCKET")
	httpClient := selfupdate.NewHTTPClient()
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dist, name))
		if err != nil {
			return err
		}
		object := "releases/" + manifest.Version + "/" + name
		if err = putImmutable(ctx, client, bucket, object, data, "application/octet-stream"); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		source, _ := selfupdate.URL(defaults.Update.DownloadBase, object)
		if err = selfupdate.DownloadAsset(ctx, httpClient, source, selfupdate.Asset{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}, io.Discard); err != nil {
			return fmt.Errorf("公开下载回读失败 (%s): %w", name, err)
		}
		fmt.Println("已上传并验证:", object)
	}
	object := "releases/" + manifest.Version + "/manifest.json"
	if err = putImmutable(ctx, client, bucket, object, signed, "application/json"); err != nil {
		return err
	}
	source, _ := selfupdate.URL(defaults.Update.DownloadBase, object)
	var downloaded bytes.Buffer
	if err = selfupdate.Fetch(ctx, httpClient, source, 1<<20, &downloaded); err != nil {
		return err
	}
	if !bytes.Equal(downloaded.Bytes(), signed) {
		return errors.New("公开版本清单与上传内容不一致")
	}
	// CI concurrency serializes publishers; conditional writes also protect the channel.
	previous, etag, err := getObject(ctx, client, bucket, "channels/stable.json")
	if err != nil {
		return err
	}
	if previous != nil {
		old, err := selfupdate.Verify(previous, public)
		if err != nil {
			return fmt.Errorf("已有 stable 签名无效，未覆盖: %w", err)
		}
		comparison, _ := selfupdate.Compare(manifest.Version, old.Version)
		if comparison <= 0 {
			fmt.Println("stable 已指向相同或更新版本，保持不变。")
			return nil
		}
	}
	input := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String("channels/stable.json"), Body: bytes.NewReader(signed), ContentType: aws.String("application/json"), CacheControl: aws.String("no-cache, max-age=0, must-revalidate")}
	if previous == nil {
		input.IfNoneMatch = aws.String("*")
	} else {
		input.IfMatch = aws.String(etag)
	}
	if _, err = client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("更新 stable 失败，请重新运行发布作业: %w", err)
	}
	source, _ = selfupdate.URL(defaults.Update.DownloadBase, "channels/stable.json")
	downloaded.Reset()
	if err = selfupdate.Fetch(ctx, httpClient, source, 1<<20, &downloaded); err != nil {
		return err
	}
	if !bytes.Equal(downloaded.Bytes(), signed) {
		return errors.New("stable 公开回读未生效，检查反向代理缓存")
	}
	fmt.Println("stable 已发布:", manifest.Version)
	return nil
}

func releaseFiles(dist, version, build string) (selfupdate.Manifest, []string, error) {
	m := selfupdate.Manifest{SchemaVersion: 1, Version: version, BuildVersion: build, Assets: map[string]selfupdate.Asset{}}
	var names []string
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := selfupdate.AssetName(platform, arch)
			names = append(names, name)
		}
	}
	names = append(names, "config.example.yaml", "checksums.txt")
	checksums, err := os.ReadFile(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		return m, nil, err
	}
	expected := map[string]string{}
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return m, nil, errors.New("checksums.txt 格式无效")
		}
		expected[strings.TrimPrefix(fields[1], "*")] = fields[0]
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dist, name))
		if err != nil {
			return m, nil, err
		}
		sum := sha256.Sum256(data)
		checksum := hex.EncodeToString(sum[:])
		if name != "checksums.txt" && expected[name] != checksum {
			return m, nil, fmt.Errorf("构建产物摘要不匹配: %s", name)
		}
		m.Assets[name] = selfupdate.Asset{SHA256: checksum, Size: int64(len(data))}
	}
	return m, names, m.Validate()
}

func getObject(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, string, error) {
	result, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && (api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "NotFound") {
			return nil, "", nil
		}
		return nil, "", err
	}
	defer result.Body.Close()
	data, err := io.ReadAll(io.LimitReader(result.Body, 1<<20+1))
	if len(data) > 1<<20 {
		return nil, "", errors.New("已有版本清单过大")
	}
	return data, aws.ToString(result.ETag), err
}

func putImmutable(ctx context.Context, client *s3.Client, bucket, key string, data []byte, contentType string) error {
	_, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(data), IfNoneMatch: aws.String("*"), ContentType: aws.String(contentType), CacheControl: aws.String("public, max-age=31536000, immutable")})
	if err == nil {
		return nil
	}
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "PreconditionFailed" {
		return err
	}
	// A retried job may reuse an identical immutable object, but never change it.
	result, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return err
	}
	defer result.Body.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(result.Body, int64(len(data))+1))
	if err != nil {
		return err
	}
	expected := sha256.Sum256(data)
	if n != int64(len(data)) || !bytes.Equal(hash.Sum(nil), expected[:]) {
		return fmt.Errorf("固定版本对象已存在且内容不同，拒绝覆盖: %s", key)
	}
	return nil
}

func validateConfiguration() (ed25519.PrivateKey, error) {
	if _, err := minioPathStyle(); err != nil {
		return nil, err
	}
	for _, name := range []string{"MINIO_ENDPOINT", "MINIO_BUCKET", "MINIO_ACCESS_KEY", "MINIO_SECRET_KEY", "UPDATE_SIGNING_PRIVATE_KEY", "UPDATE_PUBLIC_KEY"} {
		if os.Getenv(name) == "" {
			return nil, fmt.Errorf("缺少 CI 配置: %s", name)
		}
	}
	u, err := url.Parse(os.Getenv("MINIO_ENDPOINT"))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("MINIO_ENDPOINT 必须为 HTTPS S3 API 根地址，不含桶或凭据")
	}
	private, err := base64.StdEncoding.DecodeString(os.Getenv("UPDATE_SIGNING_PRIVATE_KEY"))
	if err != nil || len(private) != ed25519.PrivateKeySize {
		return nil, errors.New("UPDATE_SIGNING_PRIVATE_KEY 必须为 base64 Ed25519 私钥（64 字节）")
	}
	key := ed25519.PrivateKey(private)
	// Reject malformed 64-byte keys, not just mismatched embedded public halves.
	if !bytes.Equal(ed25519.NewKeyFromSeed(private[:ed25519.SeedSize]), private) {
		return nil, errors.New("签名私钥内容无效")
	}
	public := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	if public != os.Getenv("UPDATE_PUBLIC_KEY") {
		return nil, errors.New("签名私钥与 UPDATE_PUBLIC_KEY 不匹配")
	}

	return key, nil
}

func minioPathStyle() (bool, error) {
	value := os.Getenv("MINIO_PATH_STYLE")
	if value == "" {
		return false, nil
	}
	style, err := strconv.ParseBool(value)
	if err != nil {
		return false, errors.New("MINIO_PATH_STYLE 必须为 true 或 false")
	}
	return style, nil
}

func newMinioClient(cfg aws.Config, endpoint string, pathStyle bool) *s3.Client {
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = pathStyle
	})
}
