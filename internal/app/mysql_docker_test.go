package app

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeMySQLDocker() {
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(90)
	}
	if trace := os.Getenv("OPSKIT_DOCKER_TRACE"); trace != "" {
		f, err := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(91)
		}
		_ = json.NewEncoder(f).Encode(args)
		f.Close()
	}
	if args[0] == "rm" {
		if os.Getenv("OPSKIT_DOCKER_RM_FAIL") == "yes" {
			os.Exit(1)
		}
		return
	}
	if args[0] == "ps" {
		if os.Getenv("OPSKIT_DOCKER_PS_FAIL") == "yes" {
			os.Exit(1)
		}
		return
	}
	if os.Getenv("OPSKIT_DOCKER_FAIL") == "yes" {
		fmt.Fprint(os.Stderr, mysqlTestPassword)
		os.Exit(9)
	}
	var auth, tool string
	imageIndex := 0
	for i, arg := range args {
		if arg == "--mount" {
			fields, err := csv.NewReader(strings.NewReader(args[i+1])).Read()
			if err != nil {
				os.Exit(92)
			}
			for _, field := range fields {
				if strings.HasPrefix(field, "source=") {
					auth = strings.TrimPrefix(field, "source=")
				}
			}
			if len(fields) != 4 || fields[2] != "target=/opskit-auth" || fields[3] != "readonly" {
				os.Exit(93)
			}
		}
		if arg == "--entrypoint" {
			tool = args[i+1]
			imageIndex = i + 2
		}
	}
	if auth == "" || imageIndex == 0 || tool != "/bin/sh" {
		os.Exit(94)
	}
	clientArgs := append([]string{}, args[imageIndex+1:]...)
	if len(clientArgs) < 5 || clientArgs[0] != "-c" || clientArgs[2] != "opskit-mysql" {
		os.Exit(97)
	}
	tool = clientArgs[3]
	clientArgs = clientArgs[4:]
	if (tool != "mysql" && tool != "mysqldump") || clientArgs[0] != "--defaults-file=/opskit-client/client.cnf" {
		os.Exit(95)
	}
	for i, arg := range clientArgs {
		if before, after, ok := strings.Cut(arg, "=/opskit-client/"); ok {
			path := filepath.Join(auth, after)
			if _, err := os.Stat(path); err != nil {
				os.Exit(96)
			}
			clientArgs[i] = before + "=" + path
		}
	}
	os.Args = append([]string{tool}, clientArgs...)
	fakeMySQLClient()
}

func mysqlDockerTestArgs(t *testing.T, root string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPSKIT_FAKE_MYSQL_DOCKER", "1")
	t.Setenv("MYSQL_BACKUP_PASSWORD", mysqlTestPassword)
	t.Setenv("MYSQL_PWD", "ambient-password")
	return []string{"mysql", "backup", "--database", "app", "--client-mode", "docker", "--docker-bin", exe, "--host", "database.internal", "--directory", root, "--mysql-client", "not-installed", "--mysqldump", "not-installed"}
}

