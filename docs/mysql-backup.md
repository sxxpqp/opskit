# MySQL 逻辑备份

v0.1.15 提供 `opskit mysql backup`。保留本地官方 `mysql` 与 `mysqldump` 方式，也支持在 MySQL Docker 镜像内运行客户端，免装本地数据库客户端。以单库或多库为单位流式导出，不包含物理/增量备份、binlog 归档、复制拓扑恢复或自动清理。

## 快速使用

1. 本地方式安装匹配服务端的 MySQL 客户端，Docker 方式只需可用的本机 Linux Docker 引擎和匹配版本的镜像。已验收 Windows Docker Desktop + MySQL 8.4.13 的指定测试数据；8.0、本地客户端及其他环境未做真实验收，不承诺 MariaDB 兼容。
2. 执行 `opskit config init`，编辑生成配置中的 `mysql` 字段，设置主机、账号、目标库及 TLS。
3. 在当前终端安全设置 `MYSQL_BACKUP_PASSWORD`；建议从秘密管理工具注入，避免把真实密码写入 Shell 历史或仓库。

Linux/macOS 交互设置示例（Bash）：

```sh
read -r -s -p 'MySQL password: ' MYSQL_BACKUP_PASSWORD
export MYSQL_BACKUP_PASSWORD
printf '\n'
opskit mysql backup --database app --dry-run
opskit mysql backup --database app
unset MYSQL_BACKUP_PASSWORD
```

PowerShell 可在进程内设置环境变量后运行同样命令，例如使用 `Read-Host -MaskInput`（PowerShell 7）读取密码并赋给 `$env:MYSQL_BACKUP_PASSWORD`，结束后移除该变量。环境变量属于当前进程秘密，仍应保护运行账号。

```sh
# 多库；CLI 数据库列表整体覆盖 YAML mysql.databases
opskit mysql backup --database app --database logs
# 不压缩
opskit mysql backup --database app --gzip=false
# 自定义客户端和备份目录
opskit mysql backup --database app --mysql-client /opt/mysql/bin/mysql \
  --mysqldump /opt/mysql/bin/mysqldump --directory ./backups/mysql
```

`--dry-run` 只验证配置并输出计划 JSON（包含执行模式和 Docker 目标镜像/网络），不读密码、不检查可执行文件、不连接数据库、不创建目录或上传。本地方式缺少任一数据库客户端会返回依赖错误，Docker 方式只检查 Docker CLI。密码变量未设置会报配置错误；允许变量值为空，或用 `password_env: ""` / `--password-env ""` 明确选择无密码。

## 使用 MySQL 镜像

```sh
# 默认镜像读取 mysql.docker.image；无需本地 mysql 或 mysqldump
opskit mysql backup --client-mode docker --host mysql.example.com --database app --dry-run
opskit mysql backup --client-mode docker --host mysql.example.com --database app
# 按目标服务器选择客户端版本；也可以填私有仓库路径或 digest
opskit mysql backup --client-mode docker --image mysql:8.0 --host mysql.example.com --database app
# Linux 宿主机数据库：显式 host 网络
opskit mysql backup --client-mode docker --docker-network host --host 127.0.0.1 --database app
# Docker Desktop 宿主机数据库
opskit mysql backup --client-mode docker --host host.docker.internal --database app
# 数据库在已有 Docker 网络中
opskit mysql backup --client-mode docker --docker-network database-net --host mysql-server --database app
```

镜像默认只定义在模板 `mysql.docker.image`，CLI `--image` 优先；应选择匹配服务端的版本，生产环境可固定 tag/digest。私有仓库需预先 `docker login`，镜像代理/仓库地址直接写在该配置项。`--pull-policy missing|always|never` 控制拉取；首次拉取需要网络，整个过程受 `--timeout` 约束。默认 local 不变，不会自动切换模式。

预检和导出各使用一个独立临时容器，不启动数据库服务、不分配 TTY、不通过容器环境变量传密码。只读挂载本任务临时认证目录，其中含客户端配置及需要的 TLS 文件；固定启动脚本复制这些文件到 `/opskit-client` 临时 tmpfs，设置 0600 后通过 `exec "$@"` 执行 mysql 或 mysqldump。用户输入仍按独立 argv 转发，不插入 shell 脚本文本。镜像必须包含 mysql/mysqldump、`/bin/sh`、`cp` 和 `chmod`。

容器内重新设置权限是必要的：Windows 上的私有 ACL 不保证 bind 文件在 Linux 中呈现为私有权限，MySQL 会忽略它认定为可被其他用户写入的配置文件；不能只依赖只读挂载。tmpfs 目录为 0700，内部文件为 0600，随容器移除；宿主机源文件仍保持受限 ACL。备份 SQL 通过 stdout 回到 opskit，在本机压缩、校验和上传，不挂载数据库数据目录或整个备份目录。临时容器以 root 运行，以读取主机 0700/0600 文件，容器根文件系统只读；只应使用可信镜像。

