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
  <strong>一个 Go 运维工具箱，从 etcd 备份到 S3 归档。</strong><br>
  下载即用 · 六种平台构建 · 本地备份可追踪
</p>

<p align="center">
  <a href="#快速上手">快速上手</a> ·
  <a href="#下载">下载</a> ·
  <a href="#配置文件">配置文件</a> ·
  <a href="#etcd-备份">使用指南</a> ·
  <a href="#路线图">路线图</a> ·
  <a href="https://github.com/sxxpqp/opskit/issues/new/choose">反馈与建议</a>
</p>

## 为什么做 opskit

备份 etcd、记录校验信息、再把文件传到对象存储，这些日常操作经常散落在不同脚本里。opskit 希望把它们收进一套清楚、可重复执行的命令中，让服务器、个人电脑和自动化任务使用同一个入口。

项目正在早期迭代，当前重点是 **etcd 快照与 S3 上传**。网络排查、更多数据库备份和保留策略将逐步加入。

## 现在能做什么

| 能力 | 当前行为 |
|---|---|
| etcd 集群检查 | 成员数量、投票节点 / learner、逐节点健康、Leader 与版本 |
| S3 连通性 | 检查桶访问，区分 HTTP 权限错误与连接失败，可选对象 HEAD |
| etcd 快照 | 调用官方 `etcdctl`，支持 CA、客户端证书与任务超时 |
| 当前目录配置 | 自动加载 `./opskit.yaml`，内置模板，一条命令生成 |
| 备份清单 | 为每次任务生成独立目录，记录文件大小、SHA-256 和上传状态 |
| S3 上传 | 内置 Go SDK，支持自定义 endpoint、region 和 path-style |
| 失败保留 | 上传失败仍保留完整本地快照，便于排查和重试 |
| 跨平台交付 | Windows / Linux / macOS，amd64 与 arm64，直接下载可执行文件 |

S3 兼容服务的后端能力需实际验证。etcd 备份需要单独安装匹配版本的 `etcdctl`；当前尚未完成真实环境的恢复验收。

## 快速上手

以 Linux x86_64 为例，下载后运行；其他平台见下方下载表。

```sh
curl -fL https://github.com/sxxpqp/opskit/releases/latest/download/opskit_linux_amd64 -o opskit
chmod +x opskit
./opskit version
./opskit etcd backup --help

# 本地开发环境示例：先安装 etcdctl，并确认 etcd 已在该地址运行
./opskit etcd backup --endpoint http://127.0.0.1:2379 --directory ./backups
```

生产环境的 TLS 连接和 S3 上传示例见下文。下载文件可先按 Release 附带的 `checksums.txt` 校验。

## 下载

