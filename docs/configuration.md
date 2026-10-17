# 统一配置与生产模板

main 分支默认读取 `config/config.yaml`，通过 `opskit config init` 创建；正式版 v0.1.5 仍使用 `opskit.yaml`。默认文件不存在时回退读取旧 `./opskit.yaml`，两份文件不会合并；显式 `--config FILE` 不回退。各相对路径始终基于实际配置文件所在目录，因此迁移旧配置时请核对相对路径。

版本集中在 `versions.kind` / `versions.kubernetes` / `versions.kubectl` / `versions.kubesphere`；镜像源集中在 `mirrors.docker` / `containerd` / `go` / `npm` / `pip` / `maven`。安装或创建命令的 `--version` 覆盖对应版本项。kind 名称、worker 数量、kubeconfig、工具路径和映射端口放在 `kind` 下；KS 存储和初始密码放在 `kubesphere` 下。完整带注释模板见 `config.example.yaml`。

这些命令写入持久配置文件，不修改当前终端的环境变量；Go/pip/npm 等工具自身的 GOPROXY、PIP_INDEX_URL、NPM_CONFIG_REGISTRY 环境变量仍可能优先于文件，使用前请核对。

`mirror` 命令默认写入并保留备份，`--dry-run` 仅预览；Linux 标准 `/etc/docker/daemon.json` 写入后自动重启并检查 Docker 服务，自定义文件和宿主机 containerd 仍需手动重启。模板镜像源已按 linux 项目原配置填充：Docker 多前端、containerd huball 和现有公共语言源。部署到其他环境时需替换地址；模板更新不会覆盖本地已生成配置。containerd 主机配置识别 1.x 的 CRI registry 与 2.x 的 images registry，未知结构或旧认证/TLS 配置会停止；Go/npm/pip/Maven 只修改本用户对应配置文件，支持 `--file`。保留已有 Maven 自定义 mirrors，需人工合并；不自动关闭证书校验。

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

`kind.kubeconfig`、`kind.binary`、KS storage_class 留空属于自动定位行为，不是缺失默认值。生成对照模板可使用 `opskit config init --config config/config.new.yaml`，不会自动覆盖已有生产配置。

## 运行时版本与安装源（main，未发布）

Docker、cri-dockerd、containerd 的 install 命令默认直接执行，不接受 `--apply`；需要预览时添加 `--dry-run`。

`versions.docker`、`cri_dockerd`、`containerd`、`runc`、`cni_plugins` 均为完整 vX.Y.Z；安装命令的 `--version` 优先于对应配置。containerd 另支持 `--runc-version` 和 `--cni-version`。Docker 27.3.1、cri-dockerd 0.3.22、containerd 2.1.3、CNI 1.5.1 来自 linux 项目；runc 固定为 1.2.6，不沿用原脚本的 1.1.10，不做 glibc 失败时静默降级。此版本组合尚未做真机兼容验收。

`install.docker.method` 为 script 或 binary，`install.docker.mirror` 为官方脚本的软件包源（默认清华 URL，也支持 Aliyun / AzureChinaCloud），和 `mirrors.docker` 的镜像拉取源分别设置。脚本源 `install.docker.script` 默认通过 Nexus 代理获取固定提交的官方脚本；二进制源 `install.docker.binary_base` 按 x86_64/aarch64 拼接 Docker 静态包。`install.containerd.download_base`、`install.runc.download_base`、`install.cni_plugins.download_base`、`install.cri_dockerd.download_base` 分别用于对应组件，默认使用项目 Nexus GitHub release 代理；可改成 https://github.com。

`--url` 只覆盖主包/脚本，`--sha256` 只校验该文件；runc/CNI 无单独摘要字段，使用源站 HTTPS。安装不改既有配置、不升级现有运行时，不自动修改主机网络参数。完整示例见首页运行时安装章节。既有 config/config.yaml 不自动覆盖；用 `opskit config init --config config/config.new.yaml` 生成对照模板，手工合并新字段。

