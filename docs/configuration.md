# 统一配置与生产模板

main 分支默认读取 `config/config.yaml`，通过 `opskit config init` 创建；正式版 v0.1.5 仍使用 `opskit.yaml`。默认文件不存在时回退读取旧 `./opskit.yaml`，两份文件不会合并；显式 `--config FILE` 不回退。各相对路径始终基于实际配置文件所在目录，因此迁移旧配置时请核对相对路径。

版本按安装类型隔离：Go SDK 使用 `go.version`，kind 使用 `kind.version` / `kind.kubernetes.version`，K8s 使用 `k8s.kubernetes.version` 自动选择固定组件版本，组件 version 为可选覆盖；镜像源集中在 `mirrors.docker` / `containerd` / `go` / `npm` / `pip` / `maven`。安装或创建命令的 `--version` 覆盖对应版本项。kind 名称、worker 数量、kubeconfig、工具路径和映射端口放在 `kind` 下；KS 存储和初始密码放在 `kind.kubesphere` / `k8s.kubesphere` 下。完整带注释模板见 `config.example.yaml`。

这些命令写入持久配置文件，不修改当前终端的环境变量；Go/pip/npm 等工具自身的 GOPROXY、PIP_INDEX_URL、NPM_CONFIG_REGISTRY 环境变量仍可能优先于文件，使用前请核对。

`mirror` 命令默认写入并保留备份，`--dry-run` 仅预览；Linux 标准 `/etc/docker/daemon.json` 写入后自动重启并检查 Docker 服务，自定义文件和宿主机 containerd 仍需手动重启。模板镜像源已按 linux 项目原配置填充：Docker 多前端、containerd huball 和现有公共语言源。部署到其他环境时需替换地址；模板更新不会覆盖本地已生成配置。containerd 主机配置识别 1.x 的 CRI registry 与 2.x 的 images registry，未知结构或旧认证/TLS 配置会停止；Go/npm/pip/Maven 只修改本用户对应配置文件，支持 `--file`。保留已有 Maven 自定义 mirrors，需人工合并；不自动关闭证书校验。

kind、mirror、KS 的显式参数优先于对应环境变量，其次 YAML 和固定默认值；etcd/S3 保留已有参数及 AWS 默认凭据链语义。非空 YAML 账号信息仍按原规则优先于 AWS 环境凭据。请勿把全局的配置优先级理解成秘密管理机制。


顶层 `opskit --help` 和各命令组帮助由 Cobra 从实际命令树生成，分行显示命令与中文说明。帮助文字中的 `|` 表示任选一个子命令，实际执行使用空格，例如 `opskit etcd check`。`k8s create` 的集群名由 YAML `k8s.name` 指定，例如 `k8s: {name: sxxpqp}`（还需填写完整节点等配置），不接受 `k8s create sxxpqp`。缺少配置时执行 `opskit config init` 并编辑模板，创建前使用 `opskit k8s create --config config/config.yaml --dry-run` 检查。

完整字段说明在 [config.example.yaml](../config.example.yaml)。以下示例均使用当前实现支持的字段。

## 分步下载、检查与手动 kubeadm

完整命令、下载目录结构和手动安装步骤见[手动 kubeadm 指南](manual-kubeadm.md)。这些命令与 `k8s create` 独立：

- `k8s download`：版本读取 `k8s.kubernetes.version` 及已有组件版本字段，默认下载 kubeadm/kubelet/kubectl/crictl；`--components` 选择其他组件，`--arch` 选择 Linux amd64/arm64，不校验 SSH hosts 或读取节点密码。输出根目录为 `--download-dir` > `install.download_dir` > `~/.opskit/downloads`，YAML 相对路径基于配置文件目录。
- etcd 包下载源为 `install.etcd.download_base`，优先级为 `--etcd-download-base` > `ETCD_DOWNLOAD_BASE` > YAML > 嵌入模板。版本必须设置 `--etcd-version` 或 `k8s.etcd.version`，不从 Kubernetes 次版本推断，不改变 kubeadm 的 etcd 镜像。不提供 `etcd install` 或外置 etcd 部署。
- `calico install`：版本和 Pod CIDR 读取 `k8s.network.calico.version`、`k8s.network.kubePodsCIDR`，可用 `--version` / `--pod-subnet` 覆盖；本地清单为 `--manifest` > `k8s.network.manifest`。主源优先级为 `--calico-download-base` > `CALICO_DOWNLOAD_BASE` > `install.calico.download_base` > 模板，备用源及摘要沿用 `install.calico`。本地清单校验版本与显式 Pod CIDR，保留原内容。
- `k8s check`：读取完整 `k8s` 部署配置，通过 SSH/root/sudo 只读检查目标，缺依赖返回非零；不下载安装，不验证节点间端口连通性、可用磁盘阈值、镜像可拉取性或完整部署归属。
- `k8s status` 和 `calico install` 使用 `--kubeconfig` / `--context` 选择集群，未指定时交给 kubectl 读取 `KUBECONFIG` 或 `~/.kube/config`。不会用 SSH hosts 或 kind 配置隐式选择集群。opskit 保存的独立 kubeconfig 需显式传入。`status` 的退出结果依据节点 Ready 和查询错误，系统 Pod 表仅用于诊断。

```yaml
schema_version: 1
install:
  download_dir: ./downloads
  etcd:
    download_base: https://github.com
k8s:
  kubernetes:
    version: v1.24.7
  etcd:
    version: v3.5.5  # 历史下载示例，按目标环境选择版本
  network:
    kubePodsCIDR: 10.244.0.0/16
    calico:
      version: v3.26.5
```

