<p align="center">
  <img src="docs/assets/banner.svg" alt="opskit：运维工具，随取随用" width="100%">
</p>

<p align="center">
  <a href="https://github.com/sxxpqp/opskit/actions/workflows/ci.yml"><img src="https://github.com/sxxpqp/opskit/actions/workflows/ci.yml/badge.svg" alt="Build and tests"></a>
  <a href="https://github.com/sxxpqp/opskit/releases/latest"><img src="https://img.shields.io/github/v/release/sxxpqp/opskit" alt="Latest release"></a>
  <a href="https://github.com/sxxpqp/opskit/releases"><img src="https://img.shields.io/github/downloads/sxxpqp/opskit/total" alt="Release downloads"></a>
  <a href="https://github.com/sxxpqp/opskit/stargazers"><img src="https://img.shields.io/github/stars/sxxpqp/opskit?style=flat" alt="GitHub stars"></a>
</p>

<p align="center">
  <strong>一个统一工具安装、Kubernetes 部署、健康检查与数据备份的 Go 运维工具箱。</strong><br>
  Go / Docker / containerd / kubectl · Kubernetes SSH / kubeadm / Calico · kind / KubeSphere · etcd / S3
</p>

<p align="center">
  <a href="#快速上手">安装与上手</a> ·
  <a href="#kubernetes-集群部署">Kubernetes 部署</a> ·
  <a href="#工具与运行时安装">工具安装</a> ·
  <a href="#镜像源与下载">镜像源</a> ·
  <a href="#检查与备份">检查与备份</a> ·
  <a href="#下载">下载</a> ·
  <a href="docs/configuration.md">配置指南</a> ·
  <a href="#近期更新">近期更新</a>
</p>