安装下载统一使用 `install.download_dir`；模板留空，默认用户目录下的 `.opskit/downloads`，不随工作目录变化。配置非空相对路径仍按配置文件目录解析；CLI `--download-dir` 优先，按当前目录解析。既有显式配置不自动改变，想使用新默认值请将 download_dir 设为 ""。

kind 安装优先复用 PATH 中已有且可写的目录：先检查各平台常用目录，再按 PATH 顺序检查其他目录；不采用空项、相对路径、文件、不存在或不可写的目录。复用成功不修改 shell 配置或 Windows 用户 PATH。没有可用目录才使用平台默认入口并设置用户 PATH。`--link-dir` 可显式指定入口目录。平台默认目录：Linux root /usr/local/bin、普通用户 ~/.local/bin；macOS 普通用户优先 PATH 中可写的 /opt/homebrew/bin 或 /usr/local/bin；Windows 为 LOCALAPPDATA/opskit/bin，未设置 LOCALAPPDATA 时使用用户目录 AppData/Local/opskit/bin。Unix 使用 symlink，Windows 复制 exe。Bash 维护 ~/.bashrc 及已有优先登录配置（.bash_profile > .bash_login > .profile），Zsh 维护 ZDOTDIR/.zshrc 或 ~/.zshrc，sh/dash/ksh 维护 ~/.profile；已在当前 PATH 中则不写启动配置。追加前备份，不改已有行。Windows 写入 User 范围的 PATH，不修改 Machine 范围。需要新终端读取；安装日志同时给出当前终端立即生效命令。其他 shell 报错提示自行配置，已有其他来源的 kind 入口不覆盖。安装后会运行入口 version，并检查当前 PATH 是否实际命中此入口；旧命令遮挡返回错误，未加载新 PATH 时输出待生效。Windows 保存用户 PATH 后回读确认；用户 shell 的 alias/function 及完整新终端启动结果仍须在该终端验收。

`kind create` 默认执行创建，版本仍由 `versions.kubernetes` 或 CLI `--version` 指定；`--dry-run` 仅输出配置，不检查外部工具、不调用 Docker/kind/kubectl。实际创建前继续检查依赖、Docker、kind 工具版本及同名集群；已有集群不会删除或覆盖。创建命令不接受旧 `--apply`，删除集群默认执行，集群名按 `--name` > `OPSKIT_KIND_NAME` > `kind.name` > 默认值解析，无需重复确认名称，`--dry-run` 仅预览。

## kubectl 安装（main，未发布）

`opskit kubectl`、`opskit kubectl -h`、`opskit kubectl --help` 显示说明；`opskit kubectl install -h/--help` 显示全部参数。帮助不依赖配置或外部工具。

```yaml
versions:
  kubernetes: v1.24.7
  kubectl: ""  # 留空跟随 kubernetes，也可明确固定独立版本
install:
  kubectl:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries
  download_dir: ""  # 默认用户目录 .opskit/downloads
```

优先级为 CLI `--version` > versions.kubectl > versions.kubernetes；缺少完整版本时报错，不隐式选择 latest。下载源优先级为 `--kubectl-download-base` > 环境变量 `KUBECTL_DOWNLOAD_BASE` > install.kubectl.download_base > 模板默认 Nexus 代理；空环境变量不覆盖 YAML。默认使用 Nexus `kubernetes-binaries` 代理。官方直连可选择 `https://dl.k8s.io/release` 或 `https://storage.googleapis.com/kubernetes-release/release`。URL 格式为 `<base>/<version>/bin/<os>/<arch>/kubectl[.exe]`，校验文件追加 `.sha256`；不跳过 TLS、摘要或安装后的版本检查。

默认直接安装；`--dry-run` 只输出计划。安装目录按版本隔离，支持 `--bin-dir`，命令目录支持 `--link-dir`；下载及 PATH 沿用 kind 的共用实现。验证客户端 JSON 版本，不读取或连接 kubeconfig。SHA/HTTP/客户端版本校验失败时保留已有版本文件；已有其他来源的 PATH 入口不覆盖。测试覆盖原生测试程序的完整下载安装、重复安装、失败保护和帮助入口，真实集群访问仍需实际验收。