**[下载最新版本](https://github.com/sxxpqp/opskit/releases/latest)** · [查看历史版本与更新说明](https://github.com/sxxpqp/opskit/releases)

公开附件无需登录 GitHub，选好系统和 CPU 架构即可下载。

| 系统 | x86_64 / amd64（Intel、AMD 64 位） | ARM64 / aarch64 |
|---|---|---|
| Windows | [下载 EXE](https://github.com/sxxpqp/opskit/releases/latest/download/opskit_windows_amd64.exe) | [下载 EXE](https://github.com/sxxpqp/opskit/releases/latest/download/opskit_windows_arm64.exe) |
| Linux | [下载二进制](https://github.com/sxxpqp/opskit/releases/latest/download/opskit_linux_amd64) | [下载二进制](https://github.com/sxxpqp/opskit/releases/latest/download/opskit_linux_arm64) |
| macOS | [Intel 芯片下载](https://github.com/sxxpqp/opskit/releases/latest/download/opskit_darwin_amd64) | [Apple 芯片下载](https://github.com/sxxpqp/opskit/releases/latest/download/opskit_darwin_arm64) |

这里的 x86 指 64 位 x86_64，不提供 32 位 x86 安装包。下载后将文件重命名为 `opskit`（Windows 为 `opskit.exe`）；Linux/macOS 执行 `chmod +x opskit` 后运行，无需解压。可下载 [SHA-256 校验文件](https://github.com/sxxpqp/opskit/releases/latest/download/checksums.txt) 校验完整性。


## 配置文件

在准备运行命令的目录生成配置模板：

```sh
./opskit config init
# 编辑当前目录生成的 opskit.yaml，然后运行：
./opskit etcd backup
```

模板默认采用 **kubeadm 控制平面节点的本机 etcd 地址与证书路径**，逐项说明备份目录、TLS、MinIO/S3 和三种 key 的区别。其他 Kubernetes 发行版或外置 etcd 需修改实际路径。完整示例见 [Kubernetes + MinIO/S3 配置指南](docs/configuration.md)。也可查看仓库的 [配置模板](config.example.yaml)，或从 [最新版本附件](https://github.com/sxxpqp/opskit/releases/latest) 下载 `config.example.yaml` 后复制为 `opskit.yaml`。二进制内置同一份模板，无需额外下载即可生成。

**读取规则：命令行参数 > 配置文件 > 内置默认值。**

- 默认只读取**当前工作目录**的 `opskit.yaml`，不是可执行文件所在目录。
- 默认文件不存在时仍可使用命令行参数；使用 `--config` 显式指定的文件不存在则报错。
- 配置文件中的相对路径按配置文件所在目录解析；命令行中的相对路径按工作目录解析。
- 配置拒绝未知字段、重复字段、多个 YAML 文档及无效超时，帮助尽早发现拼写错误。
- `config init` 不覆盖已有文件。`opskit.yaml` 已加入 `.gitignore`；访问密钥可配置在所选厂家块，也可通过环境变量或 SDK 凭据链传入；请勿提交真实凭据。

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

复用当前目录的 `opskit.yaml`，或用 `--config` 指定其他文件：

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

只指定一个 endpoint；也可使用 `ETCDCTL_ENDPOINTS`、`ETCDCTL_CACERT`、`ETCDCTL_CERT`、`ETCDCTL_KEY`、`ETCDCTL_USER` 等 etcdctl 环境变量。密码不要放在命令参数里。工具直接执行 etcdctl，不经过 shell。默认保留 TLS 验证。

每个任务使用独立目录，保存 `snapshot.db` 与 `manifest.json`（大小、SHA-256、时间、上传状态）。失败快照保留 `.partial`；上传失败保留完整本地备份并返回退出码 5。当前不自动清理备份。

```sh
# 快照备份后上传；默认生成唯一的 etcd/<task>/snapshot.db key
opskit etcd backup --endpoint https://127.0.0.1:2379 \
  --bucket backups --s3-endpoint https://s3.example.com --path-style

# 单独上传已有文件，也可用于上传失败后的重试（使用新 key）
opskit s3 upload --bucket backups --key manual/snapshot.db \
  --s3-endpoint https://s3.example.com --path-style ./snapshot.db
```

S3 凭据使用 AWS SDK 默认凭据链，例如 `AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY`、可选 `AWS_SESSION_TOKEN` 或工作负载角色。支持 AWS S3、MinIO 等 S3 兼容服务（需实际验证后端能力），不自动创建 bucket。默认 region 为 `us-east-1`，可用 `--region` 修改。所有参数须放在位置文件参数前。

上传采用分段上传，默认禁止覆盖已存在对象，要求后端支持条件写；不支持时应报错。上传后 HEAD 核对长度和 SHA-256 metadata，验证数据对象后才上传远端 manifest。metadata 验证不代表远端内容回读校验。若上传完成但 HEAD 或清单失败，请检查远端已有对象，重试时指定新 key。任务总超时包含快照和上传。

## 校验和恢复演练

```sh
etcdutl snapshot status ./snapshot.db -w table
# 仅在隔离环境恢复，勿指向正在运行的生产数据目录
etcdutl snapshot restore ./snapshot.db --data-dir ./restore-test
```

快照流程依据 [etcd 官方恢复文档](https://etcd.io/docs/v3.6/op-guide/recovery/)。本项目尚未完成真实 etcd/MinIO 环境的恢复验收，生产使用前应进行恢复演练并核对业务数据。

## 路线图

下面是演进方向，未勾选的功能尚未实现。欢迎在 Issue 中分享使用场景，帮助确定优先级。

- [x] etcd 快照、SHA-256 清单、失败保留
- [x] S3 兼容上传与远端 metadata 验证
- [x] S3 桶连通性与 etcd 集群成员 / 健康检查
- [x] Windows / Linux / macOS 双架构构建与自动发布
- [ ] 真实 etcd / S3 集成测试与隔离恢复演练
- [x] 当前目录 YAML 配置、内置注释模板与命令行覆盖
- [ ] 备份保留策略
- [ ] S3 下载、列举与内容回读校验
- [ ] MySQL / PostgreSQL 逻辑备份
- [ ] TCP / DNS / HTTP 下载 / ICMP 网络排查
- [ ] Linux ss / netstat 连接查看

完整思路见 [设计文档](go-opskit-design.md)。设计文档描述目标，以上清单反映当前实现。

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
git tag v0.1.2
git push origin v0.1.2
```

仅在本地创建标签不会触发。每个版本使用新标签，已有版本不重复发布。