默认 bridge 下 localhost 指向客户端容器，工具会拒绝该误配。Docker Desktop 可用 `host.docker.internal`；Linux 宿主机可显式使用 host 网络；已有自定义网络需自行创建。host 模式的 Docker Desktop 支持取决于引擎设置，不自动开启。只支持能够访问本机挂载路径的本地引擎；远程 Docker context/daemon、特殊 user namespace/SELinux 挂载策略未验收。Docker CLI 可通过 `--docker-bin` 或 `mysql.docker.binary` 指定。网络与挂载行为见 [Docker run](https://docs.docker.com/reference/cli/docker/container/run/)、[bind mounts](https://docs.docker.com/engine/storage/bind-mounts/) 和 [官方 MySQL 镜像](https://hub.docker.com/_/mysql)。

正常退出由 `--rm` 删除容器；启动/导出失败或取消后额外按本任务唯一容器名执行清理。杀掉 Docker CLI 不代表容器已经停止，清理最多额外等待 15 秒；若 Docker 引擎不可达、无法确认容器已删除，命令会明确报告该容器名供排查。清理不会操作其他已有容器。强制杀死 opskit/掉电仍可能留下临时容器与受限凭据，需要人工检查。

## 配置与选项

完整模板见 [config.example.yaml](../config.example.yaml)，字段与路径规则见[配置指南](configuration.md#mysql-逻辑备份)。默认 TCP 连接 `127.0.0.1:3306`，不从本地 socket、用户 MySQL 配置或 login-path 推断连接目标。

| YAML | CLI | 默认/说明 |
|---|---|---|
| `mysql.client_mode` | `--client-mode` | local / docker，默认 local |
| `mysql.docker.image` | `--image` | 默认见嵌入模板；支持自定义仓库和 digest |
| `mysql.docker.binary` | `--docker-bin` | Docker CLI，默认 docker |
| `mysql.docker.network` | `--docker-network` | 默认 bridge，也可选 host 或已有网络 |
| `mysql.docker.pull_policy` | `--pull-policy` | missing / always / never，默认 missing |
| `mysql.host` / `port` / `user` | `--host` / `--port` / `--user` | `127.0.0.1` / `3306` / `backup` |
| `mysql.password_env` | `--password-env` | `MYSQL_BACKUP_PASSWORD`，是变量名 |
| `mysql.databases` | 重复 `--database` | 必须指定目标，不隐式全实例导出 |
| `mysql.directory` | `--directory` | 模板 `../data/backups/mysql`；未加载配置时 `data/backups/mysql` |
| `mysql.mysql_client` / `mysqldump` | `--mysql-client` / `--mysqldump` | 从 PATH 查找 mysql / mysqldump |
| `mysql.gzip` | `--gzip=false` | 默认 gzip |
| `mysql.routines` / `events` / `triggers` | 同名 `--xxx=false` | 默认包含存储过程/函数、事件和触发器 |
| `mysql.allow_non_transactional` | `--allow-non-transactional` | 默认 false，非 InnoDB 表会被拒绝 |
| `mysql.ssl_mode` | `--ssl-mode` | REQUIRED；也支持 DISABLED、VERIFY_CA、VERIFY_IDENTITY |
| `mysql.ssl_ca` / `ssl_cert` / `ssl_key` | `--ssl-ca` / `--ssl-cert` / `--ssl-key` | 验证模式要求 CA，客户端 cert/key 成对配置 |
| 顶层 `timeout` | `--timeout` | 默认 1h，含预检、导出、压缩、上传和远端校验 |
| 顶层 `s3` | `--bucket` / `--key` 等 | 沿用现有厂家配置，空 bucket 仅本地 |

REQUIRED 要求 TLS 加密但不验证服务端身份；生产环境可配置 VERIFY_IDENTITY + CA。工具不会在连接失败时降级为明文。数据库名不能为空、重复、含控制字符或以 `-` 开头，不提供任意 mysqldump 额外参数透传。

## 导出与一致性边界

预检通过 `mysql` 查询所选数据库的可见性及表引擎，不修改业务数据。数据库不可见或不存在时停止，默认拒绝非 InnoDB 基表。预检不是完整权限审计，实际导出所需权限由 mysqldump 验证；需要结合所选视图、触发器、过程和事件准备账号权限，不能把能登录或 metadata 可见当作能完整导出。

导出使用 `--single-transaction --quick --skip-lock-tables --no-tablespaces --set-gtid-purged=OFF --hex-blob --databases ...`。事务模式只为事务表提供相应一致性；备份期间避免并发 DDL。即使预检通过，表引擎或对象也可能随后变化，因此仍需安排备份窗口。显式允许非 InnoDB 表仅代表接受限制，不会自动加全局锁或把它们变成一致性备份。GTID 信息不用于复制恢复。相关行为见 [MySQL 8.0 mysqldump](https://dev.mysql.com/doc/refman/8.0/en/mysqldump.html) 和 [MySQL 8.4 mysqldump](https://dev.mysql.com/doc/refman/8.4/en/mysqldump.html)。

## 产物与凭据

```text
<directory>/<UTC时间-随机任务标识>/
  backup.sql.gz       # --gzip=false 时为 backup.sql
  manifest.json
```

stdout 成功输出 v1 JSON 清单，诊断进 stderr；清单记录文件名、最终文件大小、SHA-256、创建时间、上传状态及可选 remote_key。摘要对应最终存储文件的字节，压缩时为 gzip 文件的摘要。内容为 SQL 导出，可能包含业务秘密，应按生产备份管理。

先写入 `.partial`，客户端成功、SQL 非空、压缩及文件写入完成后才重命名并发布本地清单。失败导出保留部分文件，预检阶段失败可能只留下空任务目录；gzip 非空头不会被误判为非空 SQL。没有有效本地文件时不会上传。

任务目录在 Unix 设为 0700、文件为 0600；Windows 设置受保护 DACL，只授予当前用户和 SYSTEM，并让文件继承。无法设置权限时在写入秘密前失败。密码通过临时受限的 `[client]` 配置与首参数 `--defaults-file` 传入，不放进 argv 或 manifest；子进程移除密码来源环境变量及 MYSQL_PWD，并将 login 文件指向本任务不存在的文件以免用户 login-path 覆盖。配置文件语法及 login 文件行为见 [MySQL option files](https://dev.mysql.com/doc/refman/8.0/en/option-files.html) 与 [MySQL 环境变量](https://dev.mysql.com/doc/refman/8.0/en/environment-variables.html)。

成功、失败和可处理的取消均清理本任务临时凭据；若清理失败会报错且不发布备份。进程遭强制终止、掉电时无法执行清理，可能残留受限的 `.mysql-auth-*`，需在确认任务已结束后处理。客户端原始 stderr 不转发，以免暴露秘密；失败信息提示检查阶段、TLS、版本和权限。

## S3 与恢复验证

填写顶层所选厂家的 bucket 后自动上传，默认对象为 `mysql/<任务>/backup.sql[.gz]`，远端清单为 `<key>.manifest.json`。默认条件写入禁止覆盖，上传后通过 HEAD 比较长度与 SHA-256 metadata；数据或清单上传失败返回非零，有效本地文件始终保留。`upload_status: verified-metadata` 仅描述数据对象，远端清单失败仍返回非零，不能只看本地该字段判断整个远端任务完成。HEAD 不等于回读验证或恢复证明。

恢复只在隔离测试实例中演练。导出带有数据库名称及建库语句，可能写入同名数据库，不应导入生产实例。例如确认目标是专用临时 MySQL 后：

```sh
gzip -dc /path/to/backup.sql.gz > /tmp/restore-test.sql
mysql --host=127.0.0.1 --port=13306 --user=restore --password < /tmp/restore-test.sql
```

这里 `--password` 不带值，由官方客户端交互询问。请使用与测试实例匹配的 TLS 参数；导入后核对表、行数、关键数据及所需对象。工具不自动执行恢复。Windows PowerShell 管道可能转码 SQL 文本；验收脚本通过 `docker cp` 传入原始 UTF-8 文件后在容器内导入，避免把中文变成问号。

退出码：0 成功；2 参数/配置无效；3 缺少客户端；4 本地预检/导出阶段超时或取消；1 本地导出/校验/文件失败；5 本地已完成但远端阶段失败（包含远端阶段超时）。

已用模拟 mysql/mysqldump、Docker 与 HTTP S3 服务覆盖 SQL/gzip、摘要、空输出、中断、数据库可见性、非事务表、配置隔离、密码处理、镜像/网络/只读认证挂载、TLS 路径转换、容器清理及上传失败保留。真实 Windows Docker Desktop + MySQL 8.4.13 已完成 gzip/SQL、多库、中文/二进制/金额、对象恢复行为、正确/错误 CA 及非 InnoDB 拒绝验收，见[完整记录与复跑方法](mysql-validation-2026-10-22.md)。MySQL 8.0、本地客户端、其他宿主机系统、MariaDB、真实 S3/MinIO 及生产恢复仍未验收。