<!-- LATEST_RELEASE_START -->
**当前最新正式版：[v0.1.13](https://github.com/sxxpqp/opskit/releases/tag/v0.1.13)**

Kubernetes 组件自动配套、前置依赖自动安装、sudo 复用 SSH 密码、同版本 kubectl 复用、CoreDNS 路径修复、Calico 备用源、502/503/504 重试及 Go 阿里云下载源。

→ [选择系统与架构下载](#下载) · [使用 Nexus 备用入口](#下载) · [查看近期更新](#近期更新)
<!-- LATEST_RELEASE_END -->

## opskit 能做什么

opskit 将工具安装、Kubernetes 部署、镜像源设置、健康检查和备份收进同一个 Go 命令行工具，通过 YAML 管理节点、版本和下载源。以下说明对应当前 main；下载版本的具体变化见对应 Release。

| 能力 | 当前支持 |
|---|---|
| Kubernetes 部署 | 原生 SSH + kubeadm，单控制平面与可选工作节点、自动加入、Calico 安装及 Ready 检查 |
| 组件版本配套 | 按 Kubernetes 次版本选择固定运行时组合，支持显式覆盖与兼容校验 |
| 工具安装 | Go SDK、kubectl、kind，指定版本、下载校验与命令入口配置 |
| 容器运行时 | Docker 脚本／二进制、containerd + runc + CNI plugins、cri-dockerd |
| kind 与 KubeSphere | kind 创建／状态／日志／删除／context 切换；KubeSphere v3.4.1 安装 |
| 镜像源 | Docker、containerd、Go、npm、pip、Maven，备份后写入，支持预览 |
| 检查与备份 | etcd 成员和健康检查、快照及 SHA-256 清单；S3 桶／对象检查及上传 |
| 命令行与交付 | Cobra 中文帮助、Shell 补全；Windows／Linux／macOS 的 amd64／arm64 构建 |

## 快速上手

### 使用 Go 安装

已有 Go 环境可直接安装，当前代码要求 Go 1.26.2 或更新工具链：

```sh
go install github.com/sxxpqp/opskit/cmd/opskit@latest
opskit version
opskit config init
```

`@latest` 安装 Go 模块解析到的最新可用版本，并非 main 分支。固定版本安装可使用 `@v0.1.13`。后续更新可再次执行安装命令。

命令安装到 `go env GOBIN` 指定目录；未设置时使用 `go env GOPATH` 下的 `bin`（通常为 `~/go/bin`，Windows 为 `%USERPROFILE%\go\bin`），请将其加入 PATH。Go 构建需能访问模块源及依赖。

### 下载可执行文件

以 Linux amd64 为例，其他系统和 Nexus 备用入口见[下载表](#下载)：

```sh
curl -fL https://github.com/sxxpqp/opskit/releases/download/v0.1.13/opskit_linux_amd64 -o opskit
chmod +x opskit
./opskit version
./opskit config init
```

按对应 Release 的 `checksums.txt` 核对 SHA-256。生成配置后编辑 `config/config.yaml`，再执行所需命令；模板中的节点、私有代理地址、证书路径和凭据需要按环境填写。

### 常用命令

```sh
opskit --help
opskit k8s create --help
opskit k8s create --dry-run
opskit k8s create
opskit kubectl install --version v1.24.7
opskit go install --version 1.26.2
opskit mirror containerd --dry-run
opskit etcd check --timeout 30s
opskit s3 check --timeout 15s
opskit etcd backup
```

安装、集群创建与 mirror 默认执行，预览使用 `--dry-run`，不接受旧 `--apply`。`k8s create --dry-run` 只检查配置、展示计划，不连接 SSH 或下载安装。

查看命令组帮助用 `opskit kind --help`，具体参数用 `opskit kind create --help`。Shell 补全支持 `opskit completion bash|zsh|fish|powershell`，任选一个 Shell；加载方法见[配置指南](docs/configuration.md#cobra-命令与-shell-补全)。

## 配置文件

```sh
opskit config init                         # 生成 config/config.yaml，不覆盖已有文件
opskit config init --config ./staging.yaml
opskit k8s create --config ./staging.yaml --dry-run
```

- 从当前工作目录读取 `config/config.yaml`；默认文件不存在时回退 `opskit.yaml`，不合并两份文件。
- 优先级为显式参数 > 对应组件支持的环境变量 > YAML > 内置模板默认值；支持项见[配置指南](docs/configuration.md)。
- YAML 中相对路径以配置文件所在目录为准；命令行相对路径以当前工作目录为准。
- 拒绝未知字段、重复字段和多个 YAML 文档。更新模板不会改写已有配置，可生成新文件后对照迁移。
- 版本按安装类型隔离：`go.version`、`kind.version`／`kind.kubernetes.version`、`k8s.kubernetes.version`。

完整模板：[config.example.yaml](config.example.yaml)。下载源统一在 `install`，容器镜像源在 `mirrors`。请勿将真实密码、密钥或 kubeconfig 提交到仓库。

## Kubernetes 集群部署

不依赖或调用 KK；使用类似 KK 的 hosts、roleGroups 和组件版本配套方式。只需指定 Kubernetes 版本与运行时，kubeadm／kubelet／kubectl 使用同一版本：

```yaml
k8s:
  name: sxxpqp
  hosts:
    - {name: master1, address: 192.168.10.10, internalAddress: 192.168.10.10, port: 22, user: kh, passwordEnv: MASTER1_PASSWORD}
  roleGroups:
    etcd: [master1]
    master: [master1]
    # worker: [worker1, worker2]
  controlPlaneEndpoint:
    address: 192.168.10.10
    port: 6443
  kubernetes:
    version: v1.24.7
    containerManager: containerd
    clusterName: cluster.local
```

这是完整模板中 `k8s` 的部分字段，请在生成的配置上修改。启用 worker 时同时添加对应 hosts 和角色成员。支持密码、私钥和 SSH Agent；普通用户 sudo 默认复用 SSH 密码，可通过 `sudoPassword`／`sudoPasswordEnv` 覆盖；没有可用密码时要求免密 sudo。当前 SSH 不检查主机指纹，不读写 known_hosts。

### 自动配套版本

| Kubernetes | containerd | runc | Docker |
|---|---|---|---|
| 1.24～1.29 | v1.7.28 | v1.3.3 | v28.5.2 |
| 1.30 | v2.2.6 | v1.3.6 | v29.6.2 |

CNI plugins 固定 v1.9.1，cri-dockerd 固定 v0.4.7；crictl 按 Kubernetes 次版本选择。运行时使用 KK 组件表的 Docker 内置 containerd／runc 配套组合，固定在代码中，不运行时查询 latest。可选 `k8s.<组件>.version` 覆盖默认值，仍检查兼容性；完整表见[配套版本指南](docs/configuration.md#kubernetes-组件自动配套版本)。

### 部署流程与边界

1. 全节点检查 SSH、sudo、Linux amd64／arm64、systemd 和已有集群保护。
2. 管理机按架构下载并校验组件，准备网络清单。
3. 使用节点现有 apt-get／dnf／yum 安装缺失系统依赖，再分发二进制、准备内核参数与运行时镜像源。
4. 初始化控制平面、安装网络、自动加入工作节点，等待 Calico 与配置节点 Ready。

```sh
opskit k8s create --dry-run
opskit k8s create
# 部署完成后，使用管理机保存的配置
export KUBECONFIG="$HOME/.opskit/clusters/sxxpqp/kubeconfig"
kubectl get nodes -o wide
```

`network.manifest` 留空时安装固定 Calico v3.26.5，校验官方 SHA-256、按 Pod CIDR 渲染并等待就绪；当前自动路径仅支持 Kubernetes 1.24～1.28。1.29～1.30 或其他网络设置需提供经过验证的本地清单。存储驱动需单独准备，KubeSphere 默认关闭。

仅支持单控制平面、同节点 etcd 和可选工作节点，不提供 HA、升级或集群删除。修改已有集群的目标版本／网络会被阻止；`k8s create` 不能代替升级或 reset。同版本的独立 kubectl 可复用，其他半安装或不受管理的集群状态会停止部署。

Linux 同架构管理机可分发当前 opskit；macOS／Windows 或异构管理需提供相同代码构建的 `k8s.deploy.nodeBinaries`。管理机保存阶段日志和 kubeconfig，不覆盖用户默认 kubeconfig。系统包安装依赖节点网络及软件源，已有组件不主动升级。

`opskit kubeadm install|prepare|init|join` 为本地维护入口，具体操作与参数见[部署指南](docs/configuration.md#k8s-ssh-集群部署)。已有 K8s v1.24.7 + containerd v1.7.24 单节点与 Calico Ready 的实际运行记录；新配套版本、多节点网络、策略及生产恢复仍需验收。

## 工具与运行时安装

所有安装入口保留显式 `--version`；默认直接执行，预览追加 `--dry-run`。

```sh
opskit go install --version 1.26.2
opskit kubectl install --version v1.24.7
opskit kind install --version v0.17.0
# 以下运行时路线按需选择，不要在同一节点重复安装
opskit containerd install --version v1.7.28 --runc-version v1.3.3 --cni-version v1.9.1
opskit docker install --version v28.5.2 --method binary
opskit cri-dockerd install --version v0.4.7
```

- Go 安装完整 SDK，校验发布 SHA-256 和实际版本，创建 go／gofmt 入口，并设置 `mirrors.go` 中的 GOPROXY。
- kind／kubectl 支持版本目录、命令入口和 PATH 检查；Windows 复制 exe，Unix 创建链接。Go／kubectl／kind 各自支持的平台以命令帮助为准；kind 不支持 Windows ARM64。
- 运行时安装要求 Linux + systemd，拒绝覆盖已有运行时；Docker 支持官方脚本和静态包。Docker 安装会配置 inotify 最低限额，保留更高值。
- 独立运行时安装与 `k8s create` 的职责不同：主机网络准备、Calico 和集群初始化由部署流程处理。

安装参数、校验限制与入口路径见[配置指南](docs/configuration.md)。

## kind 与 KubeSphere

```sh
opskit kind install --version v0.17.0
opskit kubectl install --version v1.24.7
opskit kind create --version v1.24.7 --workers 2 --dry-run
opskit kind create --version v1.24.7 --workers 2
opskit kind status
opskit kind logs
opskit kind use
opskit kind unuse
opskit kind delete --dry-run
```

kind 使用配置中的名称，`--name` 可覆盖；已有同名集群不自动重建。模板映射宿主机 `0.0.0.0` 上的 80／443／30880，端口可配置，Ingress Controller 需另行部署。

```sh
opskit mirror containerd --kind kubesphere-341
opskit kubesphere install --version v3.4.1 --kind kubesphere-341 --dry-run
opskit kubesphere install --version v3.4.1 --kind kubesphere-341
```

自建集群不传 `--kind`，默认读取 `KUBECONFIG` 或 `~/.kube/config` 的 current-context，支持显式 `--kubeconfig`／`--context`。KS 当前使用内嵌 v3.4.1 清单，要求 Kubernetes 1.20～1.26、节点 Ready、cluster-admin 以及可用 StorageClass；不自动安装存储。已有实例不覆盖，安装器、core 状态和核心 Deployment 共用总超时（默认 45 分钟）。

初始密码通过 `k8s.kubesphere.password` 或 kind 对应配置设置，要求大小写字母和数字；成功摘要会显示配置的登录密码，请保护安装日志。不会生成密码文件。详细说明见[配置指南](docs/configuration.md)。

## 镜像源与下载

```sh
opskit mirror docker --dry-run
opskit mirror docker
opskit mirror containerd
opskit mirror go
opskit mirror npm
opskit mirror pip
opskit mirror maven
```

镜像源统一读取顶层 `mirrors`。containerd 保留各 registry 的 endpoint、路径和 TLS 设置；Docker 使用 `mirrors.docker`／`docker_insecure_registries`。`k8s.registry.imageRepository` 仅指定 kubeadm 系统镜像仓库，不替代运行时代理配置。

mirror 默认备份后写入。标准 Linux Docker 配置写入后自动重启并检查服务；宿主机 containerd 单独执行 mirror 后需安排重启，`k8s create` 会自动重启选定运行时。kind 节点逐台重启并检查 CRI。Go／npm／pip／Maven 配置不会改写当前终端的环境变量。

安装文件源统一由 `install.<组件>` 配置，CLI 或组件环境变量可覆盖。模板的 Go SDK 包使用阿里云源、校验元数据使用 Go 官方地址；Kubernetes 二进制和 GitHub 发布包使用现有 Nexus 代理。内网地址需当前环境可达，Nexus 是上游代理缓存，首次请求可能回源。

Calico 支持 `install.calico.download_base` 主源及 `fallback_bases` 备用根地址列表，下载失败依次切换，单源最多 60 秒，摘要不匹配立即停止。通用下载遇到 HTTP 502／503／504 最多尝试 3 次，等待 2 秒、4 秒，并受命令总超时约束；持续 504 仍需排查代理回源。

下载保留在 `install.download_dir`（默认 `~/.opskit/downloads`），但安装仍重新下载并校验，不将旧文件直接作为可信缓存。配置示例和证书选项见[下载源指南](docs/configuration.md#下载源配置)。

## 检查与备份

```sh
opskit etcd check --timeout 30s --probe-timeout 5s
opskit s3 check --timeout 15s
opskit s3 check --key manual/snapshot.db --timeout 15s
opskit etcd backup
opskit s3 upload --key manual/snapshot.db ./snapshot.db
```

etcd 检查报告成员、投票节点、learner、健康节点、Leader 和版本；S3 检查访问桶与可选对象，HEAD 检查不证明写入权限。输出 JSON，部分节点异常也会返回非零退出码。字段与退出码见[检查指南](docs/checks.md)。

etcd 快照依赖单独安装且版本匹配的 `etcdctl`。模板为 kubeadm 常见控制平面提供证书路径，其他发行版或外置 etcd 需自行调整。每个任务保存独立 `snapshot.db` 和 `manifest.json`，记录大小、SHA-256 和上传状态；失败快照保留 `.partial`，上传失败保留完整本地快照。

通过 `s3.provider: minio|aws|custom` 选择厂家，只填写对应配置块。bucket 留空仅本地备份，填写后自动上传；支持块内凭据或 AWS SDK 默认凭据链。上传默认禁止覆盖，完成后 HEAD 核对长度和 SHA-256 metadata；这不等于内容回读验证。模板不自动创建桶，也不清理旧备份。

```sh
# 快照状态与恢复仅在隔离环境验收
etcdutl snapshot status ./snapshot.db -w table
etcdutl snapshot restore ./snapshot.db --data-dir ./restore-test
```

真实 etcd／S3 恢复演练尚未验收。配置与凭据说明见[配置指南](docs/configuration.md)。SSH 部署日志不按密码内容全局替换，版本号和 IP 会正常显示；加入 token 仍脱敏，日志若包含命令自身输出的敏感内容需自行保护。

## 下载

**[下载最新正式版](https://github.com/sxxpqp/opskit/releases/latest)** · [历史版本与发布说明](https://github.com/sxxpqp/opskit/releases)

按系统和架构下载单个可执行文件，无需解压。Linux／macOS 需 `chmod +x opskit`；Windows 文件名为 `opskit.exe`。x86_64 指 64 位，不提供 32 位 x86 包。下载表和顶部版本摘要由正式发布工作流维护。

<!-- DOWNLOAD_LINKS_START -->
### v0.1.13 下载：GitHub / Nexus 备用

同一行的主链接与备用链接指向同一版本。按系统和 CPU 架构下载，无需解压。

| 系统 | CPU 架构 | GitHub | Nexus 备用 |
|---|---|---|---|
| Windows | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.13/opskit_windows_amd64.exe) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.13/opskit_windows_amd64.exe) |
| Windows | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.13/opskit_windows_arm64.exe) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.13/opskit_windows_arm64.exe) |
| Linux | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.13/opskit_linux_amd64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.13/opskit_linux_amd64) |
| Linux | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.13/opskit_linux_arm64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.13/opskit_linux_arm64) |
| macOS | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.13/opskit_darwin_amd64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.13/opskit_darwin_amd64) |
| macOS | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.13/opskit_darwin_arm64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.13/opskit_darwin_arm64) |