## kind Ingress 入口端口

`kind.http_port: 80`、`kind.https_port: 443` 分别映射到控制平面节点容器 80/443，绑定 `0.0.0.0`；CLI `--http-port` / `--https-port` 优先于 YAML。`kind.console_port` 默认 30880，映射 KubeSphere 节点端口 30880 并绑定 `0.0.0.0`，CLI 可用 `--console-port` 覆盖宿主机端口；已有集群需重建后生效。端口范围为 1～65535，三项不能重复；被其他服务占用时须更换宿主机端口。

创建前用 `opskit kind create --dry-run` 检查生成的 extraPortMappings。控制平面设置 `ingress-ready=true`，Ingress Controller 需另行部署到此节点并监听节点 80/443（例如 hostPort/hostNetwork）；端口映射不自动安装 Controller 或创建 Ingress 对象。已核对 kind v0.17.0 配置类型支持这些字段。已有集群的 Docker 映射不会变更，新版 opskit 不会自动删除旧集群；需使用新集群名或自行安排重建。实际集群 Ingress 连通性尚未验收。

下载源变量示例：`export KUBECTL_DOWNLOAD_BASE=https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries`；用 `unset KUBECTL_DOWNLOAD_BASE` 取消覆盖。PowerShell 用 `$env:KUBECTL_DOWNLOAD_BASE = "..."`。已有配置若仍显式填写 dl.k8s.io 会继续使用该值，应按需修改配置或使用环境变量覆盖。Nexus 代理不保证在所有网络或任意版本可用；缺少目标文件时返回错误，不静默更换版本。

下载源命名统一：CLI `--kubectl-download-base`、环境变量 `KUBECTL_DOWNLOAD_BASE`、YAML `install.kubectl.download_base`。旧 `--download-base` 已移除，传入会报未知参数。命名变更只针对 kubectl，不改变 kind 的 --download-base。

## 下载源配置

| 下载内容 | YAML 配置项 |
|---|---|
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

清华地址是 Docker CE 软件包仓库，不是脚本下载地址，也不是 Docker Hub 镜像源。清华文档的脚本仍来自 GitHub；本项目模板使用既有 Nexus raw 代理映射，固定官方脚本提交，实际可达性需目标环境验证。不能访问该代理时，将 `install.docker.script` 改为你可访问的官方脚本托管地址。Docker 目标版本仍由 `versions.docker` / `--version` 指定。

## kind 下载源分组

kind 安装文件的下载地址使用组件独立配置：

```yaml
versions:
  kind: v0.17.0
install:
  kind:
    download_base: https://nexus.ihome.sxxpqp.top:8443/repository/raw-github
```

CLI `--download-base` > `KIND_DOWNLOAD_BASE` > `install.kind.download_base` > 模板默认值。版本仍由 `--version` 或 `versions.kind` 指定，不自动使用 latest。旧 `install.kind_download_base` 已移除，需迁移到上述嵌套结构。

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

例如 v1.24.7 的 Linux amd64 会请求 `<base>/v1.24.7/bin/linux/amd64/kubectl` 和 `kubectl.sha256`。下载、SHA-256 校验及执行 `kubectl version` 的安装检查保持不变。版本仍用 `--version v1.24.7` 或 `versions.kubectl` 固定，不取 latest。

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

KS 初始账号为 `admin`；`kubesphere.password` 默认 `sxxpqp.top`，`--password` 可覆盖。直接注入安装配置，不创建本地密码文件。旧 `password_file` 和 `--password-file` 已移除，需迁移为 `password`。仅影响全新安装，不会修改已安装集群的账号密码。密码长度为 8～64 字节；真实 KS 登录尚未验证。

KS 安装总超时默认 45 分钟，`--timeout 60m` 可延长。core enabled 后先将三个核心 Deployment 的 `progressDeadlineSeconds` 设为该总超时的秒数，再检查 rollout，避免服务端进度期限先于客户端等待结束。整个流程仍受同一个总期限约束。
