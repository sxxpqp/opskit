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
  <a href="#k8s-ssh-集群部署main未发布">K8s 集群部署</a> ·
  <a href="#go-安装main未发布">Go 安装</a> ·
  <a href="#运行时安装main未发布">运行时安装</a> ·
  <a href="#kubectl-安装main未发布">kubectl 安装</a> ·
  <a href="#快速上手">快速上手</a> ·
  <a href="#下载">下载</a> ·
  <a href="#配置文件">配置文件</a> ·
  <a href="#连通性与集群健康检查">健康检查</a> ·
  <a href="#etcd-备份">备份指南</a> ·
  <a href="#近期更新">近期更新</a> ·
  <a href="#路线图">路线图</a> ·
  <a href="https://github.com/sxxpqp/opskit/issues/new/choose">反馈与建议</a>
</p>

<!-- LATEST_RELEASE_START -->
**当前最新正式版：[v0.1.11](https://github.com/sxxpqp/opskit/releases/tag/v0.1.11)**

新版本已发布，详细变化见对应 Release 说明。

→ [选择系统与架构下载](#下载) · [使用 Nexus 备用入口](#下载) · [查看近期更新](#近期更新)
<!-- LATEST_RELEASE_END -->

## 为什么做 opskit

工具安装、Kubernetes 部署、镜像源配置和集群备份经常散落在不同脚本里。opskit 把这些操作收进统一的命令行入口，通过 YAML 管理版本、节点和下载源，让服务器、个人电脑与自动化任务共用一套配置。

当前代码覆盖 **Go / kubectl / Docker / cri-dockerd / containerd 版本安装、原生 SSH + kubeadm 集群部署、固定版本 Calico、kind 管理和 KubeSphere 安装**，并支持 **etcd 健康检查与快照、MinIO / AWS / 自定义 S3 检查及上传**。命令使用 Cobra，提供中文帮助和 Shell 补全。

**v0.1.11 为本次代码标签，尚未发布。** 新增安装与部署流程已通过本地自动化验证，真实多机部署、Calico 连通性和网络策略仍需验收；SSH 部署目前支持单控制平面与可选工作节点，未实现该部署方式的 HA、升级或集群删除。配置统一为 `config/config.yaml`，旧 `opskit.yaml` 仅在默认文件不存在时回退读取，不合并。下载表仍对应已发布版本，GitHub / Nexus 入口与正式版摘要由发布流水线维护。

命令入口已迁移至 Cobra，帮助页从实际命令和参数生成，按行展示中文说明；`opskit kind --help` 等命令组帮助列出可选子命令。命令帮助支持 `opskit -h` / `--help`；命令组如 `opskit kind`、`opskit kind -h`、`opskit kind --help` 均显示子命令列表，其他已有命令组同样适用。具体参数使用 `opskit kind install --help` 等形式查看，帮助不依赖配置文件或外部工具。

支持 `opskit help kind`、`opskit kind help` 和 `opskit kind create --help`。参数统一使用 `--参数名`；布尔值显式关闭使用 `--dry-run=false`，不是 `--dry-run false`。子命令之间用空格，帮助中的 `|` 表示任选一个。S3 上传支持将参数放在文件名前后。

生成 Shell 补全脚本：

```sh
opskit completion bash > opskit.bash
opskit completion zsh > _opskit
opskit completion fish > opskit.fish
opskit completion powershell > opskit.ps1
```

根据所用 Shell 加载生成的脚本；生成脚本和查看帮助均不读取用户配置或执行安装。完整方法见[命令与补全指南](docs/configuration.md#cobra-命令与-shell-补全)。

## 现在能做什么

| 能力 | 当前行为 |
|---|---|
| Cobra CLI（main，未发布） | 原生命令树、自动生成中文帮助、参数校验与 Bash/Zsh/Fish/PowerShell 补全 |
| Go 安装（main，未发布） | 完整版本 SDK、SHA-256 / 实际版本校验、go/gofmt 入口，安装后自动设置 GOPROXY |
| 运行时安装（main，未发布） | Docker 官方脚本 / 二进制、cri-dockerd、containerd + runc + CNI；指定版本、默认执行、--dry-run 预览 |
| kubectl 安装（main，未发布） | 按版本与系统架构下载，SHA-256 与客户端版本校验，复用 PATH；支持 -h / --help |
| kind 管理（main，未发布） | 版本安装与跨系统 PATH 配置、集群创建/状态/日志、context 切换及按配置删除 |
| 统一镜像源（main，未发布） | Docker、containerd、Go、npm、pip、Maven；默认备份后写入，--dry-run 预览 |
| K8s SSH（main，未发布） | SSH/密码/密钥/sudo、下载复用、单 Master+Worker 编排；依赖版本自动匹配方案（1.24～1.37，1.29+ CNI 须手动提供），未完成全版本真实安装验收 |
| kubeadm（main，未发布） | install/prepare/init/join，containerd 兼容检查、systemd、单控制平面和工作节点；未真机验收 |
| KubeSphere（main，未发布） | 3.4.1 清单内嵌，兼容性/存储检查、初始密码注入、核心服务等待 |
| etcd 集群检查 | 成员数量、投票节点 / learner、逐节点健康、Leader 与版本 |
| S3 连通性 | 检查桶访问，区分 HTTP 权限错误与连接失败，可选对象 HEAD |
| etcd 快照 | 调用官方 `etcdctl`，支持 CA、客户端证书与任务超时 |
| 统一配置 | main 默认读取 `./config/config.yaml`；兼容旧 `./opskit.yaml`，内置模板一条命令生成 |
| 备份清单 | 为每次任务生成独立目录，记录文件大小、SHA-256 和上传状态 |
| 厂家配置 | `provider: minio / aws / custom`，仅填写选中厂家的配置块 |
| S3 上传 | 内置 Go SDK，支持配置凭据、环境变量或默认凭据链 |
| 失败保留 | 上传失败仍保留完整本地快照，便于排查和重试 |
| 跨平台交付 | Windows / Linux / macOS，amd64 与 arm64，直接下载可执行文件 |
| 备用下载 | GitHub / Nexus 同版本双入口，发布成功后自动刷新首页版本与链接 |

S3 兼容服务的后端能力需实际验证。etcd 备份需要单独安装匹配版本的 `etcdctl`；当前尚未完成真实环境的恢复验收。

## 快速上手

以 Linux x86_64 为例，下载后运行；其他平台及 **Nexus 备用下载**见[下载表](#下载)。首页随正式发布切换版本，刷新页面后选择对应链接即可。

```sh
curl -fL https://github.com/sxxpqp/opskit/releases/latest/download/opskit_linux_amd64 -o opskit
chmod +x opskit
./opskit version
./opskit config init
# 编辑 opskit.yaml：核对 etcd 证书，选择 S3 厂家并填好对应配置块。

# 检查集群有几个节点、哪些健康、谁是 Leader
./opskit etcd check --timeout 30s

# 配置好 bucket 后检查 S3 桶访问
./opskit s3 check --timeout 15s

# 备份；所选厂家的 bucket 非空时自动上传
./opskit etcd backup
```

模板已预填 kubeadm 常见 etcd 证书路径；其他部署方式请按实际环境修改，且需单独安装匹配版本的 `etcdctl`。只做本地备份时将 bucket 留空，跳过 S3 检查。下载文件可先按 Release 附带的 `checksums.txt` 校验。

## 常用命令

| 目的 | 命令 |
|---|---|
| 查看版本 | `opskit version` |
| 安装 kubectl（main） | `opskit kubectl install --version v1.24.7` |
| 安装指定 kind 版本（main） | `opskit kind install --version v0.17.0` |
| 创建 / 预览集群（main） | `opskit kind create --version v1.24.7` / 追加 `--dry-run` |
| 修改 kind 镜像源（main） | `opskit mirror containerd --kind kubesphere-341` |
| 安装 KS（main） | `opskit kubesphere install --version v3.4.1 --kind kubesphere-341` |
| 生成当前目录配置 | `opskit config init` |
| 检查 etcd 集群 | `opskit etcd check --timeout 30s` |
| 检查 S3 桶连通性 | `opskit s3 check --timeout 15s` |
| 检查 S3 已有对象 | `opskit s3 check --key existing/snapshot.db --timeout 15s` |
| 备份 etcd，按配置上传 | `opskit etcd backup` |
| 单独上传文件 | `opskit s3 upload --key manual/snapshot.db ./snapshot.db` |

main 中所有命令默认读取当前目录的 `config/config.yaml`（旧 Release 仍使用 `opskit.yaml`）；需要时在子命令后追加 `--config ./other.yaml`。检查输出为 JSON，部分节点异常也会返回非零退出码。S3 HEAD 检查不会测试写入权限。

## kind / mirror / KS（main，尚未发布）

这些功能从 linux 仓库中的运维流程迁入已有 `cmd/opskit` 入口；etcd/S3 命令继续可用。当前正式版 v0.1.5 不包含这些命令，请先从 main 构建。

```sh
./opskit config init                    # 生成 config/config.yaml；不覆盖已有文件
# 模板已带本项目镜像源；确认这些内网域名在当前环境可达
./opskit kind install --version v0.17.0
./opskit kubectl install --version v1.24.7
./opskit kind create --version v1.24.7 --workers 2 --dry-run  # 仅预览
./opskit kind create --version v1.24.7 --workers 2            # 实际创建
./opskit mirror containerd --kind kubesphere-341 --dry-run  # 预检全部节点，不写入
./opskit mirror containerd --kind kubesphere-341
# 确认集群已有可供卷的默认 StorageClass；本工具不会安装存储驱动
./opskit kubesphere install --version v3.4.1 --kind kubesphere-341 --dry-run
./opskit kubesphere install --version v3.4.1 --kind kubesphere-341
./opskit kind status
./opskit kind logs
# 删除配置中的 kind 集群；追加 --dry-run 仅预览
./opskit kind delete
```

新建 kind 集群默认在控制平面映射 `0.0.0.0:80 → 节点80`、`0.0.0.0:443 → 节点443`，并设置 `ingress-ready=true` 标签。KS 控制台映射 `0.0.0.0:30880 → 节点30880`。可在 `kind.http_port`、`kind.https_port` 配置宿主机端口，或传 `--http-port 8080 --https-port 8443`。端口须互不重复、未被其他宿主机服务占用。此配置只建立 Docker 端口映射，Ingress Controller 需另行部署到控制平面并通过 hostPort/hostNetwork 监听节点 80/443。已有集群不会自动修改映射或删除重建；需自行安排备份后重建，或用新集群名创建。已核对 kind v0.17.0 配置类型支持节点 labels 和 extraPortMappings；尚未做真实 Ingress 请求验收。

`kind create` 默认实际创建，不需要 `--apply`；仅预览时显式使用 `--dry-run`。`kind create --version` 指 Kubernetes 节点版本，`kind install --version` 指 kind 工具版本，`kubesphere install --version` 指 KS 版本。模板提供经过确认的 kind v0.17.0 / K8s v1.24.7 / KS v3.4.1 组合；创建时若配置指定 kind 版本，将验证实际工具版本一致。已有同名集群会报错并保留，不自动删除、重建或调整拓扑。kind 命令仍需 Docker、kubectl；安装 kind 本身无需 Go。

KS 安装等待共用总超时（默认 45 分钟，可用 `--timeout 45m` 延长）。KS 安装默认直接安装，`--dry-run` 仅做只读预检；读取 `KUBECONFIG` 或 `~/.kube/config` 的 current-context，也可用 `--kubeconfig FILE --context NAME` 显式指定；自建集群不传 `--kind`；各节点 mirror 请先执行 `mirror containerd` 配置。原生 SSH 节点部署见下方 K8s 部署指南。KS 仅支持已核对的内嵌 3.4.1 清单，校验 K8s 1.20～1.26、节点 Ready、cluster-admin 和唯一默认 StorageClass。已有 `kubesphere-system` 时停止，不覆盖实例和密码。完成条件是核心 enabled 和三个核心 Deployment 就绪，监控需另查状态。初始密码从 `k8s.kubesphere.password` 读取并直接注入，预检要求包含大小写字母和数字，可用 `--password` 覆盖，不创建密码文件；ARM 上游尚未完全支持，需要自行验证所选组件镜像。

`mirror docker/containerd/go/npm/pip/maven` 共用 `mirrors` 配置；不再将 mirror 归属于 kind。默认备份后写入，`--dry-run` 仅预览。Linux 标准 Docker 配置写入后自动重启并检查服务；自定义 Docker 配置文件和宿主机 containerd 需手动安排重启；kind 节点逐台重启并检查 CRI、验证 DockerHub 拉取。Maven 已有自定义 mirrors 或 containerd 旧认证配置时要求人工合并，不覆盖未知配置。

```sh
./opskit mirror docker                         # 写入配置并重启 Linux Docker 服务
./opskit mirror go --url https://your-go-proxy.example.com
./opskit mirror npm --url https://your-npm.example.com
./opskit mirror pip --url https://your-pypi.example.com/simple
./opskit mirror maven --url https://your-maven.example.com/repository
```

这些命令写入持久配置文件，不修改当前终端的环境变量；Go/pip/npm 等工具自身的 GOPROXY、PIP_INDEX_URL、NPM_CONFIG_REGISTRY 环境变量仍可能优先于文件，使用前请核对。

Go 使用 `go env GOENV` 定位配置（也可 `--file`）；npm、pip、Maven 使用当前用户配置，支持 `--file` 指定。Docker Desktop 要显式指定导出的配置文件，不修改 Docker Desktop 内部虚拟机。模板迁入了 linux 项目现有默认值：Docker 多前端、containerd huball 六份映射、Go/npm/清华 PyPI/阿里 Maven 公共源。内网代理需在当前环境可达；`docker_insecure_registries` 与 containerd 的 `skip_verify` 沿用原项目证书设置，可按实际可信 CA 情况调整。源地址仍只在配置文件或 `--url` 中维护。优先级为显式参数 > 支持的环境变量 > YAML > 固定默认值；环境变量目前支持 KIND_DOWNLOAD_BASE、OPSKIT_KIND_VERSION、OPSKIT_KUBERNETES_VERSION、OPSKIT_KUBESPHERE_VERSION、OPSKIT_KIND_NAME、OPSKIT_KIND_BIN、KUBECONFIG。`kind use/unuse` 修改用户默认 kubeconfig 并备份，目标可用 OPSKIT_USER_KUBECONFIG 指定；不会修改终端环境变量。

```sh
go build -o opskit ./cmd/opskit
./opskit kind install --version v0.17.0
./opskit kind install --version v0.33.0
# 自定义安装目录（相同目录切换版本前会保留旧二进制备份）
./opskit kind install --version v0.17.0 --bin-dir /opt/kind-v0.17.0
```

版本必须由 `--version` 或统一配置的 `kind.version` 明确提供，不隐式选择 latest。默认安装位置为 `~/.local/share/opskit/kind/<版本>/kind`，Windows 为 `kind.exe`；日志展示版本和实际路径。不同版本目录可共存。安装后先查找 PATH 中已有且可写的目录创建 kind 命令入口，优先各平台常用目录，再按 PATH 顺序检查其他目录；跳过空项、相对路径、不存在的目录及不可写目录。找到后不修改 PATH 配置。找不到时使用平台默认目录：Linux root 使用 `/usr/local/bin`，普通用户使用 `~/.local/bin`；macOS 普通用户使用 `~/.local/bin`（已有 PATH 中的 `/opt/homebrew/bin`、`/usr/local/bin` 优先）；Windows 使用 `%LOCALAPPDATA%\opskit\bin`（未设置 LOCALAPPDATA 时按用户目录推导）。可用 `--link-dir` 覆盖。目录不在 PATH 时，Bash/Zsh/sh 自动备份并追加用户启动配置，Windows 通过 PowerShell 写入用户 PATH；当前终端无法由子进程修改，会打印立即生效的命令，新终端读取持久配置。其他 shell 会报出 PATH 尚未配置并提示人工处理。已有同版本安装也会补建入口；拒绝覆盖其他来源的 kind。Windows 复制 exe，无需符号链接权限。安装结束执行入口版本及 PATH 命中检查：命中其他 kind 时返回错误；当前终端尚未加载 PATH 时明确输出“PATH 待生效”，不宣称验证通过。Windows 写入 User PATH 后回读确认。自动化验收包含新 Bash/Zsh 进程，Windows CI 额外在临时 runner 中测试用户 PATH 写入、回读和幂等并恢复原值；这不替代用户终端中 alias/function 的检查。

kind 发布包支持 Linux/macOS amd64、arm64 和 Windows amd64；Windows 原生 ARM64 的 kind 安装暂不支持（opskit 自身仍可构建该平台）。kind 安装器源默认沿用已有公开 `registry.cn-beijing.aliyuncs.com/kubesphereio/ks-installer:v3.4.1`，仅用于 `--kind` 预加载，不改上游清单；自建集群忽略该模板项。kind 工具发布包默认通过现有 Nexus `raw-github` 代理下载；可用配置 `install.kind.download_base`、`--download-base https://github.com` 或环境变量 `KIND_DOWNLOAD_BASE` 显式选择来源。安装器读取统一配置；运行不依赖 Bash、curl 或 Go。`kind install` 只安装 kind 工具，其他动作使用相应子命令。

旧 `linux` 仓库脚本在 PATH 中找到 opskit（或设置 `OPSKIT_BIN`）时会调用本实现，并传递原版本和安装目录；未部署 opskit 的机器继续使用旧脚本。测试覆盖版本共存、重复安装、SHA/版本校验失败和 HTTP 失败保留原文件；新增工作流测试通过模拟工具验证命令顺序、预检阻断和秘密保护；macOS 做了安装器原生执行检查，尚未完成真实集群 KS 安装和 Windows/Linux 真机工作流验收。

## 下载

**[下载最新版本](https://github.com/sxxpqp/opskit/releases/latest)** · [查看历史版本与更新说明](https://github.com/sxxpqp/opskit/releases)

公开附件无需登录 GitHub，选好系统和 CPU 架构即可下载。GitHub 下载不便时，可使用下表的 **Nexus 备用下载**。每次正式发布完成后，工作流自动将下表更新到 GitHub 标记的最新版本；使用版本专属地址，不经过 Nexus 的 latest 别名。

<!-- DOWNLOAD_LINKS_START -->
### v0.1.11 下载：GitHub / Nexus 备用

同一行的主链接与备用链接指向同一版本。按系统和 CPU 架构下载，无需解压。

| 系统 | CPU 架构 | GitHub | Nexus 备用 |
|---|---|---|---|
| Windows | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.11/opskit_windows_amd64.exe) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.11/opskit_windows_amd64.exe) |
| Windows | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.11/opskit_windows_arm64.exe) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.11/opskit_windows_arm64.exe) |
| Linux | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.11/opskit_linux_amd64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.11/opskit_linux_amd64) |
| Linux | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.11/opskit_linux_arm64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.11/opskit_linux_arm64) |
| macOS | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.11/opskit_darwin_amd64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.11/opskit_darwin_amd64) |
| macOS | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.11/opskit_darwin_arm64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.11/opskit_darwin_arm64) |

