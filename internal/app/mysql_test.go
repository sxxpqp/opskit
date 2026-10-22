package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

const mysqlTestPassword = " secret#\"\\word\n[mysql]\npassword=other "
const mysqlTestSQL = "CREATE DATABASE IF NOT EXISTS `app`;\nUSE `app`;\nCREATE TABLE example(id INT);\nINSERT INTO example VALUES (42);\n"

func fakeMySQLClient() {
	if len(os.Args) < 2 || !strings.HasPrefix(os.Args[1], "--defaults-file=") {
		os.Exit(81)
	}
	file := strings.TrimPrefix(os.Args[1], "--defaults-file=")
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "[client]\npassword="+mysqlOptionQuote(mysqlTestPassword)+"\n" {
		os.Exit(82)
	}
	if os.Getenv("MYSQL_BACKUP_PASSWORD") != "" || os.Getenv("MYSQL_PWD") != "" || strings.Contains(strings.Join(os.Args, " "), mysqlTestPassword) {
		os.Exit(83)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			os.Exit(84)
		}
	}
	if path := os.Getenv("OPSKIT_MYSQL_TRACE"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(85)
		}
		_ = json.NewEncoder(f).Encode(os.Args[1:])
		f.Close()
	}
	probe := false
	for _, arg := range os.Args[1:] {
		probe = probe || strings.HasPrefix(arg, "--execute=")
	}
	if probe {
		if os.Getenv("OPSKIT_MYSQL_FAIL") == "preflight" {
			fmt.Fprint(os.Stderr, mysqlTestPassword)
			os.Exit(9)
		}
		fmt.Print(envOr("OPSKIT_MYSQL_REPORT", "1\t0\n"))
		return
	}
	switch os.Getenv("OPSKIT_MYSQL_FAIL") {
	case "empty":
		return
	case "cancel":
		fmt.Print("partial SQL")
		time.Sleep(30 * time.Second)
	case "dump":
		fmt.Print("partial SQL")
		fmt.Fprint(os.Stderr, mysqlTestPassword)
		os.Exit(7)
	}
	fmt.Print(mysqlTestSQL)
}

func mysqlTestArgs(t *testing.T, root string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPSKIT_FAKE_MYSQL", "1")
	t.Setenv("MYSQL_BACKUP_PASSWORD", mysqlTestPassword)
	t.Setenv("MYSQL_PWD", "ambient-password-must-not-reach-child")
	return []string{"mysql", "backup", "--database", "app", "--mysql-client", exe, "--mysqldump", exe, "--directory", root}
}

func TestMySQLBackupSQLAndManifest(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, compressed := range []bool{true, false} {
		t.Run(fmt.Sprint(compressed), func(t *testing.T) {
			root := t.TempDir()
			args := mysqlTestArgs(t, root)
			trace := filepath.Join(t.TempDir(), "trace.jsonl")
			t.Setenv("OPSKIT_MYSQL_TRACE", trace)
			code, err, output := workflowRun(t, append(args, "--gzip="+fmt.Sprint(compressed))...)
			if code != 0 || err != nil {
				t.Fatal(code, err, output)
			}
			files, _ := filepath.Glob(filepath.Join(root, "*", "manifest.json"))
			if len(files) != 1 {
				t.Fatal(files)
			}
			raw, _ := os.ReadFile(files[0])
			var m Manifest
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			if m.Upload != "not-requested" || m.SchemaVersion != 1 {
				t.Fatal(m)
			}
			file := filepath.Join(filepath.Dir(files[0]), m.File)
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if compressed {
				zip, err := gzip.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				data, err = io.ReadAll(zip)
				zip.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if string(data) != mysqlTestSQL {
				t.Fatal(string(data))
			}
			inspected, err := inspect(file)
			if err != nil || inspected.SHA256 != m.SHA256 || inspected.Size != m.Size {
				t.Fatal(m, inspected, err)
			}
			if partial, _ := filepath.Glob(filepath.Join(root, "*", "*.partial")); len(partial) != 0 {
				t.Fatal(partial)
			}
			if auth, _ := filepath.Glob(filepath.Join(root, "*", ".mysql-auth-*")); len(auth) != 0 {
				t.Fatal("credentials retained", auth)
			}
			traceRaw, _ := os.ReadFile(trace)
			if strings.Contains(string(raw)+output+string(traceRaw), mysqlTestPassword) {
				t.Fatal("password leaked")
			}
			var calls [][]string
			decoder := json.NewDecoder(bytes.NewReader(traceRaw))
			for decoder.More() {
				var args []string
				if err := decoder.Decode(&args); err != nil {
					t.Fatal(err)
				}
				calls = append(calls, args)
			}
			if len(calls) != 2 {
				t.Fatal(calls)
			}
			joined := strings.Join(calls[1], " ")
			for _, option := range []string{"--single-transaction", "--quick", "--skip-lock-tables", "--set-gtid-purged=OFF", "--routines=true", "--events=true", "--triggers=true", "--databases app"} {
				if !strings.Contains(joined, option) {
					t.Fatal("missing option", option, joined)
				}
			}
			credentialPath := strings.TrimPrefix(calls[0][0], "--defaults-file=")
			if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
				t.Fatal("credential file remains", err)
			}
		})
	}
}

