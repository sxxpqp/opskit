# 统一配置与生产模板

main 分支默认读取 `config/config.yaml`，通过 `opskit config init` 创建；正式版 v0.1.5 仍使用 `opskit.yaml`。默认文件不存在时回退读取旧 `./opskit.yaml`，两份文件不会合并；显式 `--config FILE` 不回退。各相对路径始终基于实际配置文件所在目录，因此迁移旧配置时请核对相对路径。

版本集中在 `versions.kind` / `versions.kubernetes` / `versions.kubesphere`；镜像源集中在 `mirrors.docker` / `containerd` / `go` / `npm` / `pip` / `maven`。安装或创建命令的 `--version` 覆盖对应版本项。kind 名称、worker 数量、kubeconfig、工具路径和映射端口放在 `kind` 下；KS 存储和密码文件路径放在 `kubesphere` 下。完整带注释模板见 `config.example.yaml`。

这些命令写入持久配置文件，不修改当前终端的环境变量；Go/pip/npm 等工具自身的 GOPROXY、PIP_INDEX_URL、NPM_CONFIG_REGISTRY 环境变量仍可能优先于文件，使用前请核对。

`mirror` 命令默认预览，`--apply` 才写入并保留备份；模板镜像源已按 linux 项目原配置填充：Docker 多前端、containerd huball 和现有公共语言源。部署到其他环境时需替换地址；模板更新不会覆盖本地已生成配置。containerd 主机配置识别 1.x 的 CRI registry 与 2.x 的 images registry，未知结构或旧认证/TLS 配置会停止；Go/npm/pip/Maven 只修改本用户对应配置文件，支持 `--file`。保留已有 Maven 自定义 mirrors，需人工合并；不自动关闭证书校验。

kind、mirror、KS 的显式参数优先于对应环境变量，其次 YAML 和固定默认值；etcd/S3 保留已有参数及 AWS 默认凭据链语义。非空 YAML 账号信息仍按原规则优先于 AWS 环境凭据。请勿把全局的配置优先级理解成秘密管理机制。


完整字段说明在 [config.example.yaml](../config.example.yaml)。以下示例均使用当前实现支持的字段。

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

`provider` 支持 `minio`、`aws`、`custom`，只加载对应配置块，不回退到其他厂家。未填写 region 时默认 `us-east-1`；MinIO/custom 默认 path-style，AWS 默认虚拟主机方式。MinIO/custom 在启用上传时必须有 endpoint。其他 S3 厂家可使用 `custom`，按其文档设置 endpoint、region、path_style，兼容性仍需实际验证。

旧版平铺的 `s3.bucket` / `s3.endpoint` 等仍可使用，但不能与 `provider` 混用。迁移时将原字段移入对应厂家块，再设置 `provider`。已有 opskit.yaml 不会被自动覆盖，可用 `opskit config init --config opskit.new.yaml` 生成新模板对照迁移。

## 项目默认值来源

- Docker：linux 仓库 `docker/docker-mirror.sh`，包括内网 insecure-registry 列表；写入时合并保留既有条目。
- containerd：`docker/containerd/mirrors.sh`，包含 DockerHub、两个 Google registry 名称、Quay、GHCR 与阿里 ACR 直连。
- Go/npm/pip：`docs/infra-reference.md` 和已使用的 CI/Dockerfile；Go 支持逗号/竖线分隔的代理链及 direct/off。
- Maven：`aliyun-mirrors.md` 中的公开聚合源。
- kind KS 安装器：原 `kubernetes/kubesphere/kind-3.4.1` 使用的公开 3.4.1 镜像源；自建集群不读取这个 kind 专用模板项。

`kind.kubeconfig`、`kind.binary`、KS storage_class/password_file 留空属于自动定位行为，不是缺失默认值。生成对照模板可使用 `opskit config init --config config/config.new.yaml`，不会自动覆盖已有生产配置。

## 运行时版本与安装源（main，未发布）

