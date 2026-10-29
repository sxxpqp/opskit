// Validates only date-named, disposable Vagrant middleware clusters.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type pod struct {
	Metadata struct {
		Name, UID         string
		Labels            map[string]string
		DeletionTimestamp *string
	}
	Spec   struct{ Containers []struct{ Name string } }
	Status struct {
		Phase, PodIP string
		Conditions   []struct{ Type, Status string }
	}
}
type test struct {
	ctx                 context.Context
	base                []string
	ns, cluster, engine string
}

func (t test) kubectl(input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(t.ctx, 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "kubectl", append(append([]string{}, t.base...), args...)...)
	c.Stdin = bytes.NewReader(input)
	var out, diag bytes.Buffer
	c.Stdout = &out
	c.Stderr = &diag
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("kubectl operation failed: %w: %s", err, diag.String())
	}
	return out.Bytes(), nil
}
func (t test) pvcIdentity() (string, error) {
	raw, err := t.kubectl(nil, "get", "pvc", "-n", t.ns, "-l", "app.kubernetes.io/instance="+t.cluster, "-o", "json")
	if err != nil {
		return "", err
	}
	var list struct {
		Items []struct {
			Metadata struct{ Name, UID string }
			Spec     struct{ VolumeName string }
			Status   struct{ Phase string }
		}
	}
	if err = json.Unmarshal(raw, &list); err != nil {
		return "", err
	}
	state := map[string]string{}
	if len(list.Items) == 0 {
		return "", errors.New("no persistent claims")
	}
	for _, pvc := range list.Items {
		if pvc.Status.Phase != "Bound" || pvc.Metadata.UID == "" || pvc.Spec.VolumeName == "" {
			return "", errors.New("persistent claim is not bound")
		}
		state[pvc.Metadata.Name] = pvc.Metadata.UID + "/" + pvc.Spec.VolumeName
	}
	result, err := json.Marshal(state)
	return string(result), err
}
func (t test) pods() ([]pod, error) {
	raw, err := t.kubectl(nil, "get", "pods", "-n", t.ns, "-l", "app.kubernetes.io/instance="+t.cluster, "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct{ Items []pod }
	err = json.Unmarshal(raw, &list)
	return list.Items, err
}
func (t test) password(p pod) (string, string, error) {
	component := regexp.MustCompile(`-[0-9]+$`).ReplaceAllString(p.Metadata.Name, "")
	account := "root"
	if t.engine == "redis" {
		account = "default"
	}
	raw, err := t.kubectl(nil, "get", "secret", component+"-account-"+account, "-n", t.ns, "-o", "json")
	if err != nil {
		return "", "", err
	}
	var secret struct{ Data map[string]string }
	if err = json.Unmarshal(raw, &secret); err != nil {
		return "", "", err
	}
	u, e := base64.StdEncoding.DecodeString(secret.Data["username"])
	if e != nil {
		return "", "", e
	}
	pword, e := base64.StdEncoding.DecodeString(secret.Data["password"])
	if e != nil {
		return "", "", e
	}
	if len(u) == 0 || len(pword) == 0 || bytes.ContainsAny(pword, "\r\n") {
		return "", "", errors.New("invalid test credentials")
	}
	return string(u), string(pword), nil
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (t test) cli(p pod, password string, args ...string) (string, error) {
	return t.cliInput(p, password, "", args...)
}
func (t test) cliInput(p pod, password, input string, args ...string) (string, error) {
	return t.cliHost(p, password, "127.0.0.1", input, args...)
}
func (t test) cliHost(p pod, password, host, input string, args ...string) (string, error) {
	container := ""
	for _, c := range p.Spec.Containers {
		if strings.Contains(c.Name, t.engine) {
			container = c.Name
			break
		}
	}
	if container == "" {
		return "", errors.New("database container not found")
	}
	prefix := `IFS= read -r p; export REDISCLI_AUTH="$p"; exec redis-cli -c --raw`
	if t.engine == "mysql" {
		prefix = `IFS= read -r p; export MYSQL_PWD="$p"; exec mysql -uroot -h ` + quote(host) + ` -N -s -e`
	}
	for _, a := range args {
		prefix += " " + quote(a)
	}
	raw, err := t.kubectl([]byte(password+"\n"+input), "exec", "-i", "-n", t.ns, p.Metadata.Name, "-c", container, "--", "sh", "-ec", prefix)
	return strings.TrimSpace(string(raw)), err
}
func (t test) wait(check func() error) error {
	var last error
	for {
		if last = check(); last == nil {
			return nil
		}
		select {
		case <-t.ctx.Done():
			return fmt.Errorf("validation timeout: %w; %v", t.ctx.Err(), last)
		case <-time.After(3 * time.Second):
		}
	}
}
func (t test) replaced(old pod) error {
	return t.wait(func() error {
		pods, err := t.pods()
		if err != nil {
			return err
		}
		n := 0
		found := false
		for _, p := range pods {
			if p.Status.Phase == "Succeeded" {
				continue
			}
			n++
			if p.Metadata.Name == old.Metadata.Name {
				found = p.Metadata.UID != old.Metadata.UID
			}
			ready := false
			for _, c := range p.Status.Conditions {
				if c.Type == "Ready" && c.Status == "True" {
					ready = true
				}
			}
			if !ready || p.Metadata.DeletionTimestamp != nil {
				return errors.New("waiting for ready replacement")
			}
		}
		required := map[string]int{"mysql": 3, "redis": 6, "minio": 4}[t.engine]
		if !found || n < required {
			return errors.New("replacement UID or replicas not ready")
		}
		return nil
	})
}
func (t test) remove(p pod) error {
	_, err := t.kubectl(nil, "delete", "pod", p.Metadata.Name, "-n", t.ns, "--wait=false")
	return err
}
func (t test) mysql(token string) error {
	primary := func() (pod, error) {
		pods, err := t.pods()
		if err != nil {
			return pod{}, err
		}
		for _, p := range pods {
			for k, v := range p.Metadata.Labels {
				if strings.HasSuffix(k, "/role") && v == "primary" && p.Metadata.DeletionTimestamp == nil {
					return p, nil
				}
			}
		}
		return pod{}, errors.New("no primary")
	}
	p, err := primary()
	if err != nil {
		return err
	}
	_, secret, err := t.password(p)
	if err != nil {
		return err
	}
	query := func(p pod, sql string) (string, error) { return t.cli(p, secret, sql) }
	serviceQuery := func(p pod, sql string) (string, error) {
		return t.cliHost(p, secret, t.cluster+"-mysql."+t.ns+".svc", "", sql)
	}
	sql := "CREATE DATABASE IF NOT EXISTS opskit_kb_validation; CREATE TABLE IF NOT EXISTS opskit_kb_validation.probe (id VARCHAR(64) PRIMARY KEY, value VARCHAR(64)); INSERT INTO opskit_kb_validation.probe VALUES ('" + token + "','" + token + "');"
	if _, err = serviceQuery(p, sql); err != nil {
		return err
	}
	read := func(p pod) error {
		value, err := query(p, "SELECT value FROM opskit_kb_validation.probe WHERE id='"+token+"';")
		if err != nil {
			return err
		}
		if value != token {
			return errors.New("MySQL data mismatch")
		}
		return nil
	}
	// Require replication before replacement, rather than accepting an initial primary-only write.
	pods, err := t.pods()
	if err != nil {
		return err
	}
	for _, replica := range pods {
		if err = t.wait(func() error { return read(replica) }); err != nil {
			return err
		}
	}
	if err = t.remove(p); err != nil {
		return err
	}
	if err = t.wait(func() error {
		next, err := primary()
		if err != nil {
			return err
		}
		if next.Metadata.Name == p.Metadata.Name {
			return errors.New("waiting for another primary")
		}
		if err = read(next); err != nil {
			return err
		}
		writable, e := serviceQuery(next, "SELECT @@global.read_only;")
		if e != nil {
			return e
		}
		if writable != "0" {
			return errors.New("MySQL Service does not point to a writable primary")
		}
		_, err = serviceQuery(next, "INSERT INTO opskit_kb_validation.probe VALUES ('"+token+"-after','"+token+"') ON DUPLICATE KEY UPDATE value=VALUES(value);")
		return err
	}); err != nil {
		return err
	}
	if err = t.replaced(p); err != nil {
		return err
	}
	next, err := primary()
	if err != nil {
		return err
	}
	if err = read(next); err != nil {
		return err
	}
	value, err := serviceQuery(next, "SELECT value FROM opskit_kb_validation.probe WHERE id='"+token+"-after';")
	if err != nil {
		return err
	}
	if value != token {
		return errors.New("MySQL Service data mismatch after recovery")
	}
	return nil
}
func (t test) redis(token string) error {
	pods, err := t.pods()
	if err != nil || len(pods) < 6 {
		return errors.New("need six Redis pods")
	}
	seed := pods[0]
	_, secret, err := t.password(seed)
	if err != nil {
		return err
	}
	key := "opskit:kube:e2e:" + token
	slotRaw, err := t.cli(seed, secret, "CLUSTER", "KEYSLOT", key)
	if err != nil {
		return err
	}
	slot, err := strconv.Atoi(slotRaw)
	if err != nil {
		return err
	}
	nodes, err := t.cli(seed, secret, "CLUSTER", "NODES")
	if err != nil {
		return err
	}
	targetIP := ""
	for _, line := range strings.Split(nodes, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 || !strings.Contains(f[2], "master") {
			continue
		}
		for _, s := range f[8:] {
			parts := strings.Split(s, "-")
			low, e := strconv.Atoi(parts[0])
			if e != nil {
				continue
			}
			high := low
			if len(parts) == 2 {
				high, _ = strconv.Atoi(parts[1])
			}
			if slot >= low && slot <= high {
				targetIP = strings.Split(f[1], ":")[0]
			}
		}
	}
	var target, other pod
	for _, p := range pods {
		if p.Status.PodIP == targetIP {
			target = p
		} else {
			other = p
		}
	}
	if target.Metadata.Name == "" || other.Metadata.Name == "" {
		return errors.New("could not identify key's master")
	}
	if value, e := t.cli(other, secret, "SET", key, token); e != nil || value != "OK" {
		return fmt.Errorf("Redis initial write failed: %v", e)
	}
	// SET and WAIT must share one connection to prove this write reached a replica.
	syncResult, err := t.cliInput(target, secret, "SET "+key+" "+token+"\nWAIT 1 5000\n")
	if err != nil || !strings.HasSuffix(syncResult, "\n1") {
		return fmt.Errorf("Redis replica acknowledgement failed: %v", err)
	}
	if err = t.remove(target); err != nil {
		return err
	}
	if err = t.wait(func() error {
		value, err := t.cli(other, secret, "GET", key)
		if err != nil {
			return err
		}
		if value != token {
			return errors.New("Redis data mismatch")
		}
		value, err = t.cli(other, secret, "SET", key+":after", token)
		if err != nil || value != "OK" {
			return errors.New("Redis replacement write unavailable")
		}
		return nil
	}); err != nil {
		return err
	}
	if err = t.replaced(target); err != nil {
		return err
	}
	value, err := t.cli(other, secret, "GET", key+":after")
	if err != nil {
		return err
	}
	if value != token {
		return errors.New("Redis replacement data mismatch")
	}
	return nil
}
func (t test) minio(token, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.Path != "" {
		return errors.New("provide test NodePort http endpoint without path")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return errors.New("test endpoint must use private or loopback IP")
	}
	pods, err := t.pods()
	if err != nil || len(pods) < 4 {
		return errors.New("need four MinIO pods")
	}
	user, secret, err := t.password(pods[0])
	if err != nil {
		return err
	}
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(user, secret, ""), HTTPClient: &http.Client{Timeout: 10 * time.Second}}, func(o *s3.Options) { o.UsePathStyle = true; o.BaseEndpoint = aws.String(endpoint) })
	bucket := "opskit-kb-e2e-" + token
	if _, err = client.CreateBucket(t.ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return err
	}
	payload := []byte("opskit-kubeblocks-validation-" + token)
	put := func(name string) error {
		_, e := client.PutObject(t.ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(name), Body: bytes.NewReader(payload)})
		return e
	}
	get := func(name string) error {
		o, e := client.GetObject(t.ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(name)})
		if e != nil {
			return e
		}
		defer o.Body.Close()
		b, e := io.ReadAll(o.Body)
		if e != nil {
			return e
		}
		if !bytes.Equal(b, payload) {
			return errors.New("MinIO payload mismatch")
		}
		return nil
	}
	if err = put("before"); err != nil {
		return err
	}
	if err = t.remove(pods[0]); err != nil {
		return err
	}
	if err = t.wait(func() error {
		if err := get("before"); err != nil {
			return err
		}
		return put("after")
	}); err != nil {
		return err
	}
	if err = t.replaced(pods[0]); err != nil {
		return err
	}
	if err = get("before"); err != nil {
		return err
	}
	return get("after")
}
func main() {
	kubeconfig := flag.String("kubeconfig", "", "test kubeconfig")
	namespace := flag.String("namespace", "", "date-named test namespace")
	cluster := flag.String("cluster", "", "date-named test cluster")
	engine := flag.String("engine", "", "mysql/redis/minio")
	endpoint := flag.String("endpoint", "", "MinIO test NodePort endpoint")
	flag.Parse()
	if *kubeconfig == "" || !regexp.MustCompile(`^opskit-kb-e2e-[0-9]{8}$`).MatchString(*namespace) || !regexp.MustCompile(`^opskit-kb-[0-9]{8}-(mysql|redis|minio)$`).MatchString(*cluster) || !strings.HasSuffix(*cluster, "-"+*engine) {
		fmt.Fprintln(os.Stderr, "refuse non-disposable test target")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	t := test{ctx: ctx, base: []string{"--kubeconfig", *kubeconfig, "--request-timeout=30s"}, ns: *namespace, cluster: *cluster, engine: *engine}
	raw, err := t.kubectl(nil, "config", "current-context")
	if err == nil {
		t.base = append(t.base, "--context", strings.TrimSpace(string(raw)))
	}
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		err = e
	}
	token := hex.EncodeToString(b)
	beforeClaims := ""
	if err == nil {
		raw, err = t.kubectl(nil, "get", "clusters.apps.kubeblocks.io", t.cluster, "-n", t.ns, "-o", "json")
		var object struct {
			Metadata struct{ Labels map[string]string }
			Status   struct{ Phase string }
		}
		if err == nil {
			err = json.Unmarshal(raw, &object)
		}
		if err == nil && (object.Metadata.Labels["app.kubernetes.io/managed-by"] != "opskit" || object.Status.Phase != "Running") {
			err = errors.New("test Cluster must be opskit-owned and Running")
		}
	}
	if err == nil {
		beforeClaims, err = t.pvcIdentity()
	}
	if err == nil {
		switch *engine {
		case "mysql":
			err = t.mysql(token)
		case "redis":
			err = t.redis(token)
		case "minio":
			err = t.minio(token, *endpoint)
		default:
			err = errors.New("unsupported engine")
		}
	}
	if err == nil {
		afterClaims, e := t.pvcIdentity()
		err = e
		if err == nil && afterClaims != beforeClaims {
			err = errors.New("PVC identity or backing PV changed during replacement")
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"engine": *engine, "success": true, "pod_replacement_read_write": true, "pvc_persistence": true, "scope": "colocated Vagrant test; no host failure isolation"})
}