校验文件：[GitHub](https://github.com/sxxpqp/opskit/releases/download/v0.1.11/checksums.txt) · [Nexus](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.11/checksums.txt)

配置模板：[GitHub](https://github.com/sxxpqp/opskit/releases/download/v0.1.11/config.example.yaml) · [Nexus](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.11/config.example.yaml)

Nexus 为上游代理缓存，首次请求需回源；不是独立备份。首页链接随正式发布自动更新为最新版本的固定路径，避免 latest 缓存造成版本混淆。
<!-- DOWNLOAD_LINKS_END -->

这里的 x86 指 64 位 x86_64，不提供 32 位 x86 安装包。下载后将文件重命名为 `opskit`（Windows 为 `opskit.exe`）；Linux/macOS 执行 `chmod +x opskit` 后运行，无需解压。请使用上表同版本的 SHA-256 校验文件核对完整性。

## 配置文件

模板更新不会覆盖已有的 `config/config.yaml`。需要对照新默认值时执行 `opskit config init --config config/config.new.yaml`，检查后合并；程序仍只读取选中的一份配置，不自动合并新旧模板。

在准备运行命令的目录生成配置模板：

```sh
./opskit config init
# main 编辑 config/config.yaml；正式版 v0.1.5 编辑 opskit.yaml，然后运行：
./opskit etcd backup
```

模板默认采用 **kubeadm 控制平面节点的本机 etcd 地址与证书路径**，逐项说明备份目录、TLS、MinIO/S3 和三种 key 的区别。其他 Kubernetes 发行版或外置 etcd 需修改实际路径。完整示例见 [Kubernetes + MinIO/S3 配置指南](docs/configuration.md)。也可查看仓库的 [配置模板](config.example.yaml)，或从 [最新版本附件](https://github.com/sxxpqp/opskit/releases/latest) 下载 `config.example.yaml` 后按所用版本放置为 `config/config.yaml`（main）或 `opskit.yaml`（v0.1.5）。二进制内置同一份模板，无需额外下载即可生成。

**读取规则：命令行参数 > 配置文件 > 内置默认值。**

- main 从**当前工作目录**查找 `config/config.yaml`，不存在时回退旧 `opskit.yaml`，不合并；不是可执行文件所在目录。
- 默认文件不存在时仍可使用命令行参数；使用 `--config` 显式指定的文件不存在则报错。
- 配置文件中的相对路径按配置文件所在目录解析；命令行中的相对路径按工作目录解析。
- 配置拒绝未知字段、重复字段、多个 YAML 文档及无效超时，帮助尽早发现拼写错误。
- `config init` 不覆盖已有文件；发现旧 `opskit.yaml` 时要求先迁移或显式选择模板目标。`config/config.yaml` 与 `opskit.yaml` 已加入 `.gitignore`；访问密钥可配置在所选厂家块，也可通过环境变量或 SDK 凭据链传入；请勿提交真实凭据。

```sh
# 生成模板到指定位置（父目录需已存在）
./opskit config init --config ./staging.yaml

# 使用指定配置，并覆盖其中的任务超时
./opskit etcd backup --config ./staging.yaml --timeout 10m

# S3 连接信息从配置读取，文件名放在所有选项之后
./opskit s3 upload --config ./staging.yaml --key manual/snapshot.db ./snapshot.db
```

通过 `s3.provider` 选择 `minio`、`aws` 或 `custom`，只需修改对应配置块。填写所选块的 `bucket` 后，`etcd backup` 会自动上传；默认模板的 bucket 留空，仅保存在本地。实际运行前请核对模板地址与证书路径；访问凭据可填在所选块的 `access_key` / `secret_key`，或将它们留空，使用 `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`。块内的 `key` 是桶内文件名，二者不同。

## 连通性与集群健康检查

main 复用 `config/config.yaml`，旧版本复用 `opskit.yaml`，或用 `--config` 指定其他文件：

```sh
# 检查选中厂家的 S3 桶（不上传文件、不创建桶）
./opskit s3 check --timeout 15s

# 同时检查某个已有对象是否可访问；--key 为空时仅检查桶
./opskit s3 check --key manual/snapshot.db --timeout 15s

# 从 etcd 成员列表发现节点，逐个检查状态与健康
./opskit etcd check --timeout 30s --probe-timeout 5s

# 可提供多个种子 endpoint，便于一个地址故障时通过其他地址发现成员
./opskit etcd check --endpoint https://10.0.0.1:2379,https://10.0.0.2:2379 --timeout 30s
```

输出为带缩进的 JSON，便于直接阅读或交给监控脚本。检查通过返回 0；探测失败或部分节点异常返回 1；参数错误返回 2；缺少 etcdctl 返回 3；总超时或取消返回 4。

**S3**：报告 `service_reachable`、`bucket_access`、HTTP 状态和耗时。有 key 时额外检查对象。403 表示收到服务响应但访问被拒绝，404 标记为未找到或被隐藏，均不会直接断言网络故障。此检查使用 HEAD，`write_access` 始终为 `not-tested`，无法证明上传权限；HeadBucket 权限不足也不代表没有 PutObject 权限。连接失败或凭据不可用时，`service_reachable: false` 表示未确认收到服务响应。

**etcd**：报告成员总数、投票节点数、learner 数、健康节点数、所需多数票和观察到的 quorum，以及每个节点的 ID、名称、地址、Leader 标记、版本、数据库大小、revision、Raft term。会尝试各成员通告的客户端地址，因此执行机器必须能连接这些地址。没有通告客户端地址的成员、不可达节点、身份不匹配和健康探测失败都会标记异常；有 quorum 但节点不全健康时仍返回失败。结果是逐节点探测快照，不能替代持续监控或数据一致性校验。

成员发现与每个节点的 status + health 共用各自的 `--probe-timeout`；总期限由 `--timeout` 限制。配置中的 timeout 仍可覆盖内置默认值，检查任务建议显式传较短超时。etcd 检查不会执行快照或上传文件，不依赖 S3 endpoint/凭据是否完整。

更多字段与限制见 [检查指南](docs/checks.md)。

## etcd 备份

```sh
opskit etcd backup --endpoint https://127.0.0.1:2379 \
  --cacert ./ca.crt --cert ./client.crt --key-file ./client.key \
  --directory ./data/backups/etcd --timeout 30m
```

只指定一个 endpoint；也可使用 `ETCDCTL_ENDPOINTS`、`ETCDCTL_CACERT`、`ETCDCTL_CERT`、`ETCDCTL_KEY`、`ETCDCTL_USER` 等 etcdctl 环境变量。密码不要放在命令参数里。工具直接执行 etcdctl，不经过 shell。默认保留 TLS 验证。快照命令失败时不会转发 etcdctl 原始 stderr，避免诊断输出泄露可能包含的敏感连接信息；请检查 etcd 连接、TLS 凭据及权限。

每个任务使用独立目录，保存 `snapshot.db` 与 `manifest.json`（大小、SHA-256、时间、上传状态）。失败快照保留 `.partial`；上传失败保留完整本地备份并返回退出码 5。当前不自动清理备份。

```sh
# 快照备份后上传；默认生成唯一的 etcd/<task>/snapshot.db key
opskit etcd backup --endpoint https://127.0.0.1:2379 \
  --bucket backups --s3-endpoint https://s3.example.com --path-style

# 单独上传已有文件，也可用于上传失败后的重试（使用新 key）
opskit s3 upload --bucket backups --key manual/snapshot.db \
  --s3-endpoint https://s3.example.com --path-style ./snapshot.db
```

S3 凭据优先使用所选厂家配置块的 `access_key` / `secret_key`；留空时使用 AWS SDK 默认凭据链，例如 `AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY`、可选 `AWS_SESSION_TOKEN` 或工作负载角色。支持 AWS S3、MinIO 等 S3 兼容服务（需实际验证后端能力），不自动创建 bucket。默认 region 为 `us-east-1`，可用 `--region` 修改。所有参数须放在位置文件参数前。

上传采用分段上传，默认禁止覆盖已存在对象，要求后端支持条件写；不支持时应报错。上传后 HEAD 核对长度和 SHA-256 metadata，验证数据对象后才上传远端 manifest。metadata 验证不代表远端内容回读校验。若上传完成但 HEAD 或清单失败，请检查远端已有对象，重试时指定新 key。任务总超时包含快照和上传。

## 校验和恢复演练

```sh
etcdutl snapshot status ./snapshot.db -w table
# 仅在隔离环境恢复，勿指向正在运行的生产数据目录
etcdutl snapshot restore ./snapshot.db --data-dir ./restore-test
```

快照流程依据 [etcd 官方恢复文档](https://etcd.io/docs/v3.6/op-guide/recovery/)。本项目尚未完成真实 etcd/MinIO 环境的恢复验收，生产使用前应进行恢复演练并核对业务数据。

下载文件统一保存在 `install.download_dir`：模板留空，默认用户目录下的 `~/.opskit/downloads/`（Windows 为 `%USERPROFILE%\.opskit\downloads`），不随项目工作目录变化，可通过 `--download-dir` 覆盖。kind、Docker 脚本/静态包、cri-dockerd、containerd/runc/CNI 按组件、版本、源 URL 摘要分目录保留；安装时仍重新下载并执行原有校验，不把已有文件当成可信缓存。临时解压文件安装结束后清理。

## kubectl 安装（main，未发布）

```bash
opskit kubectl                      # 命令说明；也支持 -h / --help / help
opskit kubectl install -h           # 安装参数；也支持 --help
opskit kubectl install              # 默认执行，版本跟随统一配置
opskit kubectl install --version v1.24.7
opskit kubectl install --version v1.24.7 --dry-run
```

版本优先级为 `--version` > `k8s.kubernetes.version` > `k8s.kubernetes.version`，均须明确为 vX.Y.Z，不读取 latest。自动识别 Linux/macOS amd64、arm64 和 Windows amd64，下载对应 kubectl（Windows 为 kubectl.exe）。Windows ARM64 当前不支持安装该客户端。代码按 K8s 工具放在 `internal/k8s`，命令保持独立。

先校验上游 SHA-256，再运行 `kubectl version --client=true --output=json` 验证客户端版本，成功后安装到 `~/.local/share/opskit/kubectl/<版本>/`。下载保留在统一 `.opskit/downloads` 目录；Unix 创建软链接，Windows 复制 exe。命令入口复用 PATH 中可写目录，找不到时才配置用户 PATH；已有其他来源的 kubectl 不覆盖。安装后检查实际 PATH 命中与客户端版本，新终端待生效时明确提示。安装不连接集群、不修改 kubeconfig，实际集群操作仍应选择匹配服务端的客户端版本。

kubectl 的 `install.kubectl.download_base` 支持官方、Nexus 和具有相同目录结构的阿里云 / OSS 镜像。默认使用 Nexus 代理 `https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries`。阿里云 apt/yum 的 deb/rpm 仓库不能直接作为裸二进制下载源；本次探测的两个阿里云裸二进制候选路径返回 404，因此不列为可用源。配置方法见 [配置指南](docs/configuration.md#kubectlnexus-与阿里云镜像)。下载源优先级：`--kubectl-download-base` > `KUBECTL_DOWNLOAD_BASE` > YAML > 模板默认值，已有显式配置不会自动覆盖。

```bash
export KUBECTL_DOWNLOAD_BASE=https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries
opskit kubectl install --version v1.24.7
# 临时覆盖（与环境变量采用同一命名）
opskit kubectl install --version v1.24.7 --kubectl-download-base https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries
# 取消环境变量覆盖
unset KUBECTL_DOWNLOAD_BASE
```

kubectl 已移除旧参数 `--download-base`，只接受 `--kubectl-download-base`。配置键 `install.kubectl.download_base`、环境变量 `KUBECTL_DOWNLOAD_BASE`、主参数 `--kubectl-download-base` 统一命名，不需要同时设置三个入口。

Windows PowerShell 可用 `$env:KUBECTL_DOWNLOAD_BASE = 'https://nexus.ihome.sxxpqp.top:8443/repository/kubernetes-binaries'`。自建源需要保持同样路径结构并同时提供二进制和 `.sha256`。`--bin-dir`、`--link-dir` 和 `--download-dir` 分别覆盖版本目录、命令入口目录和下载目录。配置详见[配置指南](docs/configuration.md)。

## Go 安装（main，未发布）

```sh
opskit go install --version 1.26.2 --dry-run
opskit go install --version 1.26.2
# 临时覆盖代理；不指定时读取 mirrors.go
opskit go install --version 1.27.2 --proxy https://goproxy.cn,direct
```

支持 Linux/macOS amd64、arm64。版本读取独立的 `go.version`，CLI 优先；模板固定为 1.26.2，不自动选择 latest。完整 SDK 安装到 `~/.local/share/opskit/go/go<版本>`，校验发布元数据中的 SHA-256 和实际 `go version` 后创建 `go`、`gofmt` 命令入口，按现有安装流程配置 PATH。已有 SDK 目录或其他来源的命令入口不会覆盖；同版本重装需指定新的 `--install-dir`。

安装后将 `mirrors.go` 写入当前用户 `go env GOENV` 指向的文件，保留其他配置并备份；后续改代理可运行 `opskit mirror go`。若以 root 执行则配置 root 用户。`GOENV=off` 会导致代理设置失败，已有 `GOPROXY` 环境变量仍优先，已有 `GOROOT` 需自行调整。下载根地址为 `install.go.download_base`，校验元数据为 `install.go.checksum_url`，配置与参数见[配置指南](docs/configuration.md#go-sdk-安装与代理)。已通过本地模拟下载安装测试，尚未完成真实 SDK 安装验收。

## 运行时安装（main，未发布）

Docker 两种安装方式都会在启动安装前备份并更新 `/etc/sysctl.d/99-inotify.conf`，将 `fs.inotify.max_user_instances` 提升到至少 8192、`fs.inotify.max_user_watches` 提升到至少 524288，保留已有更高值，应用后回读验证。`--dry-run` 仅展示计划。已有 Docker 仍不重复安装；现有机器可手动应用这两个参数。

Linux systemd 主机使用；macOS/Windows 可以预览，实际安装需进入 Linux VM 或启用 systemd 的 WSL2。运行时安装命令默认直接执行，添加 `--dry-run` 仅预览；安装目标版本写在 `config/config.yaml` 的 `k8s.docker.version`、`k8s.criDockerd.version`、`k8s.containerd.version` 等组件项下，CLI 可覆盖。

```bash
# 默认使用官方脚本和清华 Docker CE 软件包源；预览可追加 --dry-run
opskit docker install --version v27.3.1
# Docker 静态二进制：包含 dockerd 自带的 containerd/runc
opskit docker install --version v27.3.1 --method binary
# 使用 Docker 作为 Kubernetes 运行时时再装 CRI 适配器
opskit cri-dockerd install --version v0.3.22
# 或单独安装 containerd（与上面的 Docker 路线二选一）
opskit containerd install --version v2.1.3 --runc-version v1.2.6 --cni-version v1.5.1
opskit mirror containerd
# 自行安排重启，使 mirror 生效
sudo systemctl restart containerd
```

二进制安装先下载解包、检查版本和目标文件，再写入并启动 systemd 服务；拒绝覆盖已有二进制、unit 和 containerd 配置，已有安装的升级需单独迁移。官方下载脚本先保存到临时文件，再带完整版本执行，不通过管道运行。安装失败会返回错误，已经落盘的文件会保留供排查，不承诺自动回滚。

`--url` 覆盖主包下载地址，`--sha256` 可校验已知摘要。未传摘要时使用 HTTPS 传输与二进制版本检查，不等同于供应链签名验证；containerd 的 runc/CNI 依赖分别从 `install.runc.download_base`、`install.cni_plugins.download_base` 下载，CNI 仅检查包内容。不要将示例版本视为最新安全版本或任意 Kubernetes 版本的兼容保证；安装器不会静默降级。

containerd 默认配置启用 SystemdCgroup 和 certs.d lookup，保持上游 sandbox image；主机网络内核参数及 CNI 网络部署需要按节点方案单独配置。cri-dockerd 直接监听 `unix:///run/cri-dockerd.sock`，kubeadm 需显式指定该 socket。更多字段见[配置指南](docs/configuration.md)。目前完成本地自动化验证，尚未进行真实 Linux 主机安装验收。

## K8s SSH 集群部署（main，未发布）

`opskit k8s create` 原生实现 SSH 编排，不安装或调用 KK。配置采用 hosts、roleGroups、controlPlaneEndpoint 风格；kubeadm/kubelet/kubectl 统一跟随 `k8s.kubernetes.version`，containerd/runc/criTools/cniPlugins 写 `version: auto` 时跟随 Kubernetes minor 解析锁定版本（显式版本仍保留），kind 使用独立的 `kind.version` / `kind.kubernetes.version`。旧全局 versions、顶层 kubeadm/kubesphere 配置已移除，读取会报未知字段。

集群名称填写在 YAML 的 `k8s.name`（例如 `sxxpqp`）；执行 `opskit k8s create --config config/config.yaml`，不接受 `k8s create sxxpqp` 位置参数。缺少配置时先执行 `opskit config init`，再编辑节点、版本与网络配置。

模板默认只启用 master1，worker 节点和角色组注释；需要时同时取消注释即可自动加入。默认不安装 KubeSphere。支持 root/普通用户、密码/私钥/Agent，以及免密或密码 sudo；SSH 按 KK 方式使用配置凭据直接连接，不读取或写入 known_hosts，不要求手动 SSH 或输入 yes；不校验服务端主机指纹。旧配置需删除 `k8s.deploy.knownHostsFile`。

```sh
opskit config init
# 编辑 config/config.yaml：填写 hosts 与版本；manifest 留空自动安装固定版本 Calico。
opskit k8s create --config config/config.yaml --dry-run
opskit k8s create --config config/config.yaml
```

部署前检查所有节点的 SSH、sudo、Linux 架构和系统依赖，然后在管理机按架构下载、校验并分发二进制。并行准备节点，初始化单控制平面、部署网络，再自动生成短期 token 并加入 worker；完成后撤销 token，并等待配置中的全部节点 Ready。单节点会允许控制平面承载工作负载；多节点保留调度隔离。管理 kubeconfig 保存在 `~/.opskit/clusters/<name>/kubeconfig`，不会覆盖用户的 `~/.kube/config`。

Linux 同架构管理机可直接分发当前 opskit。macOS/Windows 或异构节点需在 `k8s.deploy.nodeBinaries` 指定由相同代码构建的 Linux 文件；目标主机不需要 Go。每个节点需预装 conntrack、socat、iptables、ip/tc、ethtool 等系统依赖，缺项在全节点预检阶段报错，不隐式调用 apt/yum。

镜像源统一在顶层 `mirrors` 设置：Docker 读取 `mirrors.docker` / `mirrors.docker_insecure_registries`，containerd 读取 `mirrors.containerd`（含 DockerHub、registry.k8s.io 等各仓库）。`k8s.registry.imageRepository` 仅指定 kubeadm 系统镜像仓库，保留上游名称时由 containerd 的对应代理映射处理。重复的 `k8s.registry.registryMirrors` 已移除，旧配置需删掉该字段。

`k8s.network.manifest` 留空时自动安装 Calico v3.26.5（当前自动路径支持 Kubernetes 1.24～1.28），可用 `--calico-version` 显式指定。默认配置如下；已有配置缺少 calico 字段时读取模板固定版本：

```yaml
k8s:
  network:
    kubePodsCIDR: 10.244.0.0/16
    kubeServiceCIDR: 10.96.0.0/12
    manifest: ""
    calico:
      version: v3.26.5
```

管理机按 `install.calico.download_base` 下载一次清单，校验模板记录的官方文件 SHA-256 后分发；设置 Calico IP 池为 Pod CIDR，使用 Kubernetes InternalIP 探测节点地址，并等待 calico-node / calico-kube-controllers 就绪。镜像沿用官方名称，由现有节点 mirror 加速。指定本地 `manifest` 时使用该文件，不下载或改写；首次节点检测到其他 CNI 配置会停止。下载源与生产网络要求见 [Calico 配置指南](docs/configuration.md#calico-自动安装)。

现有本地 `kubeadm install / prepare / init / join` 继续作为维护入口，不自动 reset、覆盖已有工具或加入外部集群。新版本兼容层可按 Kubernetes 1.24～1.37 的已审查版本档案自动选择 containerd/runc/crictl/CNI，并按 1.31 分界生成 kubeadm v1beta3/v1beta4 配置；**1.29+ 的 CNI 清单暂须显式提供，完整多版本安装尚未真机验收**。旧 KS 配置不是生产版本推荐。参见 [Kubernetes 版本自动匹配指南](docs/k8s-version-profiles.md)。已完成本地 SSH、编排模拟和下载校验测试，真实多主机部署尚未验收；没有宣称支持 HA、独立 etcd 或升级。完整配置和迁移方法见 [K8s 部署指南](docs/configuration.md#k8s-ssh-集群部署)。

## 路线图

- [x] k8s create 自动下载并校验 Calico v3.26.5，匹配 Pod CIDR 并等待就绪（main，未发布；未真机验收）

- [x] Cobra 命令框架、统一帮助与 Shell 补全（main，未发布）

- [x] Go SDK 按版本安装、SHA-256 / 实际版本校验和自动 GOPROXY 设置（main，未发布）

下面是演进方向，未勾选的功能尚未实现。欢迎在 Issue 中分享使用场景，帮助确定优先级。

- [x] Docker 双方式、cri-dockerd 和 containerd 版本安装（main，未发布）
- [x] K8s 原生 SSH 编排、单节点/多 worker、密码/密钥/sudo 与凭据自动传递（main，未发布；尚未真机验收）
- [x] kubeadm 工具安装、节点准备、单控制平面初始化和工作节点加入（main，未发布；尚未真机验收）
- [ ] kubeadm 路线 Linux 真机端到端部署验收
- [ ] 启动检查新版本并交互确认更新（[设计计划](go-opskit-design.md#14-2026-10-18-启动更新提示计划尚未实现)，尚未实现）
- [ ] 运行时安装 Linux 真机验收及升级迁移
- [x] kubectl 版本安装、SHA-256/客户端版本及 PATH 校验，支持完整帮助（main，未发布）
- [x] kind 显式版本安装、SHA-256 校验、版本隔离和跨系统 PATH 配置（main，未发布）
- [x] kind 控制平面默认映射 Ingress 80/443，支持覆盖宿主机端口（main，未发布）
- [x] kind 集群管理、统一 mirror 和 KS 3.4.1 安装工作流（main，未发布）
- [x] etcd 快照、SHA-256 清单、失败保留
- [x] S3 兼容上传与远端 metadata 验证
- [x] S3 桶连通性与 etcd 集群成员 / 健康检查
- [x] Windows / Linux / macOS 双架构构建与自动发布
- [x] GitHub / Nexus 双下载入口，发布后自动同步首页版本与下载表
- [x] 当前目录 YAML 配置、内置注释模板与命令行覆盖
- [x] MinIO / AWS / custom 厂家选择及独立凭据
- [ ] 真实 etcd / S3 集成测试与隔离恢复演练
- [ ] 备份保留策略
- [ ] S3 下载、列举与内容回读校验
- [ ] MySQL / PostgreSQL 逻辑备份
- [ ] TCP / DNS / HTTP 下载 / ICMP 网络排查
- [ ] Linux ss / netstat 连接查看

当前可靠性与兼容性实施标准见 [M0 技术设计基线](docs/m0-design-baseline.md)；长期目标见 [设计文档](go-opskit-design.md)。设计内容不代表全部已实现，以上清单反映当前能力。

备用地址在 [downloads.json](downloads.json) 集中配置，接入说明见 [Nexus 下载指南](docs/downloads.md)。

## 近期更新

Docker 脚本安装默认使用清华 Docker CE 软件包源，`install.docker.mirror` 支持 HTTP(S) URL 或 Aliyun / AzureChinaCloud。脚本下载地址独立由 `install.docker.script` 配置，模板指向 Nexus 的固定提交代理；代理端到端连通性尚未验证。详见 [清华接入配置](docs/configuration.md#docker-清华软件包源)。

下载地址均可通过统一 YAML 覆盖，完整对应表见 [下载源配置](docs/configuration.md#下载源配置)。kind 与运行时源相互独立；默认地址统一定义在配置模板。

| 功能版本 | 变化 |
|---|---|
| [v0.1.11](https://github.com/sxxpqp/opskit/releases/tag/v0.1.11) | 新版本已发布，详细变化见对应 Release 说明 |
| [v0.1.12](https://github.com/sxxpqp/opskit/releases/tag/v0.1.12) | 新版本已发布，详细变化见对应 Release 说明 |
| [v0.1.8](https://github.com/sxxpqp/opskit/releases/tag/v0.1.8) | 新版本已发布，详细变化见对应 Release 说明 |
| [v0.1.7](https://github.com/sxxpqp/opskit/releases/tag/v0.1.7) | 新版本已发布，详细变化见对应 Release 说明 |
| [v0.1.6](https://github.com/sxxpqp/opskit/releases/tag/v0.1.6) | 新版本已发布，详细变化见对应 Release 说明 |
| v0.1.11（待发布） | Cobra 命令框架、Go SDK 与代理、原生 SSH + kubeadm 集群部署、固定版本 Calico；版本按安装类型隔离，修复镜像源覆盖、Windows CRI socket 路径校验并自动配置 Docker inotify。已通过本地回归，未完成真机验收 |
| main（未发布） | SSH 改为 KK 式直接认证，不使用 known_hosts 或指纹确认；移除 knownHostsFile 配置 |
| main（未发布） | Docker 脚本/二进制安装自动备份并应用 inotify 限额，保留更高值并回读验证 |
| main（未发布） | 删除重复的 k8s.registry.registryMirrors；集群 Docker/containerd 分别读取顶层 mirrors 配置，保留 containerd 路径代理及 TLS 设置 |
| main（未发布） | k8s create 默认自动安装固定版本 Calico，下载摘要校验、Pod CIDR 渲染及就绪等待；保留本地清单覆盖 |
| main（未发布） | 全部命令迁移至 Cobra/pflag，保留配置优先级和任务退出码，支持 Shell 补全 |
| main（未发布） | 顶层帮助改为中文分行列表；k8s create 说明配置中的集群名并改进缺失配置提示 |
| main（未发布） | 新增 go install：独立 go.version、完整 SDK、发布摘要校验及安装后自动配置 GOPROXY |
| main（未发布） | kubectl 补充 Nexus / 阿里云兼容镜像配置与下载要求，标明实际可达性限制 |
| main（未发布） | 安装配置统一为 `install.<组件>.<参数>`，kind、kubectl、Docker 和运行时各自分组；旧平铺字段需迁移 |
| main（未发布） | K8s 重跑核对服务端版本、严格检查 RuntimeReady，单节点 taint 更新保留其他设置并检测并发 |
| main（未发布） | 原生 k8s create SSH 编排；版本按 kind/k8s 分组，工具统一跟随 Kubernetes，默认单节点且关闭 KS |
| main（未发布） | 新增 kubeadm + containerd 按节点安装路线，显式版本、下载校验、init/join 和 dry-run |
| main（未发布） | kind mirror 重启 containerd 后最多等待 60 秒确认 CRI 就绪，失败输出服务状态和日志 |
| main（未发布） | containerd registry 配置识别支持单引号 plugin 节名及 kind 隐式 registry 父表 |
| main（未发布） | KS 安装成功摘要显示 admin 和配置的实际登录密码 |
| main（未发布） | KS 使用 k8s.kubesphere.password 设置初始密码，默认 Sxxpqp.top1，移除密码文件 |
| main（未发布） | KS 默认总超时延长至 45 分钟，核心 Deployment 进度期限跟随 --timeout |
| main（未发布） | KS 安装器、core 状态和核心 Deployment 等待共用 --timeout 总期限，取消固定 180 秒 / 900 秒限制 |
| main（未发布） | kind KS 控制台端口改为监听 0.0.0.0，支持通过宿主机 IP 访问；已有集群需重建映射 |
| main（未发布） | KS 安装先等待 ks-installer Deployment 就绪并输出最近 100 行日志，再等待核心服务 |
| main（未发布） | KS 安装轮询资源 JSON 等待 core enabled，兼容初始缺少 status 字段的 kubectl v1.24.7 |
| main（未发布） | 所有命令均不接受 --apply；kind delete 使用 --dry-run 预览，直接使用配置中的集群名，--name 可覆盖 |
| main（未发布） | kubesphere install 默认执行安装，--dry-run 只读预检，移除 --apply |
| main（未发布） | kubesphere install 自动读取默认 kubeconfig 和 current-context，保留显式参数与 --kind 推导 |
| main（未发布） | mirror 默认备份并写入，--dry-run 预览；标准 Docker 配置写入后自动重启并检查服务 |
| main（未发布） | kubectl 默认下载源改为 Nexus kubernetes-binaries 代理；已有显式配置保持优先 |
| main（未发布） | kubectl 下载源统一命名为 --kubectl-download-base / KUBECTL_DOWNLOAD_BASE / install.kubectl.download_base，移除旧 --download-base 参数 |
| main（未发布） | kubectl 下载源支持 KUBECTL_DOWNLOAD_BASE；命令行 > 环境变量 > YAML > 模板默认值 |
| main（未发布） | kind 控制平面补齐 Ingress 80/443 映射及 ingress-ready 标签；保留 KS 30880，校验端口冲突并支持配置覆盖 |
| main（未发布） | 新增 kubectl install：版本跟随统一配置，跨系统下载与校验，支持 -h / --help 和显式 dry-run |
| main（未发布） | kind create 默认执行创建，--dry-run 仅预览；删除操作仍需显式确认 |
| main（未发布） | 修复 kind 等命令组的裸命令、-h、--help、help 帮助入口，不再作为未知子命令处理 |
| main（未发布） | kind 优先复用 PATH 中已有可写目录，保留 Linux/macOS 软链接和 Windows exe 复制；无可用目录才配置用户 PATH |
| main（未发布） | kind 安装后检查入口版本和 PATH 实际命中，识别旧版本遮挡与待生效状态；Windows User PATH 回读校验 |
| main（未发布） | 安装文件默认统一保留到 ~/.opskit/downloads/；kind 按 Windows/Linux/macOS 选择命令入口并持久化用户 PATH，保留版本隔离目录 |
| main（未发布） | 新增 Docker 官方脚本 / 二进制、cri-dockerd、containerd + runc + CNI 安装，默认直接安装、--dry-run 预览，并拒绝覆盖已有运行时 |
| main（未发布） | 迁入 kind/mirror/KS 流程与项目实际默认镜像源，支持 Docker insecure-registry 合并与 Go proxy 列表；配置集中到 config/config.yaml |
| main（发布流程更新） | 新增 Nexus 备用下载；Release 自动附带主 / 备用链接，首页版本摘要、近期更新入口与下载表随正式发布自动同步 |
| [v0.1.5](https://github.com/sxxpqp/opskit/releases/tag/v0.1.5) | 新增 `s3 check` 与 `etcd check`，展示桶访问、成员数量、逐节点健康和观察到的 quorum |
| [v0.1.4](https://github.com/sxxpqp/opskit/releases/tag/v0.1.4) | 按 MinIO / AWS / custom 选择配置，分别使用厂家默认值与凭据 |
| [v0.1.3](https://github.com/sxxpqp/opskit/releases/tag/v0.1.3) | 模板预填 kubeadm 证书路径，补充 MinIO/S3 字段说明与示例 |
| [v0.1.2](https://github.com/sxxpqp/opskit/releases/tag/v0.1.2) | 当前目录 YAML 配置、`config init` 与内置模板 |
| [v0.1.1](https://github.com/sxxpqp/opskit/releases/tag/v0.1.1) | 六种平台可执行文件直接下载，无需解压 |
| [v0.1.0](https://github.com/sxxpqp/opskit/releases/tag/v0.1.0) | etcd 快照、SHA-256 清单、S3 上传与自动构建发布 |

新标签需待自动检查与构建通过后出现在 Releases；可在 [Actions](https://github.com/sxxpqp/opskit/actions) 查看进度。后续功能变更会同步更新首页的功能表、上手命令、路线图和此更新记录。

## 参与项目

如果 opskit 对你有帮助，欢迎点个 **Star**，也欢迎通过 **Watch → Releases** 关注新版本。

更期待你带着实际问题参与：[报告 Bug](https://github.com/sxxpqp/opskit/issues/new?template=bug_report.yml)、[提出需求](https://github.com/sxxpqp/opskit/issues/new?template=feature_request.yml)，或提交一个小而清晰的 PR。反馈时请注明系统、架构、工具版本和复现步骤，移除密码、密钥与业务数据。

开发与提交方式见 [贡献指南](CONTRIBUTING.md)。

## 本地开发与发布

Go 版本见 [go.mod](go.mod)。

```sh
go build -o opskit ./cmd/opskit
go test ./...
go test -race ./...
go vet ./...
```

每次 main 提交自动测试并构建，开发产物位于 Actions 的 `opskit-packages`。创建并推送新的 `v*` 标签后，三平台测试与六架构构建全部通过才自动发布公开 Release：

```sh
git tag v0.1.6
git push origin v0.1.6
```

仅在本地创建标签不会触发。每个版本使用新标签，已有版本不重复发布。
