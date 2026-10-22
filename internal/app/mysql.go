package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

type mysqlBackupSettings struct {
	Host                  *string  `yaml:"host"`
	Port                  *int     `yaml:"port"`
	User                  *string  `yaml:"user"`
	PasswordEnv           *string  `yaml:"password_env"`
	Databases             []string `yaml:"databases"`
	Directory             *string  `yaml:"directory"`
	Dump                  *string  `yaml:"mysqldump"`
	Client                *string  `yaml:"mysql_client"`
	Gzip                  *bool    `yaml:"gzip"`
	Routines              *bool    `yaml:"routines"`
	Events                *bool    `yaml:"events"`
	Triggers              *bool    `yaml:"triggers"`
	AllowNonTransactional *bool    `yaml:"allow_non_transactional"`
	SSLMode               *string  `yaml:"ssl_mode"`
	SSLCA                 *string  `yaml:"ssl_ca"`
	SSLCert               *string  `yaml:"ssl_cert"`
	SSLKey                *string  `yaml:"ssl_key"`
	ClientMode            *string  `yaml:"client_mode"`
	Docker                struct {
		Image      *string `yaml:"image"`
		Binary     *string `yaml:"binary"`
		Network    *string `yaml:"network"`
		PullPolicy *string `yaml:"pull_policy"`
	} `yaml:"docker"`
}

type mysqlBackupOptions struct {
	host, user, passwordEnv, directory, dump, client           string
	sslMode, sslCA, sslCert, sslKey                            string
	port                                                       int
	databases                                                  []string
	gzip, routines, events, triggers, allowNonTransactional    bool
	clientMode, image, dockerBinary, dockerNetwork, pullPolicy string
}

