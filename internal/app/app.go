package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
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
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	File          string    `json:"file"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	Created       time.Time `json:"created_at"`
	Upload        string    `json:"upload_status"`
	Key           string    `json:"remote_key,omitempty"`
}

type storage struct {
	bucket, key, endpoint, region                string
	provider, accessKey, secretKey, sessionToken string
	pathStyle                                    bool
}

func (s *storage) flags(f *flag.FlagSet) {
	f.StringVar(&s.bucket, "bucket", "", "S3 bucket; enables upload")
	f.StringVar(&s.key, "key", "", "S3 object key (required for standalone upload)")
	f.StringVar(&s.endpoint, "s3-endpoint", "", "S3-compatible endpoint URL")
	f.StringVar(&s.region, "region", "us-east-1", "S3 region")
	f.BoolVar(&s.pathStyle, "path-style", false, "use path-style addressing")
}

func Run(ctx context.Context, args []string, out, diag io.Writer, version string) (int, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, "opskit version | config init | kind install --version VERSION [flags] | etcd backup/check [flags] | s3 upload [flags] FILE | s3 check [flags]\nDefault config: ./opskit.yaml; override with COMMAND --config FILE.\nUse COMMAND --help for flags. Credentials: AWS SDK default chain; etcd: ETCDCTL_* environment.")
		return 0, nil
	}
	if args[0] == "version" {
		fmt.Fprintln(out, version)
		return 0, nil
	}
	if len(args) < 2 {
		return 2, errors.New("expected etcd backup or s3 upload")
	}
	command := strings.Join(args[:2], " ")
	if command == "kind install" {
		if err := runKindInstall(ctx, args, out, diag); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if command == "config init" {
		return initConfig(args[2:], out, diag)
	}
	if command != "etcd backup" && command != "s3 upload" && command != "etcd check" && command != "s3 check" {
		return 2, fmt.Errorf("unknown command %q", command)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(diag)
	configPath := f.String("config", "opskit.yaml", "configuration file (default: current directory)")
	var s storage
	s.flags(f)
	defaultTimeout := time.Hour
	if strings.HasSuffix(command, "check") {
		defaultTimeout = 30 * time.Second
	}
	timeout := f.Duration("timeout", defaultTimeout, "total task timeout")
	var probeTimeout *time.Duration
	if command == "etcd check" {
		probeTimeout = f.Duration("probe-timeout", 5*time.Second, "per-member health and status timeout")
	}
	var binary, endpoint, dir, ca, cert, key *string
	if strings.HasPrefix(command, "etcd ") {
		binary = f.String("etcdctl", "etcdctl", "etcdctl executable")
		endpoint = f.String("endpoint", "", "single etcd endpoint; otherwise ETCDCTL_ENDPOINTS")
		dir = f.String("directory", "data/backups/etcd", "local backup directory")
		ca = f.String("cacert", "", "trusted etcd CA file")
		cert = f.String("cert", "", "etcd client certificate")
		key = f.String("key-file", "", "etcd client key")
	}
	if err := f.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	requiredConfig := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "config" {
			requiredConfig = true
		}
	})
	if err := loadConfig(*configPath, requiredConfig, f, &s); err != nil {
		return 2, err
	}
	if *timeout <= 0 {
		return 2, errors.New("timeout must be positive")
	}
	if command != "etcd check" && (s.key != "" || s.endpoint != "") && s.bucket == "" {
		return 2, errors.New("S3 options require --bucket")
	}
	if command != "etcd check" && s.bucket != "" {
		if (s.provider == "minio" || s.provider == "custom") && s.endpoint == "" {
			return 2, fmt.Errorf("s3.%s.endpoint is required when bucket is set", s.provider)
		}
		if (s.accessKey == "") != (s.secretKey == "") || (s.sessionToken != "" && s.accessKey == "") {
			return 2, errors.New("S3 access_key and secret_key must be provided together; session_token requires both")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if command == "s3 check" {
		if f.NArg() != 0 || s.bucket == "" {
			return 2, errors.New("s3 check requires a bucket and no positional arguments")
		}
		return s.check(ctx, out)
	}
	if command == "s3 upload" {
		if f.NArg() != 1 || s.bucket == "" || s.key == "" {
			return 2, errors.New("usage: opskit s3 upload --bucket BUCKET --key KEY [flags] FILE")
		}
		m, err := inspect(f.Arg(0))
		if err != nil {
			return 1, err
		}
		if err = s.upload(ctx, f.Arg(0), m.SHA256); err != nil {
			return 1, err
		}
		m.Upload = "verified-metadata"
		m.Key = s.key
		return 0, json.NewEncoder(out).Encode(m)
	}
	if f.NArg() != 0 {
		return 2, errors.New("unexpected positional arguments")
	}
	if (*cert == "") != (*key == "") {
		return 2, errors.New("--cert and --key-file must be supplied together")
	}
	if _, err := exec.LookPath(*binary); err != nil {
		return 3, fmt.Errorf("etcdctl dependency: %w", err)
	}
	if command == "etcd check" {
		if *probeTimeout <= 0 {
			return 2, errors.New("probe-timeout must be positive")
		}
		return checkEtcd(ctx, *binary, *endpoint, *ca, *cert, *key, *probeTimeout, out)
	}
	if err := os.MkdirAll(*dir, 0700); err != nil {
		return 1, err
	}
	task, err := os.MkdirTemp(*dir, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return 1, err
	}
	dest := filepath.Join(task, "snapshot.db")
	partial := dest + ".partial"
	a := []string{}
	for _, v := range [][2]string{{"--endpoints", *endpoint}, {"--cacert", *ca}, {"--cert", *cert}, {"--key", *key}} {
		if v[1] != "" {
			a = append(a, v[0], v[1])
		}
	}
	a = append(a, "snapshot", "save", partial)
	cmd := exec.CommandContext(ctx, *binary, a...)
	cmd.Stdout = diag
	// etcdctl diagnostics can include credential-bearing endpoint URLs or user info.
	// Do not forward raw stderr into logs or terminal output.
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return taskError(ctx, fmt.Errorf("snapshot failed (partial retained at %s); check etcd connectivity, TLS credentials and permissions", partial))
	}
	if err = os.Chmod(partial, 0600); err != nil {
		return 1, err
	}
	m, err := inspect(partial)
	if err != nil {
		return 1, err
	}
	if m.Size == 0 {
		return 1, errors.New("empty snapshot")
	}
	if err = os.Rename(partial, dest); err != nil {
		return 1, err
	}
	m.File = "snapshot.db"
	m.Upload = "not-requested"
	manifest := filepath.Join(task, "manifest.json")
	if s.bucket != "" {
		m.Upload = "pending"
		if s.key == "" {
			s.key = "etcd/" + filepath.Base(task) + "/snapshot.db"
		}
		m.Key = s.key
	}
	if err = writeManifest(manifest, m); err != nil {
		return 1, err
	}
	if s.bucket != "" {
		if err = s.upload(ctx, dest, m.SHA256); err != nil {
			m.Upload = "failed"
			saveErr := writeManifest(manifest, m)
			return 5, fmt.Errorf("local backup retained at %s; upload failed: %w; manifest update: %v", dest, err, saveErr)
		}
		m.Upload = "verified-metadata"
		if err = writeManifest(manifest, m); err != nil {
			return 5, err
		}
		s.key += ".manifest.json"
		mm, e := inspect(manifest)
		if e != nil {
			return 5, e
		}
		if e = s.upload(ctx, manifest, mm.SHA256); e != nil {
			return 5, fmt.Errorf("remote manifest failed; local backup retained: %w", e)
		}
	}
	fmt.Fprintln(diag, "Local snapshot:", dest)
	return 0, json.NewEncoder(out).Encode(m)
}
func taskError(ctx context.Context, err error) (int, error) {
	if ctx.Err() != nil {
		return 4, ctx.Err()
	}
	return 1, err
}
func inspect(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, errors.New("source must be a regular file")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	return Manifest{SchemaVersion: 1, File: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), Created: time.Now().UTC()}, err
}
func writeManifest(path string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(b); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
func (s storage) client(ctx context.Context) (*s3.Client, error) {
	options := []func(*config.LoadOptions) error{config.WithRegion(s.region)}
	if s.accessKey != "" {
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(s.accessKey, s.secretKey, s.sessionToken)))
	}
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = s.pathStyle
		if s.endpoint != "" {
			o.BaseEndpoint = aws.String(s.endpoint)
		}
	})
	return client, nil
}

func (s storage) upload(ctx context.Context, path, checksum string) error {
	client, err := s.client(ctx)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	_, err = manager.NewUploader(client).Upload(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key), Body: f, IfNoneMatch: aws.String("*"), Metadata: map[string]string{"sha256": checksum}})
	if err != nil {
		return err
	}
	head, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key)})
	if err != nil {
		return err
	}
	if aws.ToInt64(head.ContentLength) != info.Size() || head.Metadata["sha256"] != checksum {
		return errors.New("remote size or SHA-256 metadata mismatch")
	}
	return nil
}