func TestMySQLBackupFailuresAndCleanup(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		name, fail, report string
		partial            bool
		code               int
	}{
		{"preflight", "preflight", "", false, 1},
		{"missing-database", "", "0\t0\n", false, 1},
		{"non-transactional", "", "1\t2\n", false, 1},
		{"invalid-report", "", "bad", false, 1},
		{"export-failure", "dump", "", true, 1},
		{"empty-export", "empty", "", true, 1},
		{"cancelled-export", "cancel", "", true, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			args := mysqlTestArgs(t, root)
			t.Setenv("OPSKIT_MYSQL_FAIL", tc.fail)
			t.Setenv("OPSKIT_MYSQL_REPORT", tc.report)
			if tc.fail == "cancel" {
				args = append(args, "--timeout", "2s")
			}
			code, err, text := workflowRun(t, args...)
			if code != tc.code || err == nil {
				t.Fatal(code, err, text)
			}
			if strings.Contains(text+err.Error(), mysqlTestPassword) {
				t.Fatal("secret leaked")
			}
			if files, _ := filepath.Glob(filepath.Join(root, "*", "manifest.json")); len(files) != 0 {
				t.Fatal("failure published", files)
			}
			if files, _ := filepath.Glob(filepath.Join(root, "*", "backup.sql.gz")); len(files) != 0 {
				t.Fatal("failure finalized", files)
			}
			if files, _ := filepath.Glob(filepath.Join(root, "*", "*.partial")); (len(files) == 1) != tc.partial {
				t.Fatal("partial retention", files)
			}
			if auth, _ := filepath.Glob(filepath.Join(root, "*", ".mysql-auth-*")); len(auth) != 0 {
				t.Fatal("credentials retained", auth)
			}
		})
	}
}

func TestMySQLBackupAcceptNonTransactionalAndMultipleDatabases(t *testing.T) {
	t.Chdir(t.TempDir())
	args := mysqlTestArgs(t, t.TempDir())
	t.Setenv("OPSKIT_MYSQL_REPORT", "2\t1\n")
	code, err, text := workflowRun(t, append(args, "--database", "logs", "--allow-non-transactional", "--routines=false", "--events=false", "--triggers=false")...)
	if code != 0 || err != nil || !strings.Contains(text, "不保证") {
		t.Fatal(code, err, text)
	}
}

func TestMySQLBackupValidationAndDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	t.Setenv("MYSQL_BACKUP_PASSWORD", "")
	dir := filepath.Join(t.TempDir(), "not-created")
	code, err, output := workflowRun(t, "mysql", "backup", "--database", "app", "--dry-run", "--directory", dir)
	if code != 0 || err != nil || !json.Valid([]byte(output)) {
		t.Fatal(code, err, output)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dry-run created files", err)
	}
	for _, args := range [][]string{
		{}, {"--database", ""}, {"--database=--all-databases"}, {"--database", "app", "--database", "app"},
		{"--database", "app", "--port", "0"}, {"--database", "app", "--timeout", "0s"},
		{"--database", "app", "--ssl-mode", "PREFERRED"}, {"--database", "app", "--ssl-mode", "VERIFY_IDENTITY"},
		{"--database", "app", "--ssl-cert", "cert.pem"}, {"--database", "app", "--password", "secret"},
		{"--database", "app", "--bucket", "", "--key", "orphan"},
	} {
		code, err, _ := workflowRun(t, append([]string{"mysql", "backup", "--dry-run"}, args...)...)
		if code != 2 || err == nil {
			t.Fatal("invalid options accepted", args, code, err)
		}
	}
	code, err, _ = workflowRun(t, "mysql", "backup", "--database", "app")
	if code != 3 || err == nil {
		t.Fatal("dependency exit code", code, err)
	}
}

func TestMySQLConfigPrecedenceAndIsolation(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "config.yaml")
	body := "schema_version: 1\ntimeout: 2m\netcd:\n  directory: ./etcd-only\nmysql:\n  host: mysql.example\n  port: 3307\n  user: reader\n  databases: [app, logs]\n  password_env: ''\n  directory: ./mysql-backups\n  mysqldump: ./bin/mysqldump\n  mysql_client: ./bin/mysql\n  gzip: false\n  routines: false\n  ssl_mode: VERIFY_IDENTITY\n  ssl_ca: ./ca.pem\n"
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cmd, err := newMySQLBackupCommand(context.Background(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	f := cmd.Flags()
	if err := f.Parse([]string{"--host", "explicit.example", "--database", "override", "--gzip=true"}); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(file, true, f); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"host": "explicit.example", "port": "3307", "user": "reader", "password-env": "", "directory": filepath.Join(base, "mysql-backups"), "mysqldump": filepath.Join(base, "bin/mysqldump"), "mysql-client": filepath.Join(base, "bin/mysql"), "ssl-ca": filepath.Join(base, "ca.pem"), "gzip": "true", "routines": "false", "timeout": "2m0s"} {
		if got := f.Lookup(key).Value.String(); got != want {
			t.Fatal(key, got, want)
		}
	}
	if values, _ := f.GetStringArray("database"); len(values) != 1 || values[0] != "override" {
		t.Fatal(values)
	}
	isolated := pflag.NewFlagSet("test", pflag.ContinueOnError)
	dir := isolated.String("directory", "default-etcd", "")
	if err := loadConfig(file, true, isolated); err != nil || *dir != filepath.Join(base, "etcd-only") {
		t.Fatal("MySQL config leaked to etcd", err, *dir)
	}
}