校验文件：[GitHub](https://github.com/sxxpqp/opskit/releases/download/v0.1.13/checksums.txt) · [Nexus](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.13/checksums.txt)

配置模板：[GitHub](https://github.com/sxxpqp/opskit/releases/download/v0.1.13/config.example.yaml) · [Nexus](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.13/config.example.yaml)

Nexus 为上游代理缓存，首次请求需回源；不是独立备份。首页链接随正式发布自动更新为最新版本的固定路径，避免 latest 缓存造成版本混淆。
<!-- DOWNLOAD_LINKS_END -->

## 验证范围与后续计划

本地自动化已覆盖版本解析、配置优先级、下载摘要、临时网关错误重试、Calico 备用源、SSH／sudo 和部署编排，完整 `go test -race ./...` 已通过。单节点实际结果不等于完整生产验收，新运行时组合和多机部署仍需验证。

- [ ] 新配套版本的 Linux 端到端部署、多节点连通性和网络策略验收
- [ ] HA、升级和集群删除能力
- [ ] 启动检查新版本并交互确认更新（[设计计划](go-opskit-design.md#14-2026-10-18-启动更新提示计划尚未实现)）
- [ ] 真实 etcd／S3 集成测试、隔离恢复及备份保留策略
- [ ] S3 下载、列举和内容回读校验

长期方向见[设计文档](go-opskit-design.md)和[M0 技术设计基线](docs/m0-design-baseline.md)，设计内容不代表已实现功能。

## 近期更新

| 功能版本 | 变化 |
|---|---|
| [v0.1.13](https://github.com/sxxpqp/opskit/releases/tag/v0.1.13) | Kubernetes 组件自动配套、前置依赖自动安装、sudo 复用 SSH 密码、同版本 kubectl 复用、CoreDNS 路径修复、Calico 备用源、502/503/504 重试及 Go 阿里云下载源 |
| [v0.1.12](https://github.com/sxxpqp/opskit/releases/tag/v0.1.12) | 历史发布详情见 Release 说明 |
| [v0.1.11](https://github.com/sxxpqp/opskit/releases/tag/v0.1.11) | 历史发布详情见 Release 说明 |
| main（文档更新） | 补充 go install 安装方式，重整首页导航、配置与功能说明 |

新标签只有在发布流水线通过后才作为正式 Release 提供下载。下载配置见 [downloads.json](downloads.json)，代理说明见[Nexus 下载指南](docs/downloads.md)。

## 本地开发与参与

Go 版本以 [go.mod](go.mod) 为准：

```sh
go build -o opskit ./cmd/opskit
go test ./...
go test -race ./...
go vet ./...
```

创建并推送新的 `v*` 标签后由流水线执行测试、构建和发布；本地创建 tag 不会触发发布，已有标签不应重复覆盖。CI 的平台范围和具体流程以[工作流目录](.github/workflows)为准。

欢迎[报告问题](https://github.com/sxxpqp/opskit/issues/new/choose)或提交 PR；请附系统、架构、版本与复现步骤，并移除密码、密钥和业务数据。开发约定见[贡献指南](CONTRIBUTING.md)。
