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
  <strong>一个 Go 运维工具箱，检查集群健康，备份 etcd，归档到 S3。</strong><br>
  集群健康检查 · Docker / CRI / containerd 安装与 kind / mirror / KS 工作流（main） · MinIO / AWS 厂家配置 · GitHub / Nexus 双下载入口
</p>

<p align="center">
  <a href="#运行时安装main未发布">运行时安装</a> ·
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
**当前最新正式版：[v0.1.6](https://github.com/sxxpqp/opskit/releases/tag/v0.1.6)**

新版本已发布，详细变化见对应 Release 说明。

→ [选择系统与架构下载](#下载) · [使用 Nexus 备用入口](#下载) · [查看近期更新](#近期更新)
<!-- LATEST_RELEASE_END -->

## 为什么做 opskit

备份 etcd、记录校验信息、再把文件传到对象存储，这些日常操作经常散落在不同脚本里。opskit 希望把它们收进一套清楚、可重复执行的命令中，让服务器、个人电脑和自动化任务使用同一个入口。

项目正在早期迭代，当前已支持 **etcd 集群健康检查、快照备份、S3 连通性检查与上传**，以及 **MinIO / AWS / 自定义 S3 厂家配置**。安装包提供 GitHub 与 Nexus 双入口，首页版本摘要、近期更新入口和下载表随正式发布自动刷新。网络排查、更多数据库备份和保留策略将逐步加入。

main 分支新增 **Docker 双方式 / cri-dockerd / containerd 安装、kind 管理、统一镜像源和 KubeSphere 3.4.1 安装**（尚未发布），配置统一为 `config/config.yaml`，旧 `opskit.yaml` 仅在默认文件不存在时回退读取，不合并；现有 Release 下载仍对应上方正式版。

## 现在能做什么

| 能力 | 当前行为 |
|---|---|
| 运行时安装（main，未发布） | Docker 官方脚本 / 二进制、cri-dockerd、containerd + runc + CNI；指定版本、默认执行、--dry-run 预览 |
| kind 管理（main，未发布） | 版本安装、集群创建/状态/日志、context 切换及确认删除 |
| 统一镜像源（main，未发布） | Docker、containerd、Go、npm、pip、Maven；预览、备份与显式写入 |
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
| 安装指定 kind 版本（main） | `opskit kind install --version v0.17.0` |
| 预览 / 创建集群（main） | `opskit kind create --version v1.24.7` / 追加 `--apply` |
| 修改 kind 镜像源（main） | `opskit mirror containerd --kind kubesphere-341 --apply` |
| 安装 KS（main） | `opskit kubesphere install --version v3.4.1 --kind kubesphere-341 --apply` |
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
./opskit kind create --version v1.24.7 --workers 2     # 默认预览
./opskit kind create --version v1.24.7 --workers 2 --apply
./opskit mirror containerd --kind kubesphere-341       # 预检全部节点，不写入
./opskit mirror containerd --kind kubesphere-341 --apply
# 确认集群已有可供卷的默认 StorageClass；本工具不会安装存储驱动
./opskit kubesphere install --version v3.4.1 --kind kubesphere-341
./opskit kubesphere install --version v3.4.1 --kind kubesphere-341 --apply
./opskit kind status
./opskit kind logs
# 删除需 apply 与完整集群名两项确认
./opskit kind delete --name kubesphere-341 --apply --confirm-name kubesphere-341
```

`kind create --version` 指 Kubernetes 节点版本，`kind install --version` 指 kind 工具版本，`kubesphere install --version` 指 KS 版本。模板提供经过确认的 kind v0.17.0 / K8s v1.24.7 / KS v3.4.1 组合；创建时若配置指定 kind 版本，将验证实际工具版本一致。已有同名集群会报错并保留，不自动删除、重建或调整拓扑。kind 命令仍需 Docker、kubectl；安装 kind 本身无需 Go。

自建集群 KS 安装使用 `--kubeconfig FILE --context NAME`，不传 `--kind`；各节点 mirror 请先执行 `mirror containerd --apply` 配置。本次未迁移 SSH 节点批量管理。KS 仅支持已核对的内嵌 3.4.1 清单，校验 K8s 1.20～1.26、节点 Ready、cluster-admin 和唯一默认 StorageClass。已有 `kubesphere-system` 时停止，不覆盖实例和密码。完成条件是核心 enabled 和三个核心 Deployment 就绪，监控需另查状态。密码保存在本机 0600 文件，不打印明文；ARM 上游尚未完全支持，需要自行验证所选组件镜像。

`mirror docker/containerd/go/npm/pip/maven` 共用 `mirrors` 配置；不再将 mirror 归属于 kind。默认只预览，`--apply` 备份后写入。宿主机 Docker/containerd 修改后不自动重启服务，需自行安排维护窗口；kind 节点逐台重启并检查 CRI、验证 DockerHub 拉取。Maven 已有自定义 mirrors 或 containerd 旧认证配置时要求人工合并，不覆盖未知配置。

```sh
./opskit mirror docker                         # 预览 Linux /etc/docker/daemon.json
./opskit mirror go --url https://your-go-proxy.example.com
./opskit mirror npm --url https://your-npm.example.com --apply
./opskit mirror pip --url https://your-pypi.example.com/simple --apply
./opskit mirror maven --url https://your-maven.example.com/repository --apply
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

版本必须由 `--version` 或统一配置的 `versions.kind` 明确提供，不隐式选择 latest。默认安装位置为 `~/.local/share/opskit/kind/<版本>/kind`，Windows 为 `kind.exe`；日志展示版本和实际路径。不同版本目录可共存，调用时使用对应路径或将所选目录加入 PATH，不自动修改 shell 配置。

kind 发布包支持 Linux/macOS amd64、arm64 和 Windows amd64；Windows 原生 ARM64 的 kind 安装暂不支持（opskit 自身仍可构建该平台）。kind 安装器源默认沿用已有公开 `registry.cn-beijing.aliyuncs.com/kubesphereio/ks-installer:v3.4.1`，仅用于 `--kind` 预加载，不改上游清单；自建集群忽略该模板项。kind 工具发布包默认通过现有 Nexus `raw-github` 代理下载；可用 `--download-base https://github.com` 或环境变量 `KIND_DOWNLOAD_BASE` 显式选择来源。安装器读取统一配置；运行不依赖 Bash、curl 或 Go。`kind install` 只安装 kind 工具，其他动作使用相应子命令。

旧 `linux` 仓库脚本在 PATH 中找到 opskit（或设置 `OPSKIT_BIN`）时会调用本实现，并传递原版本和安装目录；未部署 opskit 的机器继续使用旧脚本。测试覆盖版本共存、重复安装、SHA/版本校验失败和 HTTP 失败保留原文件；新增工作流测试通过模拟工具验证命令顺序、预检阻断和秘密保护；macOS 做了安装器原生执行检查，尚未完成真实集群 KS 安装和 Windows/Linux 真机工作流验收。

## 下载

**[下载最新版本](https://github.com/sxxpqp/opskit/releases/latest)** · [查看历史版本与更新说明](https://github.com/sxxpqp/opskit/releases)

公开附件无需登录 GitHub，选好系统和 CPU 架构即可下载。GitHub 下载不便时，可使用下表的 **Nexus 备用下载**。每次正式发布完成后，工作流自动将下表更新到 GitHub 标记的最新版本；使用版本专属地址，不经过 Nexus 的 latest 别名。

<!-- DOWNLOAD_LINKS_START -->
### v0.1.6 下载：GitHub / Nexus 备用

同一行的主链接与备用链接指向同一版本。按系统和 CPU 架构下载，无需解压。

| 系统 | CPU 架构 | GitHub | Nexus 备用 |
|---|---|---|---|
| Windows | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.6/opskit_windows_amd64.exe) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.6/opskit_windows_amd64.exe) |
| Windows | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.6/opskit_windows_arm64.exe) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.6/opskit_windows_arm64.exe) |
| Linux | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.6/opskit_linux_amd64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.6/opskit_linux_amd64) |
| Linux | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.6/opskit_linux_arm64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.6/opskit_linux_arm64) |
| macOS | x86_64 / amd64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.6/opskit_darwin_amd64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.6/opskit_darwin_amd64) |
| macOS | ARM64 / aarch64 | [下载](https://github.com/sxxpqp/opskit/releases/download/v0.1.6/opskit_darwin_arm64) | [备用下载](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.6/opskit_darwin_arm64) |

校验文件：[GitHub](https://github.com/sxxpqp/opskit/releases/download/v0.1.6/checksums.txt) · [Nexus](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.6/checksums.txt)

配置模板：[GitHub](https://github.com/sxxpqp/opskit/releases/download/v0.1.6/config.example.yaml) · [Nexus](https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.6/config.example.yaml)

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

## 运行时安装（main，未发布）

Linux systemd 主机使用；macOS/Windows 可以预览，实际安装需进入 Linux VM 或启用 systemd 的 WSL2。运行时安装命令默认直接执行，添加 `--dry-run` 仅预览；安装目标版本写在 `config/config.yaml` 的 `versions`，CLI 可覆盖。

```bash
# 默认使用官方脚本和 Aliyun 软件包源；预览可追加 --dry-run
opskit docker install --version v27.3.1
# Docker 静态二进制：包含 dockerd 自带的 containerd/runc
opskit docker install --version v27.3.1 --method binary
# 使用 Docker 作为 Kubernetes 运行时时再装 CRI 适配器
opskit cri-dockerd install --version v0.3.22
# 或单独安装 containerd（与上面的 Docker 路线二选一）
opskit containerd install --version v2.1.3 --runc-version v1.2.6 --cni-version v1.5.1
opskit mirror containerd --apply
# 自行安排重启，使 mirror 生效
sudo systemctl restart containerd
```

二进制安装先下载解包、检查版本和目标文件，再写入并启动 systemd 服务；拒绝覆盖已有二进制、unit 和 containerd 配置，已有安装的升级需单独迁移。官方下载脚本先保存到临时文件，再带完整版本执行，不通过管道运行。安装失败会返回错误，已经落盘的文件会保留供排查，不承诺自动回滚。

`--url` 覆盖主包下载地址，`--sha256` 可校验已知摘要。未传摘要时使用 HTTPS 传输与二进制版本检查，不等同于供应链签名验证；containerd 的 runc/CNI 依赖从 `install.release_base` 下载，CNI 仅检查包内容。不要将示例版本视为最新安全版本或任意 Kubernetes 版本的兼容保证；安装器不会静默降级。

containerd 默认配置启用 SystemdCgroup 和 certs.d lookup，保持上游 sandbox image；主机网络内核参数及 CNI 网络部署需要按节点方案单独配置。cri-dockerd 直接监听 `unix:///run/cri-dockerd.sock`，kubeadm 需显式指定该 socket。更多字段见[配置指南](docs/configuration.md)。目前完成本地自动化验证，尚未进行真实 Linux 主机安装验收。

## 路线图

下面是演进方向，未勾选的功能尚未实现。欢迎在 Issue 中分享使用场景，帮助确定优先级。

- [x] Docker 双方式、cri-dockerd 和 containerd 版本安装（main，未发布）
- [ ] 运行时安装 Linux 真机验收及升级迁移
- [x] kind 显式版本安装、SHA-256 校验和版本隔离（main，未发布）
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

| 功能版本 | 变化 |
|---|---|
| [v0.1.6](https://github.com/sxxpqp/opskit/releases/tag/v0.1.6) | 新版本已发布，详细变化见对应 Release 说明 |
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
