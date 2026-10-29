package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"gopkg.in/yaml.v3"
)

type settings struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	Access    string `yaml:"access_key"`
	Secret    string `yaml:"secret_key"`
	Token     string `yaml:"session_token"`
	Region    string `yaml:"region"`
	PathStyle bool   `yaml:"path_style"`
}

func run(report map[string]any, path string) (success bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		report["error"] = "private configuration could not be read"
		return false
	}
	var file struct {
		S3 struct {
			MinIO settings `yaml:"minio"`
		} `yaml:"s3"`
	}
	if yaml.Unmarshal(raw, &file) != nil {
		report["error"] = "private configuration could not be decoded"
		return false
	}
	p := file.S3.MinIO
	if p.Access == "" || p.Secret == "" || p.Endpoint == "" || p.Bucket == "" {
		report["error"] = "missing required settings"
		return false
	}
	redact := func(value string) string {
		for _, secret := range []string{p.Access, p.Secret, p.Token} {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, "[REDACTED]")
			}
		}
		return value
	}
	fail := func(stage string, e error) bool {
		report["failed_stage"] = stage
		report["error"] = redact(e.Error())
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(p.Region), config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(p.Access, p.Secret, p.Token)), config.WithRetryMaxAttempts(2))
	if err != nil {
		return fail("client", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(p.Endpoint); o.UsePathStyle = p.PathStyle })
	report["endpoint"] = p.Endpoint
	report["bucket"] = p.Bucket
	cli := func(arguments ...string) ([]byte, string, error) {
		binary := os.Getenv("OPSKIT")
		if binary == "" {
			binary = "opskit"
		}
		command := exec.CommandContext(ctx, binary, arguments...)
		var diag bytes.Buffer
		command.Stderr = &diag
		output, e := command.Output()
		return output, redact(diag.String()), e
	}
	output, diag, err := cli("s3", "check", "--config", path, "--timeout", "20s")
	var check map[string]any
	if json.Unmarshal(output, &check) == nil {
		report["bucket_check"] = check
	}
	if err != nil {
		return fail("bucket_check", fmt.Errorf("%v: %s", err, diag))
	}
	id := make([]byte, 12)
	if _, err = rand.Read(id); err != nil {
		return fail("random_id", err)
	}
	prefix := "opskit-validation/" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(id) + "/"
	report["test_prefix"] = prefix
	owned := map[string]bool{}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 60*time.Second)
		defer stop()
		cleanup := map[string]any{}
		report["cleanup"] = cleanup
		cleanupFailure := func(e error) { cleanup["error"] = redact(e.Error()); cleanup["verified"] = false; success = false }
		uploads, e := client.ListMultipartUploads(cleanupCtx, &s3.ListMultipartUploadsInput{Bucket: aws.String(p.Bucket), Prefix: aws.String(prefix), MaxUploads: aws.Int32(100)})
		if e != nil {
			cleanupFailure(e)
			return
		}
		aborted := 0
		for _, upload := range uploads.Uploads {
			if !owned[aws.ToString(upload.Key)] {
				continue
			}
			_, e = client.AbortMultipartUpload(cleanupCtx, &s3.AbortMultipartUploadInput{Bucket: aws.String(p.Bucket), Key: upload.Key, UploadId: upload.UploadId})
			if e != nil {
				cleanupFailure(e)
				return
			}
			aborted++
		}
		cleanup["aborted_multipart_uploads"] = aborted
		versions, e := client.ListObjectVersions(cleanupCtx, &s3.ListObjectVersionsInput{Bucket: aws.String(p.Bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(100)})
		if e != nil {
			cleanupFailure(e)
			return
		}
		removed := 0
		remove := func(key, version *string) bool {
			if !owned[aws.ToString(key)] {
				return true
			}
			_, e := client.DeleteObject(cleanupCtx, &s3.DeleteObjectInput{Bucket: aws.String(p.Bucket), Key: key, VersionId: version})
			if e != nil {
				cleanupFailure(e)
				return false
			}
			removed++
			return true
		}
		for _, version := range versions.Versions {
			if !remove(version.Key, version.VersionId) {
				return
			}
		}
		for _, marker := range versions.DeleteMarkers {
			if !remove(marker.Key, marker.VersionId) {
				return
			}
		}
		remaining, e := client.ListObjectVersions(cleanupCtx, &s3.ListObjectVersionsInput{Bucket: aws.String(p.Bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(100)})
		if e != nil {
			cleanupFailure(e)
			return
		}
		pending, e := client.ListMultipartUploads(cleanupCtx, &s3.ListMultipartUploadsInput{Bucket: aws.String(p.Bucket), Prefix: aws.String(prefix), MaxUploads: aws.Int32(100)})
		if e != nil {
			cleanupFailure(e)
			return
		}
		cleanup["deleted_object_versions"] = removed
		cleanup["remaining_versions"] = len(remaining.Versions)
		cleanup["remaining_delete_markers"] = len(remaining.DeleteMarkers)
		cleanup["remaining_multipart_uploads"] = len(pending.Uploads)
		verified := len(remaining.Versions) == 0 && len(remaining.DeleteMarkers) == 0 && len(pending.Uploads) == 0 && !aws.ToBool(remaining.IsTruncated) && !aws.ToBool(pending.IsTruncated)
		cleanup["verified"] = verified
		if !verified {
			success = false
		}
	}()
	task, err := os.MkdirTemp("", "opskit-s3-retest-")
	if err != nil {
		return fail("temp_dir", err)
	}
	defer os.RemoveAll(task)
	tests := []map[string]any{}
	report["objects"] = tests
	download := func(key, checksum string, size int) error {
		object, e := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(p.Bucket), Key: aws.String(key)})
		if e != nil {
			return e
		}
		hash := sha256.New()
		length, readErr := io.Copy(hash, object.Body)
		closeErr := object.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if length != int64(size) || hex.EncodeToString(hash.Sum(nil)) != checksum {
			return errors.New("download size or SHA256 mismatch")
		}
		return nil
	}
	for _, size := range []int{64 * 1024, 11 * 1024 * 1024} {
		test := map[string]any{"bytes": size, "multipart_expected": size > 5*1024*1024}
		tests = append(tests, test)
		report["objects"] = tests
		key := prefix + fmt.Sprintf("payload-%d.bin", size)
		test["key"] = key
		_, headErr := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(p.Bucket), Key: aws.String(key)})
		var response *smithyhttp.ResponseError
		if !errors.As(headErr, &response) || response.HTTPStatusCode() != 404 {
			return fail("unique_key_preflight", fmt.Errorf("expected 404 for new object, got %v", headErr))
		}
		data := make([]byte, size)
		if _, err = rand.Read(data); err != nil {
			return fail("payload", err)
		}
		sum := sha256.Sum256(data)
		checksum := hex.EncodeToString(sum[:])
		test["sha256"] = checksum
		payloadPath := filepath.Join(task, "payload.bin")
		if err = os.WriteFile(payloadPath, data, 0600); err != nil {
			return fail("payload_file", err)
		}
		owned[key] = true
		output, diag, err = cli("s3", "upload", "--config", path, "--key", key, "--timeout", "90s", payloadPath)
		if err != nil {
			return fail("opskit_upload", fmt.Errorf("%v: %s", err, diag))
		}
		var manifest struct {
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
			Upload string `json:"upload_status"`
			Key    string `json:"remote_key"`
		}
		if err = json.Unmarshal(output, &manifest); err != nil {
			return fail("upload_manifest", err)
		}
		if manifest.Size != int64(size) || manifest.SHA256 != checksum || manifest.Upload != "verified-metadata" || manifest.Key != key {
			return fail("upload_manifest", errors.New("unexpected upload verification result"))
		}
		test["opskit_upload_verified"] = true
		if _, diag, err = cli("s3", "verify", "--config", path, "--key", key, "--timeout", "90s"); err != nil {
			return fail("opskit_verify", fmt.Errorf("%v: %s", err, diag))
		}
		listing, diag, listErr := cli("s3", "list", "--config", path, "--prefix", prefix, "--limit", "100")
		if listErr != nil || !strings.Contains(string(listing), key) {
			return fail("opskit_list", fmt.Errorf("%v: %s", listErr, diag))
		}
		destination := filepath.Join(task, fmt.Sprintf("download-%d.bin", size))
		if _, diag, err = cli("s3", "download", "--config", path, "--key", key, "--file", destination, "--timeout", "90s"); err != nil {
			return fail("opskit_download", fmt.Errorf("%v: %s", err, diag))
		}
		if content, err := os.ReadFile(destination); err != nil || !bytes.Equal(content, data) {
			return fail("opskit_download_bytes", errors.New("downloaded content differs"))
		}
		if _, _, err := cli("s3", "download", "--config", path, "--key", key, "--file", destination); err == nil {
			return fail("download_no_overwrite", errors.New("existing destination accepted"))
		}
		badDestination := filepath.Join(task, fmt.Sprintf("bad-%d.bin", size))
		if _, _, err := cli("s3", "download", "--config", path, "--key", key, "--file", badDestination, "--sha256", strings.Repeat("0", 64)); err == nil {
			return fail("bad_digest", errors.New("wrong digest accepted"))
		}
		if _, err := os.Stat(badDestination); !os.IsNotExist(err) {
			return fail("bad_digest_file", errors.New("unverified file published"))
		}
		test["opskit_list_verify_download"] = true
		test["local_overwrite_and_wrong_digest_rejected"] = true
		head, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(p.Bucket), Key: aws.String(key)})
		if err != nil {
			return fail("head_object", err)
		}
		if aws.ToInt64(head.ContentLength) != int64(size) || head.Metadata["sha256"] != checksum {
			return fail("head_object", errors.New("remote metadata mismatch"))
		}
		test["head_verified"] = true
		if err = download(key, checksum, size); err != nil {
			return fail("download", err)
		}
		test["download_sha256_verified"] = true
		data[0] ^= 0xff
		if err = os.WriteFile(payloadPath, data, 0600); err != nil {
			return fail("overwrite_payload", err)
		}
		_, diag, overwriteErr := cli("s3", "upload", "--config", path, "--key", key, "--timeout", "90s", payloadPath)
		if overwriteErr == nil || (!strings.Contains(diag, "PreconditionFailed") && !strings.Contains(diag, "412")) {
			return fail("overwrite_protection", fmt.Errorf("expected HTTP 412, got %v: %s", overwriteErr, diag))
		}
		test["overwrite_rejected_http_412"] = true
		if err = download(key, checksum, size); err != nil {
			return fail("original_after_rejected_overwrite", err)
		}
		test["original_preserved"] = true
	}
	if snapshot := os.Getenv("OPSKIT_E2E_SNAPSHOT"); snapshot != "" {
		key := prefix + "etcd/snapshot.db"
		owned[key] = true
		if _, diag, err := cli("s3", "upload", "--config", path, "--key", key, snapshot); err != nil {
			return fail("snapshot_upload", fmt.Errorf("%v: %s", err, diag))
		}
		destination := filepath.Join(task, "snapshot.db")
		if _, diag, err := cli("s3", "download", "--config", path, "--key", key, "--file", destination); err != nil {
			return fail("snapshot_download", fmt.Errorf("%v: %s", err, diag))
		}
		bin := os.Getenv("ETCD_BIN_DIR")
		dataDir := filepath.Join(task, "restored")
		restore := exec.CommandContext(ctx, filepath.Join(bin, "etcdutl"), "snapshot", "restore", destination, "--name", "s3-restored", "--data-dir", dataDir, "--initial-cluster", "s3-restored=http://127.0.0.1:32380", "--initial-advertise-peer-urls", "http://127.0.0.1:32380")
		if err := restore.Run(); err != nil {
			return fail("snapshot_restore", err)
		}
		server := exec.CommandContext(ctx, filepath.Join(bin, "etcd"), "--name", "s3-restored", "--data-dir", dataDir, "--listen-client-urls", "http://127.0.0.1:32379", "--advertise-client-urls", "http://127.0.0.1:32379", "--listen-peer-urls", "http://127.0.0.1:32380", "--initial-advertise-peer-urls", "http://127.0.0.1:32380", "--initial-cluster", "s3-restored=http://127.0.0.1:32380")
		if err := server.Start(); err != nil {
			return fail("restored_etcd_start", err)
		}
		defer func() { server.Process.Kill(); server.Wait() }()
		for attempt := 0; attempt < 30; attempt++ {
			if exec.CommandContext(ctx, filepath.Join(bin, "etcdctl"), "--endpoints=http://127.0.0.1:32379", "endpoint", "health").Run() == nil {
				break
			}
			time.Sleep(time.Second)
		}
		for key, expected := range map[string]string{"/opskit/recovery-test": "preserved-value", "/opskit/second-key": "second-value"} {
			value, err := exec.CommandContext(ctx, filepath.Join(bin, "etcdctl"), "--endpoints=http://127.0.0.1:32379", "get", key, "--print-value-only").Output()
			if err != nil || strings.TrimSpace(string(value)) != expected {
				return fail("restored_key", errors.New("restored key differs"))
			}
		}
		report["etcd_snapshot_s3_isolated_restore"] = true
	}
	return true
}

func main() {
	report := map[string]any{"started_at": time.Now().UTC().Format(time.RFC3339)}
	success := false
	if len(os.Args) == 2 {
		success = run(report, os.Args[1])
	} else {
		report["error"] = "private config path required"
	}
	report["success"] = success
	report["finished_at"] = time.Now().UTC().Format(time.RFC3339)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
	if !success {
		os.Exit(1)
	}
}