func TestMySQLOptionQuoteAndSchemaEscaping(t *testing.T) {
	if got, want := mysqlOptionQuote("x\"\\\n\r\t#"), "\"x\\\"\\\\\\n\\r\\t#\""; got != want {
		t.Fatalf("%q != %q", got, want)
	}
	t.Chdir(t.TempDir())
	args := mysqlTestArgs(t, t.TempDir())
	args[3] = "app'); DROP DATABASE victim;--"
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("OPSKIT_MYSQL_TRACE", trace)
	code, err, output := workflowRun(t, args...)
	if code != 0 || err != nil {
		t.Fatal(code, err, output)
	}
	raw, _ := os.ReadFile(trace)
	var first []string
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&first); err != nil {
		t.Fatal(err)
	}
	for _, arg := range first {
		if strings.HasPrefix(arg, "--execute=") && strings.Contains(arg, "DROP DATABASE") {
			t.Fatal("database injected into SQL", arg)
		}
	}
}

func TestMySQLUploadFailureRetainsLocal(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	root := t.TempDir()
	args := mysqlTestArgs(t, root)
	code, err, text := workflowRun(t, append(args, "--bucket", "test", "--s3-endpoint", server.URL, "--path-style")...)
	if code != 5 || err == nil {
		t.Fatal(code, err, text)
	}
	files, _ := filepath.Glob(filepath.Join(root, "*", "backup.sql.gz"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(files[0]), "manifest.json"))
	if !strings.Contains(string(raw), `"upload_status": "failed"`) || strings.Contains(text+err.Error()+string(raw), mysqlTestPassword) {
		t.Fatal(string(raw), text, err)
	}
}

func TestMySQLRemoteDataAndManifest(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, failAt := range []string{"", "data-head", "manifest"} {
		t.Run("fail="+failAt, func(t *testing.T) {
			t.Setenv("AWS_ACCESS_KEY_ID", "test")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
			type object struct {
				size int
				hash string
			}
			objects := map[string]object{}
			var objectMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				objectMu.Lock()
				defer objectMu.Unlock()
				isManifest := strings.HasSuffix(r.URL.Path, ".manifest.json")
				if failAt == "manifest" && isManifest || failAt == "data-head" && r.Method == "HEAD" && !isManifest {
					w.WriteHeader(403)
					return
				}
				switch r.Method {
				case "PUT":
					if r.Header.Get("If-None-Match") != "*" {
						t.Error("missing conditional write")
					}
					data, _ := io.ReadAll(r.Body)
					size := len(data)
					if decoded := r.Header.Get("X-Amz-Decoded-Content-Length"); decoded != "" {
						size, _ = strconv.Atoi(decoded)
					}
					objects[r.URL.Path] = object{size, r.Header.Get("X-Amz-Meta-Sha256")}
					w.Header().Set("ETag", `"test"`)
				case "HEAD":
					o, ok := objects[r.URL.Path]
					if !ok {
						w.WriteHeader(404)
						return
					}
					w.Header().Set("Content-Length", strconv.Itoa(o.size))
					w.Header().Set("X-Amz-Meta-Sha256", o.hash)
				default:
					t.Error("unexpected method", r.Method)
				}
			}))
			defer server.Close()
			root := t.TempDir()
			args := mysqlTestArgs(t, root)
			code, err, text := workflowRun(t, append(args, "--bucket", "test", "--s3-endpoint", server.URL, "--path-style")...)
			if failAt == "" && (code != 0 || err != nil) || failAt != "" && (code != 5 || err == nil) {
				t.Fatal(code, err, text)
			}
			files, _ := filepath.Glob(filepath.Join(root, "*", "manifest.json"))
			if len(files) != 1 {
				t.Fatal(files)
			}
			raw, _ := os.ReadFile(files[0])
			var m Manifest
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(m.Key, "mysql/") {
				t.Fatal(m)
			}
			objectMu.Lock()
			objectCount := len(objects)
			objectMu.Unlock()
			if failAt == "" && (m.Upload != "verified-metadata" || objectCount != 2) {
				t.Fatal(m, objectCount)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(files[0]), m.File)); err != nil {
				t.Fatal("local backup lost", err)
			}
		})
	}
}