func newMySQLBackupCommand(parent context.Context, out, diag io.Writer) (*cobra.Command, error) {
	var defaults fileConfig
	if err := yamlClusterDefaults(&defaults); err != nil {
		return nil, err
	}
	defaultImage := ""
	if defaults.MySQL.Docker.Image != nil {
		defaultImage = *defaults.MySQL.Docker.Image
	}
	cmd := &cobra.Command{Use: "backup", Long: `使用官方 mysql 客户端预检，调用 mysqldump 流式导出指定数据库。
--client-mode local 使用本地 mysql/mysqldump；docker 使用 MySQL 镜像内客户端，无需本地安装。
镜像须匹配服务端版本，配置 mysql.docker.image 或 --image；Docker 模式需要本机可用的 Linux Docker 引擎。
Docker 默认 bridge 网络，连接宿主机需显式选择可达地址或合适网络；不启动 MySQL 服务。
--database 可重复，不指定时读取 mysql.databases；不隐式备份全部实例。
默认 gzip、single-transaction、quick，包含 routines/events/triggers；GTID_PURGED=OFF。
默认拒绝非 InnoDB 表；允许后也不保证非事务表或并发 DDL 下的一致性。
密码从 --password-env 指定的环境变量读取，临时客户端配置受限权限并在结束后清理。
成功保存 backup.sql[.gz] 和 manifest.json；失败保留 .partial，上传失败保留本地备份。
使用顶层 s3 配置可自动上传；HEAD 元数据校验不代表恢复成功。`}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置")
	var o mysqlBackupOptions
	f.StringVar(&o.host, "host", "127.0.0.1", "MySQL TCP 主机；mysql.host")
	f.IntVar(&o.port, "port", 3306, "MySQL TCP 端口；mysql.port")
	f.StringVar(&o.user, "user", "backup", "备份账号；mysql.user")
	f.StringVar(&o.passwordEnv, "password-env", "MYSQL_BACKUP_PASSWORD", "密码环境变量名；空值表示显式无密码；mysql.password_env")
	f.StringArrayVar(&o.databases, "database", nil, "目标数据库，可重复；mysql.databases")
	f.StringVar(&o.directory, "directory", "data/backups/mysql", "本地备份根目录；mysql.directory")
	f.StringVar(&o.dump, "mysqldump", "mysqldump", "官方 mysqldump 可执行文件")
	f.StringVar(&o.client, "mysql-client", "mysql", "用于数据库及表引擎预检的官方 mysql 客户端")
	f.StringVar(&o.clientMode, "client-mode", "local", "local / docker；默认保留本地客户端方式")
	f.StringVar(&o.image, "image", defaultImage, "MySQL 客户端镜像；mysql.docker.image，建议固定匹配版本或 digest")
	f.StringVar(&o.dockerBinary, "docker-bin", "docker", "Docker CLI；mysql.docker.binary")
	f.StringVar(&o.dockerNetwork, "docker-network", "bridge", "Docker 网络：bridge、host 或已有网络名称；mysql.docker.network")
	f.StringVar(&o.pullPolicy, "pull-policy", "missing", "镜像拉取策略：missing / always / never；mysql.docker.pull_policy")
	f.BoolVar(&o.gzip, "gzip", true, "gzip 压缩 SQL；mysql.gzip")
	f.BoolVar(&o.routines, "routines", true, "包含存储过程和函数")
	f.BoolVar(&o.events, "events", true, "包含事件")
	f.BoolVar(&o.triggers, "triggers", true, "包含触发器")
	f.BoolVar(&o.allowNonTransactional, "allow-non-transactional", false, "显式接受非 InnoDB 表的一致性限制")
	f.StringVar(&o.sslMode, "ssl-mode", "REQUIRED", "DISABLED / REQUIRED / VERIFY_CA / VERIFY_IDENTITY；默认要求 TLS")
	f.StringVar(&o.sslCA, "ssl-ca", "", "MySQL TLS CA 文件")
	f.StringVar(&o.sslCert, "ssl-cert", "", "MySQL TLS 客户端证书")
	f.StringVar(&o.sslKey, "ssl-key", "", "MySQL TLS 客户端私钥")
	var s storage
	s.flags(f)
	timeout := f.Duration("timeout", time.Hour, "预检、导出、校验及上传共用的总超时")
	dry := f.Bool("dry-run", false, "仅输出计划，不读取密码、不调用客户端、不创建文件或上传")
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 {
			return 2, errors.New("mysql backup 不接受位置参数；使用 --database 指定目标数据库")
		}
		if err := loadConfig(*config, f.Changed("config"), f, &s); err != nil {
			return 2, err
		}
		if *timeout <= 0 {
			return 2, errors.New("timeout 必须大于零")
		}
		if err := o.validate(); err != nil {
			return 2, err
		}
		if err := validateMySQLStorage(s); err != nil {
			return 2, err
		}
		if *dry {
			return 0, json.NewEncoder(out).Encode(struct {
				Host       string   `json:"host"`
				Port       int      `json:"port"`
				Databases  []string `json:"databases"`
				Directory  string   `json:"directory"`
				Gzip       bool     `json:"gzip"`
				Upload     bool     `json:"upload_requested"`
				ClientMode string   `json:"client_mode"`
				Image      string   `json:"image,omitempty"`
				Network    string   `json:"docker_network,omitempty"`
			}{o.host, o.port, o.databases, o.directory, o.gzip, s.bucket != "", o.clientMode, mysqlDockerPlanValue(o, o.image), mysqlDockerPlanValue(o, o.dockerNetwork)})
		}
		binaries := []string{o.client, o.dump}
		if o.clientMode == "docker" {
			binaries = []string{o.dockerBinary}
		}
		for _, binary := range binaries {
			if _, err := exec.LookPath(binary); err != nil {
				if o.clientMode == "docker" {
					return 3, errors.New("缺少 Docker CLI；请安装 Docker 或指定 --docker-bin")
				}
				return 3, errors.New("缺少 MySQL 客户端依赖；请安装 mysql 和 mysqldump，或指定 --mysql-client / --mysqldump")
			}
		}
		password := ""
		if o.passwordEnv != "" {
			var ok bool
			password, ok = os.LookupEnv(o.passwordEnv)
			if !ok {
				return 2, errors.New("未设置指定的 MySQL 密码环境变量")
			}
		}
		if strings.ContainsRune(password, 0) {
			return 2, errors.New("MySQL 密码不能包含 NUL")
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		return backupMySQL(ctx, o, s, password, out, diag)
	})
	return cmd, nil
}

func (o mysqlBackupOptions) validate() error {
	if err := o.validateClientMode(); err != nil {
		return err
	}
	if o.port < 1 || o.port > 65535 || strings.TrimSpace(o.host) == "" || strings.ContainsAny(o.host, "\r\n\x00/\\") || o.user == "" || strings.ContainsAny(o.user, "\r\n\x00") || o.directory == "" {
		return errors.New("MySQL host、port、user 或 directory 无效")
	}
	if len(o.databases) == 0 {
		return errors.New("必须指定 --database 或 mysql.databases，不隐式备份全部数据库")
	}
	seen := map[string]bool{}
	for _, db := range o.databases {
		if strings.TrimSpace(db) == "" || strings.HasPrefix(db, "-") || strings.ContainsFunc(db, unicode.IsControl) || seen[db] {
			return errors.New("数据库名为空、重复、含控制字符或以 '-' 开头")
		}
		seen[db] = true
	}
	switch o.sslMode {
	case "DISABLED", "REQUIRED", "VERIFY_CA", "VERIFY_IDENTITY":
	default:
		return errors.New("ssl-mode 必须为 DISABLED / REQUIRED / VERIFY_CA / VERIFY_IDENTITY")
	}
	if (o.sslCert == "") != (o.sslKey == "") {
		return errors.New("ssl-cert 与 ssl-key 必须同时提供")
	}
	if (o.sslMode == "VERIFY_CA" || o.sslMode == "VERIFY_IDENTITY") && o.sslCA == "" {
		return errors.New("验证 MySQL TLS 证书时必须指定 ssl-ca")
	}
	if o.sslMode == "DISABLED" && (o.sslCA != "" || o.sslCert != "") {
		return errors.New("禁用 TLS 时不能指定 MySQL TLS 证书")
	}
	return nil
}