Docker、cri-dockerd、containerd 的 install 命令默认直接执行，不接受 `--apply`；需要预览时添加 `--dry-run`。

`versions.docker`、`cri_dockerd`、`containerd`、`runc`、`cni_plugins` 均为完整 vX.Y.Z；安装命令的 `--version` 优先于对应配置。containerd 另支持 `--runc-version` 和 `--cni-version`。Docker 27.3.1、cri-dockerd 0.3.22、containerd 2.1.3、CNI 1.5.1 来自 linux 项目；runc 固定为 1.2.6，不沿用原脚本的 1.1.10，不做 glibc 失败时静默降级。此版本组合尚未做真机兼容验收。

`install.docker_method` 为 script 或 binary，`docker_mirror` 为官方脚本的软件包源（Aliyun / AzureChinaCloud），和 `mirrors.docker` 的镜像拉取源分别设置。脚本源 `docker_script` 默认 get.docker.com；二进制源 `docker_binary_base` 按 x86_64/aarch64 拼接 Docker 静态包。`release_base` 用于 containerd、runc、CNI 和 cri-dockerd，默认使用项目 Nexus GitHub release 代理；可改成 https://github.com。

`--url` 只覆盖主包/脚本，`--sha256` 只校验该文件；runc/CNI 无单独摘要字段，使用源站 HTTPS。安装不改既有配置、不升级现有运行时，不自动修改主机网络参数。完整示例见首页运行时安装章节。既有 config/config.yaml 不自动覆盖；用 `opskit config init --config config/config.new.yaml` 生成对照模板，手工合并新字段。

安装下载统一使用 `install.download_dir`；模板留空，默认用户目录下的 `.opskit/downloads`，不随工作目录变化。配置非空相对路径仍按配置文件目录解析；CLI `--download-dir` 优先，按当前目录解析。既有显式配置不自动改变，想使用新默认值请将 download_dir 设为 ""。

kind 安装优先复用 PATH 中已有且可写的目录：先检查各平台常用目录，再按 PATH 顺序检查其他目录；不采用空项、相对路径、文件、不存在或不可写的目录。复用成功不修改 shell 配置或 Windows 用户 PATH。没有可用目录才使用平台默认入口并设置用户 PATH。`--link-dir` 可显式指定入口目录。平台默认目录：Linux root /usr/local/bin、普通用户 ~/.local/bin；macOS 普通用户优先 PATH 中可写的 /opt/homebrew/bin 或 /usr/local/bin；Windows 为 LOCALAPPDATA/opskit/bin，未设置 LOCALAPPDATA 时使用用户目录 AppData/Local/opskit/bin。Unix 使用 symlink，Windows 复制 exe。Bash 维护 ~/.bashrc 及已有优先登录配置（.bash_profile > .bash_login > .profile），Zsh 维护 ZDOTDIR/.zshrc 或 ~/.zshrc，sh/dash/ksh 维护 ~/.profile；已在当前 PATH 中则不写启动配置。追加前备份，不改已有行。Windows 写入 User 范围的 PATH，不修改 Machine 范围。需要新终端读取；安装日志同时给出当前终端立即生效命令。其他 shell 报错提示自行配置，已有其他来源的 kind 入口不覆盖。安装后会运行入口 version，并检查当前 PATH 是否实际命中此入口；旧命令遮挡返回错误，未加载新 PATH 时输出待生效。Windows 保存用户 PATH 后回读确认；用户 shell 的 alias/function 及完整新终端启动结果仍须在该终端验收。

`kind create` 默认执行创建，版本仍由 `versions.kubernetes` 或 CLI `--version` 指定；`--dry-run` 仅输出配置，不检查外部工具、不调用 Docker/kind/kubectl。实际创建前继续检查依赖、Docker、kind 工具版本及同名集群；已有集群不会删除或覆盖。创建命令不接受旧 `--apply`，删除集群仍需 `--apply --confirm-name`。