版本示例不表示新生产环境推荐。下载来源与各组件环境变量对照表见[手动指南](manual-kubeadm.md#下载源与优先级)。默认源只维护在嵌入的 `config.example.yaml`；新增 etcd 配置不改变已有 etcd 备份连接字段。

## MySQL 逻辑备份

完整步骤与边界见 [MySQL 备份指南](mysql-backup.md)。新增 `mysql` 配置只影响 `mysql backup`，不会借用或覆盖 `etcd` 的目录、证书或客户端。

```yaml
schema_version: 1
timeout: 1h
mysql:
  client_mode: local  # local / docker；保留本地方式
  docker:
    image: mysql:8.4  # 按服务端选择固定 tag/digest，支持私有仓库或代理
    binary: docker
    network: bridge
    pull_policy: missing  # missing / always / never
  host: mysql.example.com
  port: 3306
  user: backup
  password_env: MYSQL_BACKUP_PASSWORD
  databases: [app]
  directory: ./backups/mysql
  mysqldump: mysqldump
  mysql_client: mysql
  gzip: true
  routines: true
  events: true
  triggers: true
  allow_non_transactional: false
  ssl_mode: VERIFY_IDENTITY
  ssl_ca: ./certs/mysql-ca.pem
  ssl_cert: ""
  ssl_key: ""
```

CLI 参数优先于 YAML；`--database` 可重复且整体替换 YAML 数据库列表，不能留空或隐式全实例备份。密码只从 `password_env` 指定的环境变量读取，不支持明文 `--password` 或 YAML `password`。空 `password_env` 明确使用无密码认证。`mysql` 客户端、输出目录及 TLS 文件的 YAML 相对路径基于配置文件目录；裸客户端名称从 PATH 查找；CLI 相对路径基于当前目录。

`client_mode` 默认 local，`--client-mode docker` 改用镜像内的 mysql/mysqldump，只检查 Docker CLI 依赖。镜像源优先级为 `--image` > `mysql.docker.image` > 嵌入模板；默认镜像只维护在 `config.example.yaml`。`--docker-bin`、`--docker-network`、`--pull-policy` 覆盖 `mysql.docker.binary/network/pull_policy`，没有组件环境变量别名。Docker CLI 的 YAML 相对路径同样按配置文件解析，本地客户端路径在 Docker 模式不使用。

Docker 方式要求本机可用的 Linux Docker 引擎及可挂载的本地目录。`network: bridge` 不允许把 localhost/127.0.0.1 误当成宿主机，需设置远程地址、Docker Desktop 的 `host.docker.internal`，或在 Linux 上显式 `network: host`；也支持已存在的自定义网络名称。镜像拉取受总超时约束，策略 missing/always/never 分别对应缺少时拉取、每次检查拉取、不拉取。登录私有仓库应预先使用 Docker 的凭据管理，不在镜像 URL 中放密码。

镜像需包含 mysql/mysqldump、`/bin/sh`、`cp`、`chmod`。容器固定启动脚本将只读挂载的密码及 TLS 文件复制到临时 tmpfs，并设为 0600 后执行客户端；用户参数通过独立 argv 转发，不拼入脚本。这样可避免 Windows Docker Desktop 挂载的权限映射使 MySQL 忽略客户端配置。[Windows + MySQL 8.4.13 验收记录](mysql-validation-2026-10-22.md)包含可重复测试脚本入口。

默认 `ssl_mode: REQUIRED` 要求加密但不验证服务端身份；身份验证请设置 `VERIFY_IDENTITY` 并提供 CA。`DISABLED` 是显式关闭 TLS，不与证书配置混用。顶层 `timeout` 覆盖完整预检、导出和上传；CLI `--timeout` 优先。上传使用顶层所选 `s3` 厂家，bucket 留空仅本地，默认 key 为 `mysql/<任务标识>/backup.sql[.gz]`，不自动安装客户端或创建桶。

## kubeadm 控制平面节点：只备份到本地

```sh
./opskit config init
# main：确认 config/config.yaml 中地址、证书路径正确，且 etcdctl 已安装：
./opskit etcd backup
```

生成模板已启用这些值：

```yaml
schema_version: 1
timeout: 1h
etcd:
  endpoint: https://127.0.0.1:2379
  directory: ./data/backups/etcd
  etcdctl: etcdctl
  cacert: /etc/kubernetes/pki/etcd/ca.crt
  cert: /etc/kubernetes/pki/etcd/healthcheck-client.crt
  key_file: /etc/kubernetes/pki/etcd/healthcheck-client.key
s3:
  bucket: ""
```

证书路径依据 [Kubernetes kubeadm PKI 文档](https://kubernetes.io/docs/setup/best-practices/certificates/#certificate-paths)。这是一套常见 kubeadm 配置，不代表所有 Kubernetes 发行版。K3s、RKE2、外置 etcd 或自定义 `certificatesDir` 需替换为实际路径。在远程电脑或容器执行时还需提供能访问的 etcd endpoint 和可读取的证书；`127.0.0.1` 指运行 opskit 的机器或容器自身。

`cacert` 用于验证 etcd 服务端，`cert` 和 `key_file` 是配套客户端证书与私钥。程序不会生成、复制证书，也不会从 kubeconfig 获取它们。模板中的值会传给 etcdctl；未加载配置时不自动套用 Kubernetes 路径。

## 上传到 MinIO

将 `s3.provider` 设为 `minio`，只填写 `s3.minio` 的空项，其余厂家保持模板原样：

```yaml
s3:
  provider: minio
  minio:
    endpoint: https://minio.example.com:9000
    bucket: k8s-backups
    access_key: "replace-with-minio-access-key"
    secret_key: "replace-with-minio-secret-key"
    region: us-east-1
    path_style: true
    key: ""
```

本地 YAML 填写凭据后会优先使用它们；不想在文件中存放凭据时，将 `access_key`、`secret_key`、`session_token` 都留空，再使用下方环境变量方式。

域名、端口与桶名是示例，需替换成实际环境；endpoint 必须是 S3 API 入口。桶需提前创建。将 MinIO 服务账号凭据传给 AWS SDK：

```sh
export AWS_ACCESS_KEY_ID='replace-with-minio-access-key'
export AWS_SECRET_ACCESS_KEY='replace-with-minio-secret-key'
# 长期凭据不需要会话令牌，避免沿用旧的临时令牌：
unset AWS_SESSION_TOKEN
./opskit etcd backup
```

PowerShell 的环境变量写法：

```powershell
$env:AWS_ACCESS_KEY_ID = 'replace-with-minio-access-key'
$env:AWS_SECRET_ACCESS_KEY = 'replace-with-minio-secret-key'
Remove-Item Env:AWS_SESSION_TOKEN -ErrorAction SilentlyContinue
.\opskit.exe etcd backup
```

Windows 上使用时要将 etcd endpoint 与证书路径改为实际可用值。S3 凭据通过 [AWS SDK 默认凭据链](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html) 读取；也可使用共享凭据配置或工作负载角色。不要把占位符当成真实凭据；所选厂家配置块支持 `access_key` / `secret_key`，但不会展开 `${AWS_ACCESS_KEY_ID}`；请填实际值或留空使用环境凭据。

## 三种“key”分别是什么

| 名称 | 用途 | 在哪里设置 |
|---|---|---|
| `etcd.key_file` | etcd TLS 客户端证书的私钥文件路径 | YAML 或 `--key-file` |
| Access Key / Secret Key | 对象存储账号的访问凭据 | `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` |
| `s3.minio.key` / `s3.aws.key` | 桶内对象的完整名称 | 所选厂家块或 `--key` |

例如桶为 `k8s-backups`，对象 key 为 `clusters/prod/snapshot.db`，其对象位置可表示为 `s3://k8s-backups/clusters/prod/snapshot.db`。但 **key 字段只填 `clusters/prod/snapshot.db`**。

定时备份建议保留 `key: ""`，每次自动生成唯一名称：

```text
k8s-backups（bucket）
└── etcd/
    └── 20261015T120000Z-<随机任务号>/
        ├── snapshot.db
        └── snapshot.db.manifest.json
```

自定义 key 不支持日期变量、环境变量或占位符展开，不会自动追加文件名。固定 key 重复运行会触发防覆盖错误，本地新快照仍保留。手工上传时可明确指定完整名称：

```sh
./opskit s3 upload --key manual/first-snapshot.db ./snapshot.db
```

## AWS S3

将 `s3.provider` 改为 `aws`，只修改 `s3.aws` 的桶与所在区域，endpoint 通常留空：

```yaml
s3:
  provider: aws
  aws:
    bucket: replace-with-your-bucket
    region: ap-southeast-1
    endpoint: ""
    path_style: false
    access_key: ""
    secret_key: ""
    key: ""
```

临时凭据需同时提供匹配的 `AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY`、`AWS_SESSION_TOKEN`。上传账号需具备对象写入、HEAD 查询以及分段上传失败时的清理权限；后端必须支持条件写入。opskit 不创建桶、不修改桶权限。

## 排查常见配置问题

| 现象 | 检查方向 |
|---|---|
| etcd 证书文件找不到 | 是否为 kubeadm 节点；文件路径是否正确；容器是否挂载了证书 |
| etcd 证书认证或权限失败 | 客户端证书是否成对、未过期，etcd 认证授权是否允许快照 |
| etcd 服务端证书验证失败 | CA 是否属于该 etcd，连接地址是否在服务端证书 SAN 中 |
| S3 找不到凭据 | 环境变量是否导出到了运行任务的进程；cron/systemd 不自动继承交互 shell |
| S3 403 | 访问密钥、会话令牌、桶权限及后端策略；不会自动改用匿名上传 |
| 对象已存在 | 自定义 key 是否重复；改用新 key 或让 etcd 备份自动生成 |
| S3 私有 CA 不受信任 | 将 CA 安装到运行机器的系统信任库；当前未提供 S3 自定义 CA 配置字段 |

上传后 HEAD 比对大小与 SHA-256 metadata，不能替代内容回读校验或恢复演练。

## 厂家选择与旧配置兼容

`provider` 支持 `minio`、`aws`、`custom`，只加载对应配置块，不回退到其他厂家。未填写 region 时默认 `us-east-1`；MinIO/custom 默认 path-style，AWS 默认虚拟主机方式。备份是否上传由 bucket 决定，仅配置 endpoint、bucket 留空时仍只生成本地备份。MinIO/custom 在启用上传时必须有 endpoint。其他 S3 厂家可使用 `custom`，按其文档设置 endpoint、region、path_style，兼容性仍需实际验证。

旧版平铺的 `s3.bucket` / `s3.endpoint` 等仍可使用，但不能与 `provider` 混用。迁移时将原字段移入对应厂家块，再设置 `provider`。已有 opskit.yaml 不会被自动覆盖，可用 `opskit config init --config opskit.new.yaml` 生成新模板对照迁移。

## 项目默认值来源

- Docker：linux 仓库 `docker/docker-mirror.sh`，包括内网 insecure-registry 列表；写入时合并保留既有条目。
- containerd：`docker/containerd/mirrors.sh`，包含 DockerHub、两个 Google registry 名称、Quay、GHCR 与阿里 ACR 直连。
- Go/npm/pip：`docs/infra-reference.md` 和已使用的 CI/Dockerfile；Go 支持逗号/竖线分隔的代理链及 direct/off。
- Maven：`aliyun-mirrors.md` 中的公开聚合源。
- kind KS 安装器：原 `kubernetes/kubesphere/kind-3.4.1` 使用的公开 3.4.1 镜像源；自建集群不读取这个 kind 专用模板项。

`kind.kubeconfig`、`kind.binary`、KS storageClass 留空属于自动定位行为，不是缺失默认值。生成对照模板可使用 `opskit config init --config config/config.new.yaml`，不会自动覆盖已有生产配置。

## 运行时版本与安装源（main，未发布）

Docker 的 script/binary 安装均自动配置 inotify：备份后更新 `/etc/sysctl.d/99-inotify.conf`，保留其他配置，将 `fs.inotify.max_user_instances` 设置为至少 8192、`fs.inotify.max_user_watches` 至少 524288。已有运行值或该文件中更高的持久值不会降低。使用 `sysctl -p /etc/sysctl.d/99-inotify.conf` 应用并回读确认；失败返回错误，不继续启动安装。下载与包校验失败不会写此配置，`--dry-run` 也不修改内核参数。已有 Docker 的拒绝覆盖规则保持不变；此前安装的主机需手动应用，宿主机限制应在宿主机设置。

Docker、cri-dockerd、containerd 的 install 命令默认直接执行，不接受 `--apply`；需要预览时添加 `--dry-run`。

`k8s.docker.version`、`cri_dockerd`、`containerd`、`runc`、`cni_plugins` 均为完整 vX.Y.Z；安装命令的 `--version` 优先于对应配置。containerd 另支持 `--runc-version` 和 `--cni-version`。Docker 27.3.1、cri-dockerd 0.3.22、containerd 2.1.3、CNI 1.5.1 来自 linux 项目；runc 固定为 1.2.6，不沿用原脚本的 1.1.10，不做 glibc 失败时静默降级。此版本组合尚未做真机兼容验收。

`install.docker.method` 为 script 或 binary，`install.docker.mirror` 为官方脚本的软件包源（默认清华 URL，也支持 Aliyun / AzureChinaCloud），和 `mirrors.docker` 的镜像拉取源分别设置。脚本源 `install.docker.script` 默认通过 Nexus 代理获取固定提交的官方脚本；二进制源 `install.docker.binary_base` 按 x86_64/aarch64 拼接 Docker 静态包。`install.containerd.download_base`、`install.runc.download_base`、`install.cni_plugins.download_base`、`install.cri_dockerd.download_base` 分别用于对应组件，默认使用项目 Nexus GitHub release 代理；可改成 https://github.com。

`--url` 只覆盖主包/脚本，`--sha256` 只校验该文件；runc/CNI 无单独摘要字段，使用源站 HTTPS。安装不改既有配置、不升级现有运行时，不自动修改主机网络参数。完整示例见首页运行时安装章节。既有 config/config.yaml 不自动覆盖；用 `opskit config init --config config/config.new.yaml` 生成对照模板，手工合并新字段。

安装下载统一使用 `install.download_dir`；模板留空，默认用户目录下的 `.opskit/downloads`，不随工作目录变化。配置非空相对路径仍按配置文件目录解析；CLI `--download-dir` 优先，按当前目录解析。既有显式配置不自动改变，想使用新默认值请将 download_dir 设为 ""。

同一下载根目录下的组件下载、安装和 `k8s create` 共用按完整 URL 区分的文件缓存，索引位于 `.cache`。复用前重新计算本地 SHA-256，文件或索引缺失、损坏时重新下载；原有的上游摘要和版本验证照常执行。版本、架构或下载源改变会使用不同 URL，不复用其他资源。JSON 发布元数据与安装脚本每次重新获取。缓存只在完整传输后登记，失败任务仍保留已完成文件；旧版没有索引的文件首次运行会重新获取。若上游修正了相同 URL 的内容，可删除该下载根目录的 `.cache` 索引强制重新获取。使用 sudo 或切换用户时应配置相同的绝对下载目录，才能跨入口共享缓存。

kind 安装优先复用 PATH 中已有且可写的目录：先检查各平台常用目录，再按 PATH 顺序检查其他目录；不采用空项、相对路径、文件、不存在或不可写的目录。复用成功不修改 shell 配置或 Windows 用户 PATH。没有可用目录才使用平台默认入口并设置用户 PATH。`--link-dir` 可显式指定入口目录。平台默认目录：Linux root /usr/local/bin、普通用户 ~/.local/bin；macOS 普通用户优先 PATH 中可写的 /opt/homebrew/bin 或 /usr/local/bin；Windows 为 LOCALAPPDATA/opskit/bin，未设置 LOCALAPPDATA 时使用用户目录 AppData/Local/opskit/bin。Unix 使用 symlink，Windows 复制 exe。Bash 维护 ~/.bashrc 及已有优先登录配置（.bash_profile > .bash_login > .profile），Zsh 维护 ZDOTDIR/.zshrc 或 ~/.zshrc，sh/dash/ksh 维护 ~/.profile；已在当前 PATH 中则不写启动配置。追加前备份，不改已有行。Windows 写入 User 范围的 PATH，不修改 Machine 范围。需要新终端读取；安装日志同时给出当前终端立即生效命令。其他 shell 报错提示自行配置，已有其他来源的 kind 入口不覆盖。安装后会运行入口 version，并检查当前 PATH 是否实际命中此入口；旧命令遮挡返回错误，未加载新 PATH 时输出待生效。Windows 保存用户 PATH 后回读确认；用户 shell 的 alias/function 及完整新终端启动结果仍须在该终端验收。

`kind create` 默认执行创建，版本仍由 `kind.kubernetes.version` 或 CLI `--version` 指定；`--dry-run` 仅输出配置，不检查外部工具、不调用 Docker/kind/kubectl。实际创建前继续检查依赖、Docker、kind 工具版本及同名集群；已有集群不会删除或覆盖。创建命令不接受旧 `--apply`，删除集群默认执行，集群名按 `--name` > `OPSKIT_KIND_NAME` > `kind.name` > 默认值解析，无需重复确认名称，`--dry-run` 仅预览。

## kubectl 安装（main，未发布）

OpenEBS 的 Operator 安装入口为 `opskit openebs install`。下载地址与摘要读取 `install.openebs.operator_url`／`sha256`，统一缓存读取 `install.download_dir`；`openebs.manifest` 可指定本地文件，`openebs.set_default` 控制是否显式设置默认存储类。参数、优先级和安装边界见[OpenEBS 指南](openebs.md)。

`install.openebs.fallback_urls` 为备用完整地址列表，默认保留 Nexus。主源下载失败或 60 秒超时后顺序回退，重复地址去重，`[]` 关闭备用；所有尝试共享总超时及同一 SHA-256，摘要不匹配不切换。

`opskit kubectl`、`opskit kubectl -h`、`opskit kubectl --help` 显示说明；`opskit kubectl install -h/--help` 显示全部参数。帮助不依赖配置或外部工具。

```yaml
k8s:
  kubernetes:
    version: v1.24.7  # kubeadm/kubelet/kubectl 同版
install:
  kubectl:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries
  download_dir: ""  # 默认用户目录 .opskit/downloads
```

优先级为 CLI `--version` > k8s.kubernetes.version；缺少完整版本时报错，不隐式选择 latest。下载源优先级为 `--kubectl-download-base` > 环境变量 `KUBECTL_DOWNLOAD_BASE` > install.kubectl.download_base > 模板默认 Nexus 代理；空环境变量不覆盖 YAML。默认使用 Nexus `kubernetes-binaries` 代理。官方直连可选择 `https://dl.k8s.io/release` 或 `https://storage.googleapis.com/kubernetes-release/release`。URL 格式为 `<base>/<version>/bin/<os>/<arch>/kubectl[.exe]`，校验文件追加 `.sha256`；不跳过 TLS、摘要或安装后的版本检查。

默认直接安装；`--dry-run` 只输出计划。安装目录按版本隔离，支持 `--bin-dir`，命令目录支持 `--link-dir`；下载及 PATH 沿用 kind 的共用实现。验证客户端 JSON 版本，不读取或连接 kubeconfig。SHA/HTTP/客户端版本校验失败时保留已有版本文件；已有其他来源的 PATH 入口不覆盖。测试覆盖原生测试程序的完整下载安装、重复安装、失败保护和帮助入口，真实集群访问仍需实际验收。

## kind Ingress 入口端口

`kind.http_port: 80`、`kind.https_port: 443` 分别映射到控制平面节点容器 80/443，绑定 `0.0.0.0`；CLI `--http-port` / `--https-port` 优先于 YAML。`kind.console_port` 默认 30880，映射 KubeSphere 节点端口 30880 并绑定 `0.0.0.0`，CLI 可用 `--console-port` 覆盖宿主机端口；已有集群需重建后生效。端口范围为 1～65535，三项不能重复；被其他服务占用时须更换宿主机端口。

创建前用 `opskit kind create --dry-run` 检查生成的 extraPortMappings。控制平面设置 `ingress-ready=true`，Ingress Controller 需另行部署到此节点并监听节点 80/443（例如 hostPort/hostNetwork）；端口映射不自动安装 Controller 或创建 Ingress 对象。已核对 kind v0.17.0 配置类型支持这些字段。已有集群的 Docker 映射不会变更，新版 opskit 不会自动删除旧集群；需使用新集群名或自行安排重建。实际集群 Ingress 连通性尚未验收。

下载源变量示例：`export KUBECTL_DOWNLOAD_BASE=https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries`；用 `unset KUBECTL_DOWNLOAD_BASE` 取消覆盖。PowerShell 用 `$env:KUBECTL_DOWNLOAD_BASE = "..."`。已有配置若仍显式填写 dl.k8s.io 会继续使用该值，应按需修改配置或使用环境变量覆盖。Nexus 代理不保证在所有网络或任意版本可用；缺少目标文件时返回错误，不静默更换版本。

下载源命名统一：CLI `--kubectl-download-base`、环境变量 `KUBECTL_DOWNLOAD_BASE`、YAML `install.kubectl.download_base`。旧 `--download-base` 已移除，传入会报未知参数。命名变更只针对 kubectl，不改变 kind 的 --download-base。

## 下载源配置

| 下载内容 | YAML 配置项 |
|---|---|
| Calico 固定版本清单 | `install.calico.download_base`（摘要为 `install.calico.sha256`） |
| Go SDK 压缩包 | `install.go.download_base` |
| Go 发布 SHA-256 元数据 | `install.go.checksum_url` |
| kind 二进制和校验文件 | `install.kind.download_base` |
| kubectl 二进制和校验文件 | `install.kubectl.download_base` |
| Docker 官方安装脚本 | `install.docker.script` |
| Docker 静态安装包 | `install.docker.binary_base` |
| containerd | `install.containerd.download_base` |
| runc | `install.runc.download_base` |
| CNI plugins | `install.cni_plugins.download_base` |
| cri-dockerd | `install.cri_dockerd.download_base` |

默认地址只维护在 `config.example.yaml`，代码读取嵌入模板作为默认值；用户配置留空时使用该默认值。已有用户配置不自动覆写。kind 源优先级为 CLI `--download-base` > `KIND_DOWNLOAD_BASE` > YAML > 模板默认值；该环境变量不影响运行时安装。运行时 `--url` 可覆盖主包地址，containerd 配套 runc/CNI 分别使用自己的下载配置块。Docker 官方脚本内部的软件包源由 `install.docker.mirror` 控制。上述配置针对文件下载，容器镜像仍走 mirrors 配置。完整下载地址、版本与架构在执行日志中显示。

## Docker 清华软件包源

按 [清华 Docker CE 帮助](https://mirrors.tuna.tsinghua.edu.cn/help/docker-ce/) 设置软件包源：

```yaml
install:
  docker:
    method: script
    mirror: https://mirrors.tuna.tsinghua.edu.cn/docker-ce
    script: https://nexus.ihome.sxxpqp.top:8443/repository/raw-githubusercontent/docker/docker-install/ada7a567e59b3935bd601bb21e422da50e80e376/install.sh
```

运行 `opskit docker install --version v27.3.1`，或用 `--mirror https://mirrors.tuna.tsinghua.edu.cn/docker-ce` 覆盖软件包源。URL 通过子进程环境 `DOWNLOAD_URL` 传递，此时不传脚本的 `--mirror`，避免其覆盖该地址；Aliyun / AzureChinaCloud 仍通过官方 `--mirror` 传递。

清华地址是 Docker CE 软件包仓库，不是脚本下载地址，也不是 Docker Hub 镜像源。清华文档的脚本仍来自 GitHub；本项目模板使用既有 Nexus raw 代理映射，固定官方脚本提交，实际可达性需目标环境验证。不能访问该代理时，将 `install.docker.script` 改为你可访问的官方脚本托管地址。Docker 目标版本仍由 `k8s.docker.version` / `--version` 指定。

## kind 下载源分组

kind 安装文件的下载地址使用组件独立配置：

```yaml
kind:
  version: v0.17.0
install:
  kind:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/raw-github
```

CLI `--download-base` > `KIND_DOWNLOAD_BASE` > `install.kind.download_base` > 模板默认值。版本仍由 `--version` 或 `kind.version` 指定，不自动使用 latest。旧 `install.kind_download_base` 已移除，需迁移到上述嵌套结构。

## install 统一组件分组

所有组件专用选项统一为 `install.<组件>.<参数>`；共享的 `install.download_dir` 仍供所有组件使用。版本继续在 `versions` 下固定，或通过 `--version` 显式覆盖。

```yaml
install:
  download_dir: ""
  kind:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/raw-github
  kubectl:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries
  docker:
    method: script
    mirror: https://mirrors.tuna.tsinghua.edu.cn/docker-ce
    script: https://nexus.ihome.sxxpqp.top:8443/repository/raw-githubusercontent/docker/docker-install/ada7a567e59b3935bd601bb21e422da50e80e376/install.sh
    binary_base: https://mirrors.aliyun.com/docker-ce/linux/static/stable
  containerd:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/raw-github
  runc:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/raw-github
  cni_plugins:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/raw-github
  cri_dockerd:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/raw-github
```

旧平铺字段 `kind_download_base`、`kubectl_download_base`、`docker_method`、`docker_mirror`、`docker_script`、`docker_binary_base`、`release_base` 已移除，读取时报未知字段。原 `release_base` 的值分别迁移到 containerd、runc、cni_plugins、cri_dockerd 块中的 `download_base`；各组件可单独指定来源，不再共用隐式发布地址。CLI 参数和环境变量名称保持原命名。

## kubectl：Nexus 与阿里云镜像

当前安装器支持配置 Nexus 的 Kubernetes 裸二进制代理，例如：

```yaml
install:
  kubectl:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries
```

例如 v1.24.7 的 Linux amd64 会请求 `<base>/v1.24.7/bin/linux/amd64/kubectl` 和 `kubectl.sha256`。下载、SHA-256 校验及执行 `kubectl version` 的安装检查保持不变。版本仍用 `--version v1.24.7` 或 `k8s.kubernetes.version` 固定，不取 latest。

```sh
opskit kubectl install --version v1.24.7 --dry-run
# 使用其他兼容的镜像根地址：
opskit kubectl install --version v1.24.7 --kubectl-download-base https://your-mirror.example/release --dry-run
```

阿里云来源可以使用 OSS 自建镜像或其他满足相同 URL 结构的文件服务，直接填写根地址即可。阿里云 apt/yum Kubernetes 镜像提供 deb/rpm 软件包，不能直接用作这个裸二进制安装器的 download_base。本次探测 `mirrors.aliyun.com/kubernetes-release/kubectl` 和 `.../release` 下的 v1.24.7 Linux amd64 候选路径均返回 404，因此未将它们列为可用默认源，也不会在 Nexus 失败时静默切换过去。

现有用户配置不会自动改写；请编辑 `install.kubectl.download_base`，或用环境变量 `KUBECTL_DOWNLOAD_BASE` / CLI 覆盖。Nexus 未缓存文件需先回源，受其上游网络和缓存状态影响。此前探测曾出现校验文件可读、完整二进制回源返回 504；本次环境无法解析 Nexus 域名，未验证真实下载或安装。默认值已改为 Nexus，使用前应确认目标版本的二进制及校验文件均可访问。

Docker 默认镜像源按顺序为 `https://index.dockerhub.sxxpqp.top`、`https://dockerhub.ihome.sxxpqp.top:8443`。已有配置不会自动增加源，请在本地 YAML 的 `mirrors.docker` 列表中加入新地址，再执行 `opskit mirror docker`。旧 `--apply` 已移除，预览使用 `--dry-run`。

`kubesphere install` 未指定 `--context` 时读取 kubeconfig 的 `current-context`。路径优先级为 `--kubeconfig` > `KUBECONFIG` > `kind.kubeconfig` > kubectl 默认的 `~/.kube/config`；`KUBECONFIG` 多文件列表由 kubectl 按其规则合并。指定 `--kind NAME` 时仍推导 `kind-NAME` context，无路径配置时使用对应专用 kubeconfig。执行前输出目标 context，默认直接安装，`--dry-run` 仅做只读预检，不提交清单；旧 `--apply` 已移除。

KS 提交 ClusterConfiguration 后每 5 秒读取资源 JSON，等待 `status.core.status=enabled`（与安装器及核心 Deployment 等待共用安装总超时，默认 45 分钟，可用 `--timeout 45m` 延长）；初始缺少 status/core 时继续等待，兼容 kubectl v1.24.7。读取或解析失败仍返回错误。该等待失败不表示已创建资源被回滚；可用 `kubectl -n kubesphere-system logs deployment/ks-installer -f` 查看安装器日志，已有实例保护仍会阻止重新安装。

KS 安装提交配置后先执行 `rollout status deployment/ks-installer`，等待期限使用安装总超时的剩余时间，就绪后输出最近 100 行安装器日志，再继续轮询 core 状态及检查核心 Deployment。不使用阻塞的 `logs -f`；需要持续查看时可在另一个终端执行 `kubectl -n kubesphere-system logs deployment/ks-installer -f`。

KS 初始账号为 `admin`；`k8s.kubesphere.password` 默认 `Sxxpqp.top1`，`--password` 可覆盖。直接以明文注入安装配置，不创建本地密码文件。旧 `password_file` 和 `--password-file` 已移除，需迁移为 `password`。仅影响全新安装，不会修改已安装集群的账号密码。密码须包含大写字母、小写字母和数字，长度为 8～64 字节，预检时校验；真实 KS 登录尚未验证。

KS 安装总超时默认 45 分钟，`--timeout 60m` 可延长。core enabled 后先将三个核心 Deployment 的 `progressDeadlineSeconds` 设为该总超时的秒数，再检查 rollout，避免服务端进度期限先于客户端等待结束。整个流程仍受同一个总期限约束。

KS 核心服务就绪后，opskit 最终摘要输出 `Account: admin` 和 `Password: <配置的明文密码>`，用于登录。安装器日志与 opskit 摘要均使用配置中的实际密码。

containerd registry 配置识别支持 `plugins."io.containerd.grpc.v1.cri"` / `plugins."io.containerd.cri.v1.images"` 及其单引号等价写法；仍拒绝未知布局和未迁移的认证/TLS 配置。

kind 节点的默认 containerd 配置可能仅声明 `.registry.mirrors` 子表，而省略 `.registry` 父表。`mirror containerd --kind NAME` 支持补齐该父表、设置 certs.d 并迁移旧 mirror；保留 sandbox_image、运行时和快照设置，旧认证/TLS 配置仍要求人工迁移。宿主机旧 mirror 配置仍不自动迁移。

kind mirror 重启节点 containerd 后，每秒检查 CRI，最多等待 60 秒（受 mirror 总超时约束）；就绪后才验证镜像拉取并处理下一个节点。等待失败输出该节点 containerd 的 systemd 状态、最近 50 行日志及备份目录，便于区分启动延迟和配置错误。


## K8s SSH 集群部署

新入口 `opskit k8s create --config config/config.yaml`，默认执行，`--dry-run` 只读取和校验配置与本地网络清单，不读取认证环境变量、不连接 SSH、不下载或修改节点。不依赖 KK；只参考其 hosts/roleGroups 配置习惯。完整模板见仓库 `config.example.yaml`。

### 配置与版本迁移

旧全局 `versions`、顶层 `kubeadm` 和顶层 `kubesphere` 已删除，不保留兼容别名。kind 使用 `kind.version`、`kind.kubernetes.version`、`kind.kubesphere`；K8s 及独立运行时安装使用 `k8s` 下的对应组件配置。kubeadm/kubelet/kubectl 只有一个版本入口 `k8s.kubernetes.version`，不接受 `k8s.kubectl.version` 等独立版本字段。命令 `--version` 只覆盖该次操作；CLI 参数仍优先。

```yaml
schema_version: 1
k8s:
  name: sxxpqp
  hosts:
    - {name: master1, address: 192.168.10.10, internalAddress: 192.168.10.10, port: 22, user: root, password: "请填写SSH密码"}
    # - {name: worker1, address: 192.168.10.11, internalAddress: 192.168.10.11, port: 22, user: root, password: "请填写SSH密码"}
    # 支持 privateKeyPath、privateKeyPassphraseEnv；密码也可改为 passwordEnv。
    # 普通用户 sudo 默认复用 SSH 密码；用 sudoPassword / sudoPasswordEnv 覆盖，无密码时需免密 sudo。
  roleGroups:
    etcd: [master1]
    master: [master1]
    # worker: [worker1]
  controlPlaneEndpoint: {address: 192.168.10.10, port: 6443}
  kubernetes: {version: v1.24.7, containerManager: containerd, clusterName: cluster.local}
  network:
    kubePodsCIDR: 10.244.0.0/16
    kubeServiceCIDR: 10.96.0.0/12
    manifest: ""  # 自动安装固定版本 Calico；本地文件可覆盖
    calico: {version: v3.26.5}
  registry:
    imageRepository: registry.k8s.io
  kubesphere: {enabled: false, version: v3.4.1, password: Sxxpqp.top1, storageClass: ""}
  deploy:
    concurrency: 3
    timeout: 45m
    outputDir: "" # 默认 ~/.opskit/clusters/<name>
    nodeBinaries: {} # 非 Linux 管理机/异构节点指定 Linux opskit 文件路径
```

样例保留当前旧 KS 测试环境的固定版本，并非新生产环境推荐。首版使用 kubeadm v1beta3，支持 Kubernetes 1.24～1.30；不猜测更高版本配置。Kubernetes 1.24/1.25 拒绝 containerd 2.x；1.26+ 的 containerd 1.6 至少需要 1.6.15，其他搭配仍需真机验证。criTools 与 Kubernetes 必须同 minor。选择 docker 时使用 Docker 二进制包及 cri-dockerd，不安装独立 containerd/runc；k8s.containerd/runc 仅用于 containerd 路线。

### 节点与认证

address 用于 SSH，internalAddress 用于 API/kubelet 集群通信。省略 internalAddress 时使用 address，因此 address 必须是可用于集群通信的 IPv4；多网卡时必须显式填写本机正确内网地址。首版仅支持一个 master，同节点 kubeadm etcd；roleGroups.etcd 必须等于 master，不支持独立 etcd 或 HA。每个 hosts 节点须出现在 master 或 worker 中；worker 注释时不会连接该节点。只有一个 master 且无 worker 时，会移除控制平面的调度 taint。

密码和私钥互斥，password 与 passwordEnv 互斥。没有密码和私钥时使用管理机 SSH Agent。普通用户自动 sudo；显式 sudoPassword/sudoPasswordEnv 优先，否则复用 password/passwordEnv 的 SSH 登录密码；无密码（私钥或 Agent 登录）时使用 sudo -n。密码通过标准输入传入，不放进命令参数；用户仍须拥有 sudo 权限。requiretty 等强制交互 sudo 策略需由管理员提前调整。SSH 按 KK 方式使用配置的密码、私钥或 Agent 直接认证，HostKeyCallback 使用 InsecureIgnoreHostKey；不读取或写入 known_hosts，不提示 yes，也不校验主机密钥变化。移除 `k8s.deploy.knownHostsFile` 字段，旧配置需删除该项，不保留别名。相对文件路径基于 YAML 所在目录，不展开 ~。

管理机需可访问 SSH 和配置的下载源。节点需运行 Linux amd64/arm64 + systemd，并具备 systemctl。全节点连接、root/sudo、systemd、架构及已有集群保护检查全部通过，且管理机下载准备完成后，自动安装缺少命令对应的系统软件包；使用 apt-get/dnf/yum 和节点现有软件源，不修改源配置、不主动升级已存在的组件，安装后重新检查全部依赖。包管理器失败则停止后续部署；已经安装的软件包不回滚。不支持的包管理器要求提前安装全部依赖。

每台控制平面和工作节点均需准备以下组件（Ubuntu / Debian 包名）：

| 软件包 | 预检命令 | 用途 |
| --- | --- | --- |
| conntrack | conntrack | 连接跟踪管理 |
| socat | socat | kubeadm 所需通信工具 |
| iptables | iptables | 网络规则 |
| iproute2 | ip、tc | 地址、路由和流量控制 |
| kmod | modprobe | 加载内核模块 |
| procps | sysctl | 内核参数 |
| util-linux | swapoff、nsenter | 禁用 swap、进入命名空间 |
| mount | mount | 挂载管理 |
| tar | tar | 解压组件包 |
| ethtool | ethtool | 网络设备检查 |
| systemd | systemctl | 管理运行时和 kubelet 服务 |

Ubuntu / Debian 在每台节点上执行：

```bash
sudo apt-get update
sudo apt-get install -y conntrack socat iptables iproute2 kmod procps util-linux mount tar ethtool systemd
```

以上命令使用发行版软件源的候选版本，可先用 `apt-cache policy <包名>` 查看，需固定版本时使用 `包名=版本`。SSH 服务和普通用户的 sudo 权限也须提前配置；仅安装 systemd 软件包不能让非 systemd 环境满足要求。opskit 会分发版本化的 Kubernetes 与运行时二进制，缺失的 OS 软件包由上述自动阶段安装；需要固定包版本时由管理员预装。该阶段使用节点网络及系统软件源，不能仅依赖管理机下载代理；`--dry-run` 不连接节点、不安装软件。


### 下载、二进制与镜像

下载源继续统一放在 `install.<component>.download_base` / `install.docker.binary_base`，默认只定义于模板。管理机按节点架构下载一次，校验官方 SHA-256（kubeadm/kubelet/kubectl/crictl/containerd/runc/CNI plugins），然后分发。Docker 和 cri-dockerd 包使用 HTTPS 传输及节点实际版本检查，管理机另计算摘要验证分发内容，不把该摘要称为独立上游签名或摘要认证。节点离线缓存中的文件再次计算摘要，缺项不回源下载。各本地安装器继续验证实际二进制版本，不静默接受目标版本不一致。

Windows/macOS 管理机预检 Linux 节点的 CRI socket 时，按 Unix 路径规则校验 `unix:///run/containerd/containerd.sock` / `unix:///run/cri-dockerd.sock`，不将其作为管理机本地路径解析；实际节点安装仍要求 Linux。

Linux 同架构管理机直接分发当前 opskit 可执行文件。非 Linux 管理机或异构架构需配置 `nodeBinaries: {amd64: /path/opskit_linux_amd64, arm64: /path/opskit_linux_arm64}`；文件必须从相同代码构建，不得拿缺少内部节点协议的旧 Release 替代。目标主机不需要 Go。现有仓库发布包仍区分操作系统/架构；本功能尚未完成发布，不宣称当前公开附件已支持。

`k8s.registry.imageRepository` 仅控制 kubeadm 系统镜像仓库。运行时镜像源统一读取顶层 `mirrors`：Docker 使用 `mirrors.docker` / `mirrors.docker_insecure_registries`；containerd 使用完整的 `mirrors.containerd`，保留各仓库 endpoint、server、skip_verify、override_path，不再由 DockerHub 列表覆盖。重复的 `k8s.registry.registryMirrors` 已删除，旧配置须移除此字段，读取时会报未知字段，不保留别名。Docker 的 registry-mirrors 仅加速 DockerHub；使用 Docker 时请将 imageRepository 按需设置为可访问的系统镜像仓库，insecure-registries 本身不会重写其他仓库地址。`network.manifest` 留空自动下载固定版本 Calico，校验 SHA-256 并按 Pod CIDR 渲染；填入本地 YAML 则优先使用，不下载、不改写，由部署者确认镜像、版本和 Pod CIDR 配套。CNI plugins 二进制不等于网络插件 Deployment/DaemonSet。网络和存储清单可以按环境准备，KS 默认关闭，不会自动安装存储驱动。

### 执行、重跑与结果

并行准备节点：备份 fstab 并禁用其中 swap，加载 overlay/br_netfilter，持久化 IPv4/bridge 转发参数，安装所选运行时和 Kubernetes 工具，配置镜像源和 systemd。systemd swap unit、zram 等额外 swap 来源需管理员自行禁用，init/join 再检查实际 swap。

控制平面初始化后应用网络清单，所有节点加入后自动路径等待 Calico DaemonSet 和控制器就绪；生成短期 token（30 分钟）及 CA 公钥摘要，自动传给 worker 完成 kubeadm join，随后撤销本次 token。join 凭据只存在于受保护的传输文件和临时配置中，正常结束/失败尽力清除；日志过滤 join token。管理机断电等无法执行清理时，token 仍受 TTL 限制。不会跳过发现过程中的 CA 校验。

管理机保存 0600 kubeconfig 和阶段日志，默认位置 `~/.opskit/clusters/<name>`，不覆盖用户 ~/.kube/config。每个节点在 `/var/lib/opskit/clusters/<name>/state.json` 记录阶段、目标版本指纹及集群 CA；重跑核对实际工具版本和 CA，跳过已完成安装，已加入目标集群的 worker 不重复加入。版本/网络目标发生变化、属于其他集群、存在未管理的配置或 kubeadm/kubelet/crictl 半安装工具时明确报错，不盲目升级/覆盖/reset。若进程被强制终止留下 deploy.lock，需先确认没有部署运行再人工处理。

最终按配置中的节点名称逐一确认 Ready；缺失节点不视为成功。KubeSphere 仅 enabled=true 时安装，沿用前置条件和已有实例保护，不能将创建集群当作升级 KS 的入口。用户选择开启 KS 时仍需提供可用的默认或指定 StorageClass。

本地维护命令 `opskit kubeadm install/prepare/init/join` 保留，版本和网络默认从 k8s 获取；init 的 advertise-address、join 的 endpoint/token/CA hash 使用 CLI 显式传入。install 可复用同目标版本的已有 kubectl，保留其原路径和链接；其他已有工具仍拒绝覆盖；prepare/init/join 不接受已有节点数据。prepare 与自动 create 使用同一套系统依赖逻辑，通过 apt-get/dnf/yum 安装缺失的 conntrack、socat 等工具，已有工具不重复安装，然后才修改内核、sysctl 与 swap。check 仍为只读检查，不安装软件。所有命令支持 --dry-run，拒绝旧 --apply。

已验证：本地 SSH 密码认证及 KK 式主机指纹跳过、sudo stdin、下载/离线校验、模拟多节点编排、配置隔离、worker 自动加入顺序与所有预期节点的就绪判定。尚未验证真实多服务器的服务启动、网络连通性、重跑恢复和 KS 端到端安装；HA、升级、删除和 SSH 密码交互输入尚未实现。

重跑控制平面初始化时先核对服务端实际 Kubernetes 版本，再应用网络清单，版本不匹配直接停止。运行时等待要求 CRI 的 RuntimeReady=true，不能只以 info 命令成功为准。单节点模式仅移除 master/control-plane 的 NoSchedule taint，保留其他 taint，并使用 resourceVersion 防止覆盖并发更新；API/权限错误会返回，不再忽略。

管理机向每个节点阶段传递总超时的剩余时间，节点执行器使用同一剩余期限，不会为每个阶段重新开始完整的 45 分钟等待；SSH 中断时仍由节点自身的期限约束子命令。

## Go SDK 安装与代理

```yaml
schema_version: 1
go:
  version: 1.26.2
install:
  go:
    download_base: https://mirrors.aliyun.com/golang/
    checksum_url: https://go.dev/dl/?mode=json&include=all
mirrors:
  go: https://goproxy.cn,direct
```

```sh
opskit go install --dry-run
opskit go install
opskit go install --version 1.27.2 --proxy https://goproxy.cn,direct
opskit mirror go  # 后续仅修改代理
```

版本优先级为 `--version` > `go.version` > 模板固定版本 1.26.2；接受完整 `1.X.Y`、`go1.X.Y` 或 `v1.X.Y`，不接受 latest/rc/beta。Go SDK 版本与 kind、K8s 无关。仅支持当前机器的 Linux/macOS amd64、arm64，不进行跨平台安装；Windows 使用此命令会报不支持。

SDK 默认源为阿里云 `https://mirrors.aliyun.com/golang/`，已有配置不会自动改写；SHA-256 元数据默认仍从 Go 官方读取。下载地址为 `<download_base>/go<version>.<os>-<arch>.tar.gz`。下载根地址优先级为 `--go-download-base` > `GO_DOWNLOAD_BASE` > `install.go.download_base` > 模板默认值；此环境变量只影响 Go SDK。发布元数据由 `--checksum-url` > `install.go.checksum_url` > 模板默认值提供，格式与 Go 官方下载 JSON 相同，必须包含目标版本、精确文件名和有效 SHA-256。镜像仅提供压缩包时可以保留官方元数据地址；离线/内网部署需同时提供可访问的元数据源。校验或版本不匹配会失败，不静默换源或换版本。

`--download-dir` 或 `install.download_dir` 控制保留下载位置，默认 `~/.opskit/downloads`。完整 SDK 从暂存目录解压并校验后移动到 `~/.local/share/opskit/go/go<version>`，`--install-dir` 可覆盖；不会向已有目录叠加文件，同版本重装请使用新目录。`--link-dir` 覆盖 go/gofmt 入口目录，默认复用 PATH 中可写目录；其他来源的入口不会覆盖，PATH 遮挡会报错。新版本安装可切换默认版本目录下的 opskit 托管入口；自定义目录的旧入口需要自行处理。没有修改调用进程的 PATH，配置待生效时按输出更新当前 shell 或重开终端。

安装后读取新 SDK 的 `go env GOENV`，备份后只更新该文件的 `GOPROXY`，保留其余设置。代理优先级为 `--proxy` > `mirrors.go` > 模板默认代理。配置作用于执行命令的用户，sudo/root 执行不会替其他用户配置。`GOENV=off` 或文件写入失败会返回错误，SDK 可能已经安装，应根据错误处理代理及入口。`GOPROXY` 环境变量仍会覆盖文件配置；检测到冲突会提示。继承的 `GOROOT` / `GOTOOLCHAIN` 不参与安装版本验证，已有 GOROOT 应由用户移除或调整；不修改 GOSUMDB。`--dry-run` 不下载、不调用 Go、不写 SDK、代理或 PATH。

本地测试使用模拟 SDK 和 HTTP 服务覆盖完整流程、校验失败、危险归档、配置备份及参数优先级；不代表已经完成真实 Go SDK 的部署验收。

## Cobra 命令与 Shell 补全

全部命令由 Cobra v1.10.2 / pflag 解析，命令层级和配置键保持不变。使用 `opskit --help`、`opskit kind --help`、`opskit help kind`、`opskit kind help` 或 `opskit kind create --help` 逐层查看帮助。帮助页从已注册的命令、参数生成；查看帮助和生成补全脚本不读取用户配置、不连接远端。内部 `k8s node` 不出现在公开命令列表中。

CLI 参数仍优先于组件环境变量，其次 YAML 和模板默认值。`--config FILE` 仍属于具体命令，例如 `opskit k8s create --config config/config.yaml`。使用双横线长参数；单横线仅用于已注册的短参数，如 `-h`。布尔值使用 `--dry-run` 或 `--dry-run=false`，不要将 false 作为单独位置参数。支持交错参数，例如 `opskit s3 upload snapshot.db --bucket BUCKET --key KEY`。旧 `--apply` 及已移除的下载参数不会恢复；未知命令和参数返回退出码 2，业务任务保留原有错误码。Cobra 的 `--help` 会优先显示帮助，不执行任务。

```sh
# Bash：在当前 shell 加载；也可保存到 bash-completion 目录
source <(opskit completion bash)
# Zsh：先确保已执行 autoload -Uz compinit && compinit，再加载
source <(opskit completion zsh)
# Fish：当前 shell 加载
opskit completion fish | source
```

PowerShell 可执行 `opskit completion powershell | Out-String | Invoke-Expression` 加载当前会话补全。持久化时将脚本保存到该 Shell 的补全目录或从用户启动文件加载。补全由相同命令树生成，包含对应子命令和可用参数。

## Calico 自动安装

`k8s create` 的 `network.manifest` 留空时，自动使用 `k8s.network.calico.version`，缺省从嵌入模板读取固定版本 v3.26.5。当前自动路径仅接受 v3.26.5 和 Kubernetes 1.24～1.28；其他组合应先验证，再使用本地清单覆盖，不静默替换版本。版本配套参考 [KubeKey 组件矩阵](https://github.com/kubesphere/kubekey/blob/main/docs/en/installation/components.md#calico)，这不代表 opskit 已完成生产部署验收。

```yaml
install:
  calico:
    download_base: https://raw.githubusercontent.com
    fallback_bases:
      - https://nexus.ihome.sxxpqp.top:8443/repository/raw-githubusercontent
    sha256: 2468a8c939658b959860b0e78c3d2f420c53bb08af7511f3d335e051f9db8b22
k8s:
  network:
    kubePodsCIDR: 10.244.0.0/16
    kubeServiceCIDR: 10.96.0.0/12
    manifest: ""
    calico:
      version: v3.26.5
```

```sh
opskit k8s create --dry-run
opskit k8s create --calico-version v3.26.5
# 可通过配置、组件环境变量或参数使用保持相同路径的内网代理：
# CALICO_DOWNLOAD_BASE=https://your-raw-github-proxy.example
# opskit k8s create --calico-download-base https://your-raw-github-proxy.example
```

清单地址为 `<download_base>/projectcalico/calico/<version>/manifests/calico.yaml`。根地址优先级：`--calico-download-base` > `CALICO_DOWNLOAD_BASE` > `install.calico.download_base` > 模板默认值；该环境变量只影响 Calico。原始下载保留到统一 `install.download_dir`（默认 `~/.opskit/downloads`）下。官方固定文件的 SHA-256 已实际下载计算；镜像必须提供相同内容。摘要不匹配或 YAML/镜像版本校验失败时，在修改节点前退出。渲染后的清单仅在部署临时目录中保存并随 bundle 分发。

自动路径仅修改官方清单中 calico-node 的 `CALICO_IPV4POOL_CIDR` 与 `IP_AUTODETECTION_METHOD=kubernetes-internal-ip`，保留其他对象及官方镜像名。Pod CIDR 必须是规范 IPv4 网络地址且前缀不大于 /26，以容纳默认分配块；与 Service/节点网段的现有冲突检查仍保留。镜像由节点容器运行时通过既有 mirror 拉取，不把 YAML 下载代理当作镜像代理。

沿用该版本官方 BGP + IP-in-IP 默认模式：节点间须允许 TCP 179（BGP）及 IP 协议 4（IP-in-IP），内核需支持对应网络功能；不自动修改防火墙、NetworkManager 或主机安全策略。参考[官方系统要求](https://docs.tigera.io/calico/latest/getting-started/kubernetes/requirements)及[IP 自动探测说明](https://docs.tigera.io/calico/latest/networking/ipam/ip-autodetection)。如需 VXLAN、自定义 MTU 或其他环境设置，请提供经过验证的本地 `manifest`。此覆盖路径不改写内容、不执行 Calico 专用就绪检查，但仍等待所有预期节点 Ready。

新节点已有 `.conf` / `.conflist` / `.json` CNI 配置时拒绝自动替换。重跑会核对目标 Calico 版本，不自动更换网络或 IP 池；已存在 IP 池不会因改环境变量而迁移。所有就绪等待共用部署总超时。内部节点协议已更新为 v2，异构架构的 `nodeBinaries` 必须重新从同一份代码构建。`--dry-run` 不下载、不连接 SSH；本地 `kubeadm init` 维护入口仍只接受已准备的本地清单，不单独触发自动下载。

已验证官方完整清单 31 个对象的渲染语义、摘要、模拟下载/分发、版本和 CIDR 拒绝路径及超时/错误传播；尚未在真实集群验证 Calico 网络连通性和策略执行。

### SSH 直接连接（KK 方式）

直接运行 `opskit k8s create`，使用 `hosts.password` / `passwordEnv`、私钥或 SSH Agent 登录。无需提前执行 ssh，不依赖管理用户的 known_hosts，也没有首次指纹确认提示。sudo 密码仍由对应 sudo 配置传入。错误的 SSH 凭据仍会认证失败。

实现与所核对的 [KubeKey SSH 连接器](https://github.com/kubesphere/kubekey/blob/99699fa9c30102f3941768b205a6bb612dbba348/pkg/connector/ssh_connector.go#L185) 一致，使用 `ssh.InsecureIgnoreHostKey()`，因此不会验证服务端身份或阻止主机指纹变化。原有 known_hosts 文件保持不动。`k8s.deploy.knownHostsFile` 已移除，包含该字段的旧配置会报未知字段，需手动删除。

`--dry-run` 不连接 SSH。此方式同样适用于非交互和批量运行；不会额外从终端询问 SSH 或 sudo 密码。已通过本地 SSH 测试覆盖缺少/损坏/不匹配 known_hosts 时正常连接、不修改该文件、正确密码认证、错误密码拒绝、sudo stdin 和私密文件上传；尚未进行真实生产节点部署验收。

Calico 的 `install.calico.fallback_bases` 为可选根地址列表，主源下载失败（包括每源 60 秒超时）后依次尝试；共享部署总超时。所有源使用相同固定 SHA-256，摘要不匹配立即停止，不回退。未配置或 `[]` 不启用备用源；本地清单不走下载回退。Nexus 是代理缓存，需当前网络可达，首次请求可能回源。

使用 `k8s.registry.imageRepository: registry.k8s.io` 时，生成的 kubeadm ClusterConfiguration 显式包含 `dns.imageRepository: registry.k8s.io/coredns`，使 CoreDNS 使用 `registry.k8s.io/coredns/coredns:<版本>`。该路径已在目标节点 kubeadm v1.24.7 的 `config images list` 中验证；Harbor 对应 `google_containers/coredns/coredns:v1.8.6` manifest 返回 200。自定义 imageRepository 不自动追加此路径。无需忽略 ImagePull 预检或 reset 节点。

SSH 部署日志不再按 SSH/sudo 密码内容做全局替换，避免短密码误伤版本号、IP 和节点名；集群加入 token 仍脱敏。密码仍通过 SSH 认证或 sudo 标准输入传递，不加入远端命令参数；日志如包含程序自身输出的密码文本，将按原文显示。

## Kubernetes 组件自动配套版本

只需配置 `k8s.kubernetes.version` 和 `containerManager`，不必填写六份组件版本。`k8s create --version` 同样参与默认版本选择。可选的 `k8s.<组件>.version` 仍优先于配套默认值，且继续执行兼容校验；独立安装命令保留 `--version`。

| Kubernetes | crictl |
| --- | --- |
| 1.24 | v1.24.2 |
| 1.25 | v1.25.0 |
| 1.26 | v1.26.1 |
| 1.27 | v1.27.1 |
| 1.28 | v1.28.0 |
| 1.29 | v1.29.0 |
| 1.30 | v1.30.1 |

运行时按 [KubeKey 组件表](https://github.com/kubesphere/kubekey/blob/main/docs/en/installation/components.md) 的 Docker 内置 containerd/runc 配套组合选择（2026-10-19 查阅快照，固定在代码中，不运行时抓取）：

| Kubernetes | containerd | runc | Docker | cri-dockerd | CNI plugins |
| --- | --- | --- | --- | --- | --- |
| 1.24～1.29 | v1.7.28 | v1.3.3 | v28.5.2 | v0.4.7 | v1.9.1 |
| 1.30 | v2.2.6 | v1.3.6 | v29.6.2 | v0.4.7 | v1.9.1 |

KK 用 Docker 包内置的 containerd/runc 组合确定分组默认值，使两种运行时选择有对应的版本栈；本项目采用相同组合。crictl 保持上表与 Kubernetes 同 minor 的固定版本。只安装选定运行时所需组件，日志展示解析后的版本。超出本项目支持范围报错，不使用 latest 或自动升级已有节点。自动 Calico 仍仅支持 Kubernetes 1.24～1.28，1.29～1.30 需提供本地网络清单。

这些新配套组合仅完成版本解析、配置和现有兼容规则的自动化验证，尚未全部真机验收；此前 v1.24.7 + containerd v1.7.24 的部署结果不能替代新组合验收。已有配置可保留显式版本以沿用原组合；删除覆盖项后使用新表，现存节点版本不一致时仍拒绝自动升级或覆盖。

安装文件下载遇到 HTTP 502/503/504 时自动重试，最多共 3 次，间隔 2 秒、4 秒，受命令总超时约束；404、认证失败、文件传输中断和摘要不匹配不自动重试，避免拼接损坏文件。持续 504 仍需检查 Nexus 回源与反向代理超时；安装器不会绕过 SHA-256 校验或擅自换版本。