func validateMySQLStorage(s storage) error {
	if (s.key != "" || s.endpoint != "") && s.bucket == "" {
		return errors.New("S3 参数需要 bucket")
	}
	if s.bucket != "" {
		if (s.provider == "minio" || s.provider == "custom") && s.endpoint == "" {
			return errors.New("所选 S3 厂家需要 endpoint")
		}
		if (s.accessKey == "") != (s.secretKey == "") || (s.sessionToken != "" && s.accessKey == "") {
			return errors.New("S3 凭据需同时提供 access_key 和 secret_key")
		}
	}
	return nil
}

func backupMySQL(ctx context.Context, o mysqlBackupOptions, s storage, password string, out, diag io.Writer) (int, error) {
	root, err := filepath.Abs(o.directory)
	if err != nil {
		return 1, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return 1, err
	}
	task, err := os.MkdirTemp(root, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return 1, err
	}
	if err := restrictMySQLDirectory(task); err != nil {
		return 1, err
	}
	name := "backup.sql"
	if o.gzip {
		name += ".gz"
	}
	dest := filepath.Join(task, name)
	m, err := exportMySQL(ctx, o, password, task, dest+".partial", diag)
	if err != nil {
		failure := fmt.Errorf("MySQL 备份失败；任务文件保留在 %s: %w", task, err)
		if ctx.Err() != nil {
			return 4, failure
		}
		return 1, failure
	}
	if err := ctx.Err(); err != nil {
		return 4, err
	}
	if err := os.Rename(dest+".partial", dest); err != nil {
		return 1, err
	}
	m.File = name
	m.Upload = "not-requested"
	if s.bucket != "" {
		if s.key == "" {
			s.key = "mysql/" + filepath.Base(task) + "/" + name
		}
		m.Upload, m.Key = "pending", s.key
	}
	manifest := filepath.Join(task, "manifest.json")
	if err := writeManifest(manifest, m); err != nil {
		return 1, err
	}
	if s.bucket != "" {
		if err := s.upload(ctx, dest, m.SHA256); err != nil {
			m.Upload = "failed"
			if err := writeManifest(manifest, m); err != nil {
				return 5, fmt.Errorf("本地 MySQL 备份保留在 %s；上传及清单更新失败", dest)
			}
			return 5, fmt.Errorf("本地 MySQL 备份保留在 %s；S3 上传或远端校验失败，请检查目标对象、凭据、权限及连接", dest)
		}
		m.Upload = "verified-metadata"
		if err := writeManifest(manifest, m); err != nil {
			return 5, err
		}
		s.key += ".manifest.json"
		mm, err := inspect(manifest)
		if err != nil {
			return 5, err
		}
		if err := s.upload(ctx, manifest, mm.SHA256); err != nil {
			return 5, fmt.Errorf("本地 MySQL 备份保留在 %s；数据对象已通过 metadata 校验，远端清单上传或校验失败", dest)
		}
	}
	fmt.Fprintln(diag, "Local MySQL backup:", dest)
	return 0, json.NewEncoder(out).Encode(m)
}

func mysqlOptionQuote(value string) string {
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t", "\b", "\\b")
	return "\"" + r.Replace(value) + "\""
}

