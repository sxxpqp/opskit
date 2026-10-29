// Fault tests for newly-created Docker validation projects; never run on production projects.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type testProject struct {
	ctx                               context.Context
	directory, project, dockerContext string
	opskit, config                    string
}

func (p testProject) command(ctx context.Context, input string, args ...string) (string, error) {
	prefix := []string{}
	if p.dockerContext != "" {
		prefix = append(prefix, "--context", p.dockerContext)
	}
	prefix = append(prefix, "compose", "--project-name", p.project, "--project-directory", p.directory, "-f", filepath.Join(p.directory, "compose.yaml"))
	prefix = append(prefix, args...)
	cmd := exec.CommandContext(ctx, "docker", prefix...)
	cmd.Stdin = strings.NewReader(input)
	var output, diag bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &diag
	if err := cmd.Run(); err != nil {
		return output.String(), fmt.Errorf("docker compose %s failed: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(output.String()), nil
}
func (p testProject) run(args ...string) (string, error) { return p.command(p.ctx, "", args...) }
func (p testProject) restore(services ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append([]string{"start"}, services...)
	_, _ = p.command(ctx, "", args...)
}
func poll(ctx context.Context, limit time.Duration, check func() bool) error {
	until := time.Now().Add(limit)
	for time.Now().Before(until) {
		if check() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("condition did not become ready before timeout")
}

func redisTest(p testProject, report map[string]any) error {
	cli := func(args ...string) (string, error) {
		return p.run(append([]string{"exec", "-T", "redis2", "redis-cli", "--raw", "-c"}, args...)...)
	}
	key := ""
	for i := 0; i < 100; i++ {
		candidate := fmt.Sprintf("opskit-ha-proof-%d", i)
		text, err := cli("CLUSTER", "KEYSLOT", candidate)
		if err != nil {
			return err
		}
		slot, err := strconv.Atoi(text)
		if err == nil && slot < 5461 {
			key = candidate
			break
		}
	}
	if key == "" {
		return errors.New("could not select a slot on redis1")
	}
	if out, err := cli("SET", key, "before-failover"); err != nil || !strings.HasSuffix(out, "OK") {
		return errors.New("initial Redis write failed")
	}
	if err := poll(p.ctx, 15*time.Second, func() bool {
		out, err := p.command(p.ctx, "READONLY\nGET "+key+"\n", "exec", "-T", "redis6", "redis-cli", "--raw")
		return err == nil && strings.HasSuffix(out, "before-failover")
	}); err != nil {
		return fmt.Errorf("replica did not receive data: %w", err)
	}
	defer p.restore("redis1")
	if _, err := p.run("stop", "redis1"); err != nil {
		return err
	}
	if err := poll(p.ctx, 60*time.Second, func() bool {
		out, err := cli("GET", key)
		return err == nil && strings.HasSuffix(out, "before-failover")
	}); err != nil {
		return fmt.Errorf("Redis primary failover: %w", err)
	}
	if out, err := cli("SET", key, "after-failover"); err != nil || !strings.HasSuffix(out, "OK") {
		return errors.New("Redis write after primary failure failed")
	}
	report["primary_failure_read_write"] = true
	p.restore("redis1")
	if _, err := p.run("stop", "redis1", "redis2", "redis3", "redis4", "redis5", "redis6"); err != nil {
		return err
	}
	if _, err := p.run("start", "redis1", "redis2", "redis3", "redis4", "redis5", "redis6"); err != nil {
		return err
	}
	if err := poll(p.ctx, 90*time.Second, func() bool {
		out, err := cli("GET", key)
		return err == nil && strings.HasSuffix(out, "after-failover")
	}); err != nil {
		return fmt.Errorf("Redis restart persistence: %w", err)
	}
	if _, err := cli("DEL", key); err != nil {
		return err
	}
	report["full_restart_persistence"] = true
	return nil
}

func mysqlTest(p testProject, report map[string]any) error {
	client := "mysql2"
	sql := func(query string) (string, error) {
		return p.run("exec", "-T", client, "sh", "-ec", `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --protocol TCP -hrouter -P6446 -uroot -NB -e "$1"`, "sh", query)
	}
	primary, err := sql("SELECT @@hostname")
	if err != nil {
		return err
	}
	if primary != "mysql1" && primary != "mysql2" && primary != "mysql3" {
		return errors.New("unexpected initial primary hostname")
	}
	if client == primary {
		client = "mysql3"
	}
	if _, err := sql("DROP DATABASE IF EXISTS opskit_ha_validation; CREATE DATABASE opskit_ha_validation; CREATE TABLE opskit_ha_validation.proof(id INT PRIMARY KEY,v VARCHAR(64)); INSERT INTO opskit_ha_validation.proof VALUES(1,'before-failover')"); err != nil {
		return err
	}
	defer func() { _, _ = sql("DROP DATABASE IF EXISTS opskit_ha_validation") }()
	defer p.restore(primary)
	if _, err := p.run("stop", primary); err != nil {
		return err
	}
	if err := poll(p.ctx, 90*time.Second, func() bool {
		out, err := sql("SELECT v FROM opskit_ha_validation.proof WHERE id=1")
		return err == nil && out == "before-failover"
	}); err != nil {
		return fmt.Errorf("Router failover read: %w", err)
	}
	if _, err := sql("INSERT INTO opskit_ha_validation.proof VALUES(2,'after-failover')"); err != nil {
		return err
	}
	newPrimary, err := sql("SELECT @@hostname")
	if err != nil || newPrimary == primary {
		return errors.New("Router did not change primary")
	}
	report["primary_failure_router_read_write"] = true
	p.restore(primary)
	if err := poll(p.ctx, 90*time.Second, func() bool {
		out, err := sql("SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_STATE='ONLINE'")
		return err == nil && out == "3"
	}); err != nil {
		return fmt.Errorf("old primary rejoin: %w", err)
	}
	if _, err := p.run("stop", "mysql1", "mysql2", "mysql3", "router"); err != nil {
		return err
	}
	if _, err := p.run("start", "mysql1", "mysql2", "mysql3", "router"); err != nil {
		return err
	}
	if _, err := p.run("up", "-d", "--wait", "--wait-timeout", "300", "mysql1", "mysql2", "mysql3"); err != nil {
		return err
	}
	if p.opskit == "" || p.config == "" {
		return errors.New("MySQL full-outage validation needs --opskit and --config for explicit recovery")
	}
	recovery := exec.CommandContext(p.ctx, p.opskit, "middleware", "recover", "--config", p.config, "--directory", p.directory, "--timeout", "5m")
	output, err := recovery.CombinedOutput()
	os.WriteFile(filepath.Join(filepath.Dir(p.directory), "mysql-e2e-recover.log"), output, 0600)
	if err != nil {
		return fmt.Errorf("explicit complete-outage recovery failed: %w", err)
	}
	report["explicit_complete_outage_recovery"] = true
	if err := poll(p.ctx, 120*time.Second, func() bool {
		out, err := sql("SELECT GROUP_CONCAT(v ORDER BY id) FROM opskit_ha_validation.proof")
		return err == nil && out == "before-failover,after-failover"
	}); err != nil {
		return fmt.Errorf("MySQL restart persistence: %w", err)
	}
	report["full_restart_persistence"] = true
	return nil
}

func minioTest(p testProject, endpoint, user string, report map[string]any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return errors.New("MinIO fault tests require an explicit HTTP loopback endpoint")
	}
	raw, err := os.ReadFile(filepath.Join(p.directory, ".env"))
	if err != nil {
		return err
	}
	secret := strings.TrimPrefix(strings.TrimSpace(string(raw)), "OPSKIT_CLUSTER_PASSWORD=")
	if secret == "" {
		return errors.New("missing private deployment credentials")
	}
	cfg, err := config.LoadDefaultConfig(p.ctx, config.WithRegion("us-east-1"), config.WithRetryMaxAttempts(1), config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(user, secret, "")))
	if err != nil {
		return err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true; o.BaseEndpoint = aws.String(endpoint) })
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	bucket := "opskit-ha-" + hex.EncodeToString(id)
	if _, err := client.CreateBucket(p.ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return err
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, key := range []string{"before", "after"} {
			_, _ = client.DeleteObject(clean, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		}
		_, _ = client.DeleteBucket(clean, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	}()
	data := make([]byte, 64*1024)
	if _, err := rand.Read(data); err != nil {
		return err
	}
	put := func(key string) error {
		_, err := client.PutObject(p.ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(data)})
		return err
	}
	read := func(key string) bool {
		response, err := client.GetObject(p.ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		if err != nil {
			return false
		}
		defer response.Body.Close()
		actual, err := io.ReadAll(response.Body)
		return err == nil && bytes.Equal(actual, data)
	}
	if err := put("before"); err != nil {
		return err
	}
	if !read("before") {
		return errors.New("initial S3 data mismatch")
	}
	defer p.restore("minio1")
	if _, err := p.run("stop", "minio1"); err != nil {
		return err
	}
	if err := poll(p.ctx, 30*time.Second, func() bool { return read("before") }); err != nil {
		return errors.New("MinIO frontend read after backend failure failed")
	}
	if err := poll(p.ctx, 30*time.Second, func() bool { return put("after") == nil }); err != nil {
		return err
	}
	if !read("after") {
		return errors.New("MinIO write after one node failure failed")
	}
	report["single_node_failure_read_write"] = true
	p.restore("minio1")
	if _, err := p.run("stop", "minio1", "minio2", "minio3", "minio4"); err != nil {
		return err
	}
	if _, err := p.run("start", "minio1", "minio2", "minio3", "minio4"); err != nil {
		return err
	}
	if err := poll(p.ctx, 90*time.Second, func() bool { return read("before") && read("after") }); err != nil {
		return fmt.Errorf("MinIO restart persistence: %w", err)
	}
	report["full_restart_persistence"] = true
	return nil
}

func main() {
	directory := flag.String("directory", "", "private test deployment directory")
	project := flag.String("project", "", "owned test compose project")
	dockerContext := flag.String("docker-context", "desktop-linux", "Docker context")
	engine := flag.String("engine", "", "redis/mysql/minio")
	endpoint := flag.String("endpoint", "", "MinIO loopback S3 endpoint")
	user := flag.String("minio-user", "opskitadmin", "MinIO test root user")
	opskit := flag.String("opskit", "", "opskit binary for MySQL complete-outage recovery")
	configPath := flag.String("config", "", "middleware config for MySQL recovery")
	flag.Parse()
	report := map[string]any{"engine": *engine, "success": false}
	run := func() error {
		if *directory == "" || !regexp.MustCompile(`^opskit-ha-[0-9]{8}-(redis|mysql|minio)$`).MatchString(*project) {
			return errors.New("explicit dedicated opskit-ha-* test project required")
		}
		abs, err := filepath.Abs(*directory)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(abs, "ownership.json"))
		if err != nil {
			return err
		}
		var identity struct{ Name, Engine string }
		if json.Unmarshal(raw, &identity) != nil || identity.Name+"-"+identity.Engine != *project || identity.Engine != *engine {
			return errors.New("test directory ownership does not match project")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		p := testProject{ctx: ctx, directory: abs, project: *project, dockerContext: *dockerContext, opskit: *opskit, config: *configPath}
		switch *engine {
		case "redis":
			return redisTest(p, report)
		case "mysql":
			return mysqlTest(p, report)
		case "minio":
			return minioTest(p, *endpoint, *user, report)
		default:
			return errors.New("unknown test engine")
		}
	}
	err := run()
	if err != nil {
		report["error"] = err.Error()
	} else {
		report["success"] = true
	}
	json.NewEncoder(os.Stdout).Encode(report)
	if err != nil {
		os.Exit(1)
	}
}