func TestMySQLDockerBackupAndTLSMount(t *testing.T) {
	t.Chdir(t.TempDir())
	root := filepath.Join(t.TempDir(), "backups, with spaces")
	args := mysqlDockerTestArgs(t, root)
	trace := filepath.Join(t.TempDir(), "docker.jsonl")
	t.Setenv("OPSKIT_DOCKER_TRACE", trace)
	certs := t.TempDir()
	for _, name := range []string{"ca.pem", "cert.pem", "key.pem"} {
		if err := os.WriteFile(filepath.Join(certs, name), []byte("test certificate"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args = append(args, "--ssl-mode", "VERIFY_IDENTITY", "--ssl-ca", filepath.Join(certs, "ca.pem"), "--ssl-cert", filepath.Join(certs, "cert.pem"), "--ssl-key", filepath.Join(certs, "key.pem"), "--docker-network", "db-network", "--pull-policy", "never")
	code, err, text := workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	files, _ := filepath.Glob(filepath.Join(root, "*", "backup.sql.gz"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	raw, _ := os.ReadFile(trace)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var calls [][]string
	for {
		var args []string
		if err := decoder.Decode(&args); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, args)
	}
	if len(calls) != 2 {
		t.Fatal(calls)
	}
	for i, args := range calls {
		joined := strings.Join(args, " ")
		for _, want := range []string{"run --rm --name opskit-mysql-", "--network db-network", "--pull never", "--entrypoint /bin/sh", "opskit-mysql " + []string{"mysql", "mysqldump"}[i], "--tmpfs /opskit-client:", "--defaults-file=/opskit-client/client.cnf", "--ssl-ca=/opskit-client/ca.pem", "--ssl-key=/opskit-client/key.pem"} {
			if !strings.Contains(joined, want) {
				t.Fatal("missing Docker argument", want, joined)
			}
		}
		if strings.Contains(joined, mysqlTestPassword) || strings.Contains(joined, "not-installed") {
			t.Fatal("secret or local command leaked", joined)
		}
	}
	if auth, _ := filepath.Glob(filepath.Join(root, "*", ".mysql-auth-*")); len(auth) != 0 {
		t.Fatal("credentials retained", auth)
	}
}

func TestMySQLDockerFailureAndCancellation(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, scenario := range []string{"docker-failure", "dump-failure", "cancel", "cleanup-unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			args := mysqlDockerTestArgs(t, root)
			trace := filepath.Join(t.TempDir(), "docker.jsonl")
			t.Setenv("OPSKIT_DOCKER_TRACE", trace)
			switch scenario {
			case "docker-failure":
				t.Setenv("OPSKIT_DOCKER_FAIL", "yes")
			case "dump-failure":
				t.Setenv("OPSKIT_MYSQL_FAIL", "dump")
			case "cancel":
				t.Setenv("OPSKIT_MYSQL_FAIL", "cancel")
				args = append(args, "--timeout", "2s")
			case "cleanup-unconfirmed":
				t.Setenv("OPSKIT_DOCKER_FAIL", "yes")
				t.Setenv("OPSKIT_DOCKER_RM_FAIL", "yes")
				t.Setenv("OPSKIT_DOCKER_PS_FAIL", "yes")
			}
			code, err, text := workflowRun(t, args...)
			wantCode := 1
			if scenario == "cancel" {
				wantCode = 4
			}
			if code != wantCode || err == nil {
				t.Fatal(code, err, text)
			}
			if strings.Contains(text+err.Error(), mysqlTestPassword) {
				t.Fatal("secret leaked")
			}
			raw, _ := os.ReadFile(trace)
			if !strings.Contains(string(raw), `["rm","--force","opskit-mysql-`) {
				t.Fatal("container cleanup missing", string(raw))
			}
			if scenario == "cleanup-unconfirmed" && !strings.Contains(err.Error(), "无法确认临时容器") {
				t.Fatal("cleanup failure hidden", err)
			}
			if files, _ := filepath.Glob(filepath.Join(root, "*", "manifest.json")); len(files) != 0 {
				t.Fatal("failure published", files)
			}
			if auth, _ := filepath.Glob(filepath.Join(root, "*", ".mysql-auth-*")); len(auth) != 0 {
				t.Fatal("credentials retained", auth)
			}
		})
	}
}

func TestMySQLDockerConfigAndDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	file := filepath.Join(t.TempDir(), "config.yaml")
	body := "schema_version: 1\nmysql:\n  databases: [app]\n  host: db.internal\n  client_mode: docker\n  docker:\n    image: private.example/mysql:8.0\n    binary: ./bin/docker\n    network: db-network\n    pull_policy: never\n"
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	code, err, text := workflowRun(t, "mysql", "backup", "--config", file, "--dry-run", "--image", "override.example/mysql:8.4")
	if code != 0 || err != nil || !strings.Contains(text, `"client_mode":"docker"`) || !strings.Contains(text, "override.example/mysql:8.4") || !strings.Contains(text, "db-network") {
		t.Fatal(code, err, text)
	}
	for _, args := range [][]string{{"--client-mode", "invalid"}, {"--client-mode", "docker"}, {"--client-mode", "docker", "--host", "db.internal", "--image", ""}, {"--client-mode", "docker", "--host", "db.internal", "--pull-policy", "bad"}} {
		code, err, _ := workflowRun(t, append([]string{"mysql", "backup", "--database", "app", "--dry-run"}, args...)...)
		if code != 2 || err == nil {
			t.Fatal("invalid Docker options accepted", args, code, err)
		}
	}
	code, err, _ = workflowRun(t, "mysql", "backup", "--client-mode", "docker", "--database", "app", "--host", "db.internal", "--docker-bin", "missing-docker")
	if code != 3 || err == nil {
		t.Fatal("missing Docker dependency", code, err)
	}
	code, err, text = workflowRun(t, "mysql", "backup", "--client-mode", "docker", "--docker-network", "host", "--database", "app", "--dry-run")
	if code != 0 || err != nil || !strings.Contains(text, "127.0.0.1") {
		t.Fatal(code, err, text)
	}
}

func TestMySQLDockerBootstrapKeepsDatabaseAsArgument(t *testing.T) {
	t.Chdir(t.TempDir())
	args := mysqlDockerTestArgs(t, t.TempDir())
	database := "app name'; $(touch /tmp/must-not-run)"
	args[3] = database
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("OPSKIT_DOCKER_TRACE", trace)
	code, err, text := workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err, text)
	}
	raw, _ := os.ReadFile(trace)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var dump []string
	for {
		var call []string
		if err := decoder.Decode(&call); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		for i, value := range call {
			if value == "-c" && i+1 < len(call) && strings.Contains(call[i+1], database) {
				t.Fatal("database interpolated into shell script")
			}
		}
		dump = call
	}
	if len(dump) == 0 || dump[len(dump)-1] != database {
		t.Fatal("database argv changed", dump)
	}
}