func exportMySQL(ctx context.Context, o mysqlBackupOptions, password, task, partial string, diag io.Writer) (manifest Manifest, err error) {
	auth, err := os.MkdirTemp(task, ".mysql-auth-")
	if err != nil {
		return manifest, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(auth); cleanupErr != nil {
			err = errors.Join(err, errors.New("无法清理本任务临时 MySQL 凭据"))
		}
	}()
	if err := restrictMySQLDirectory(auth); err != nil {
		return manifest, err
	}
	credentials := filepath.Join(auth, "client.cnf")
	if err := os.WriteFile(credentials, []byte("[client]\npassword="+mysqlOptionQuote(password)+"\n"), 0600); err != nil {
		return manifest, err
	}
	if o.clientMode == "docker" {
		credentials = "/opskit-client/client.cnf"
		for _, cert := range []struct {
			value *string
			name  string
		}{{&o.sslCA, "ca.pem"}, {&o.sslCert, "cert.pem"}, {&o.sslKey, "key.pem"}} {
			if *cert.value == "" {
				continue
			}
			data, err := os.ReadFile(*cert.value)
			if err != nil {
				return manifest, errors.New("读取 MySQL 容器 TLS 文件失败")
			}
			if err := os.WriteFile(filepath.Join(auth, cert.name), data, 0600); err != nil {
				return manifest, err
			}
			*cert.value = "/opskit-client/" + cert.name
		}
	}
	base := []string{"--defaults-file=" + credentials, "--protocol=TCP", "--host=" + o.host, "--port=" + strconv.Itoa(o.port), "--user=" + o.user, "--default-character-set=utf8mb4", "--ssl-mode=" + o.sslMode}
	for _, setting := range [][2]string{{"ssl-ca", o.sslCA}, {"ssl-cert", o.sslCert}, {"ssl-key", o.sslKey}} {
		if setting[1] != "" {
			base = append(base, "--"+setting[0]+"="+setting[1])
		}
	}
	env := []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.EqualFold(key, "MYSQL_PWD") || strings.EqualFold(key, "MYSQL_TEST_LOGIN_FILE") || (o.passwordEnv != "" && strings.EqualFold(key, o.passwordEnv)) {
			continue
		}
		env = append(env, value)
	}
	// Prevent user login-path files from overriding the task's explicit credentials (also MySQL 8.0).
	env = append(env, "MYSQL_TEST_LOGIN_FILE="+filepath.Join(auth, "unused.login"))
	var names []string
	for _, db := range o.databases {
		names = append(names, "CONVERT(0x"+hex.EncodeToString([]byte(db))+" USING utf8mb4)")
	}
	list := strings.Join(names, ",")
	query := "SELECT (SELECT COUNT(*) FROM information_schema.schemata WHERE SCHEMA_NAME IN (" + list + ")), (SELECT COUNT(*) FROM information_schema.tables WHERE TABLE_SCHEMA IN (" + list + ") AND TABLE_TYPE='BASE TABLE' AND ENGINE <> 'InnoDB')"
	var report bytes.Buffer
	if err := runMySQLClient(ctx, o, "mysql", append(append([]string{}, base...), "--batch", "--skip-column-names", "--raw", "--execute="+query), env, auth, &report); err != nil {
		return manifest, mysqlClientFailure("MySQL 数据库/表引擎预检失败；请检查客户端或 Docker/镜像、网络、TLS 和账号权限", err)
	}
	fields := strings.Fields(report.String())
	if len(fields) != 2 {
		return manifest, errors.New("MySQL 预检结果无效")
	}
	schemas, e1 := strconv.Atoi(fields[0])
	nonTransactional, e2 := strconv.Atoi(fields[1])
	if e1 != nil || e2 != nil || schemas != len(o.databases) || nonTransactional < 0 {
		return manifest, errors.New("目标数据库不存在、不可见或预检结果无效")
	}
	if nonTransactional > 0 {
		if !o.allowNonTransactional {
			return manifest, errors.New("存在非 InnoDB 表；需要一致性方案，或显式 --allow-non-transactional 接受限制")
		}
		fmt.Fprintln(diag, "警告：目标含非 InnoDB 表，事务模式不保证这些表的一致性。")
	}
	args := append(append([]string{}, base...), "--single-transaction", "--quick", "--skip-lock-tables", "--no-tablespaces", "--set-gtid-purged=OFF", "--hex-blob", "--routines="+strconv.FormatBool(o.routines), "--events="+strconv.FormatBool(o.events), "--triggers="+strconv.FormatBool(o.triggers), "--databases")
	args = append(args, o.databases...)
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return manifest, err
	}
	defer f.Close()
	hash := sha256.New()
	var writer io.Writer = io.MultiWriter(f, hash)
	var compressed *gzip.Writer
	if o.gzip {
		compressed = gzip.NewWriter(writer)
		writer = compressed
	}
	counted := &mysqlDumpWriter{Writer: writer}
	runErr := runMySQLClient(ctx, o, "mysqldump", args, env, auth, counted)
	var zipErr error
	if compressed != nil {
		zipErr = compressed.Close()
	}
	if runErr != nil {
		return manifest, mysqlClientFailure("mysqldump 导出失败（部分文件保留）；请检查数据库权限、客户端或 Docker/镜像和连接", runErr)
	}
	if zipErr != nil {
		return manifest, errors.New("压缩或写入 MySQL 备份失败")
	}
	if counted.size == 0 {
		return manifest, errors.New("mysqldump 输出为空，未发布备份")
	}
	if err := f.Sync(); err != nil {
		return manifest, err
	}
	info, err := f.Stat()
	if err != nil {
		return manifest, err
	}
	if err := f.Close(); err != nil {
		return manifest, err
	}
	return Manifest{SchemaVersion: 1, Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)), Created: time.Now().UTC()}, nil
}

type mysqlDumpWriter struct {
	io.Writer
	size int64
}

func (w *mysqlDumpWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.size += int64(n)
	return n, err
}
