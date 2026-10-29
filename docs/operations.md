# 诊断、备份校验与离线交付

本文对应 main 的未发布功能。正式下载版本以 README 的 Release 链接为准。真实验证范围见[2026-10-28 验收记录](validation-2026-10-28.md)。

## 环境诊断

```sh
opskit doctor --profile local
opskit doctor --profile kind --format json
opskit doctor --profile k8s --config ./config/config.yaml
opskit doctor --profile backup
opskit doctor --network --timeout 30s
```

`local` 检查配置解析和下载目录权限；`kind` 额外检查 kind、kubectl、Docker 命令及 Docker Engine；`k8s` 检查 kubectl、集群配置和部署锁；`backup` 检查 etcdctl。目录检查在最近的已有父目录创建并删除一个临时文件，不创建目标下载目录。工具检查不安装软件；SSH 节点检查另用 `opskit k8s check`。

`--network` 对 `install` 中的 kubectl、kind、containerd 和 Go 下载根地址发起 HEAD 请求，配置缺省时读取嵌入模板。根地址可达不代表具体版本文件存在、吞吐达标或镜像可拉取；部分文件服务根路径返回 404/405 仍算服务可达。TLS 错误、401/403、5xx 和连接失败显示失败，不自动改源。

`doctor --format json` 输出检查明细；存在失败项返回退出码 3。配置解析失败时报告配置问题，不回显配置内容。

## 统一 JSON 结果

```sh
opskit k8s create --config ./staging.yaml --dry-run --output json
opskit doctor --profile kind --output json
opskit s3 list --prefix etcd/ --output json
```

各命令支持公共 `--output text|json`，默认 text 保持流式输出。JSON 模式在命令结束时输出一个对象，包含 `schema_version`、`command`、`ok`、`exit_code`、失败时的 `error_code`，以及 `result` 或 `log`。原生 JSON 放入 `result`；文本 stdout 最多保留 1 MiB，超出时 `truncated: true`。stderr 仍独立输出诊断，不包含在封装中；不是逐阶段事件流。

`error_code` 按退出码分为 `operation-failed`、`invalid-input`、`dependency-unavailable`、`timeout-or-cancelled` 和 `upload-failed`。请同时检查进程退出码；不同命令的已有退出码约定保持不变。封装不额外回显原始错误，详细原因仍在 stderr。

部署失败会尽可能指出节点与阶段，修复环境后用同一配置重跑。`deploy.lock` 记录管理进程 PID、创建时间及集群名；`doctor --profile k8s` 检查是否存在该锁。PID 属于原管理机，必须先确认该部署进程已退出，才手动移除锁。自动重跑不执行 reset、不接管其他集群。

## S3 列举、下载与内容校验

沿用 `s3.provider` 与对应厂家配置块及 CLI 覆盖，不新增独立下载地址；下载通过所选 S3 API endpoint 读取对象。

```sh
opskit s3 list --prefix etcd/ --limit 100
opskit s3 verify --key etcd/task/snapshot.db
opskit s3 download --key etcd/task/snapshot.db --file ./snapshot.db
# 可使用可信本地 manifest 中的摘要，优先于远端对象元数据
opskit s3 download --key etcd/task/snapshot.db --file ./snapshot.db --sha256 <64位摘要>
```

`list` 自动翻页直到上限，`limit` 范围 1～10000；结果的 `truncated` 表示仍有其他对象，本命令不提供后续游标。`verify` 完整读取对象并计算 SHA-256，不保存文件。`download` 先写目标目录中的私密临时文件，摘要与长度校验通过后才发布；拒绝覆盖已有文件、目录或链接，失败清理临时文件，目标父目录须已存在。

校验优先使用 `--sha256`，否则要求有效的对象 `sha256` 元数据；缺少或格式错误即失败。元数据和内容同来自远端时只能证明一致性，使用可信本地 manifest 的摘要可独立核对备份。`verified-content` 表示完整内容摘要通过，不等同于数据库可恢复；恢复还需匹配版本的数据库工具和隔离演练。

## 本地备份校验与保留策略

```yaml
backup:
  retention:
    keep: 7
    older_than: 720h
```

```sh
# directory 为单个任务目录
opskit backup verify --directory ./data/backups/etcd/<任务>
# directory 为备份根目录；默认只读预览
opskit backup prune --directory ./data/backups/etcd
opskit backup prune --directory ./data/backups/mysql --keep 14 --older-than 1440h
# 检查预览后实际执行
opskit backup prune --directory ./data/backups/etcd --dry-run=false
```

CLI 覆盖 `backup.retention`，缺省保留 7 份且仅选择早于 720 小时的备份。两项条件同时满足才清理，`keep` 至少为 1。按 manifest 的创建时间排序；有效任务须是直接子目录，只有一个 v1 `manifest.json` 和对应 `snapshot.db`／`backup.sql[.gz]`，文件大小与摘要一致。

清理只选择 `not-requested` 或 `verified-metadata` 状态；上传失败、pending、损坏、额外文件、链接、特殊文件和未知格式任务列入 `protected`。`backup verify` 可独立核对上传失败但已完成本地导出的文件。清理不递归删除、不删除远端对象，执行前重新核对内容并持有同根目录清理锁。请使用专用备份目录，并避免在其他进程正在修改这些历史任务时执行清理。

备份命令本身不自动清理；可由管理员在定时任务中分别安排备份和清理。文件摘要通过只证明文件完整，不证明 SQL 或快照可恢复。

## 离线归档

```sh
opskit k8s download --components kubeadm,kubelet,kubectl,crictl,containerd,runc,cni-plugins,calico
# directory 使用上一条命令输出的完成任务目录
opskit bundle export --directory ./k8s-<版本>-linux-amd64-<任务> \
  --image-archive ./images.tar --package-file ./conntrack.deb --file ./offline.tar.gz
opskit bundle import --file ./offline.tar.gz --directory ./offline
opskit bundle verify --directory ./offline
```

镜像归档及系统包均为可选、由用户事先准备；可以重复传参。包内 `components/` 保留下载目录结构，`images/`、`packages/` 存放附带文件，`bundle.json` 登记逐文件大小与 SHA-256。不导出节点认证配置或 kubeconfig；输入应使用专用的下载完成目录，不要混入个人配置。

导出前核对 `downloads.json` 的组件摘要，拒绝链接或特殊文件；导出目标必须不存在。导入拒绝路径穿越、重复文件、链接、特殊文件和超限内容，默认最多 50 GiB 未压缩文件，可用 `--max-bytes` 调整；清单最多 32 MiB、文件数量最多 100000。先在临时目录校验全部文件，再发布到尚不存在的目标目录；缺失、损坏或未登记文件均报错。

此命令提供可校验的文件交付，不自动解析系统包依赖、识别镜像完整性或执行离线安装。实际隔离环境需提前准备所有系统依赖、运行时镜像、Kubernetes 系统镜像与网络镜像；镜像可使用对应运行时导入，组件手工安装步骤见[手动 kubeadm 指南](manual-kubeadm.md)。导入不会自动填充下载缓存或改写 `install` 下载源。

## 可选 SSH 指纹与密码摘要

各 `k8s.hosts` 条目可添加 `hostKeySHA256: "SHA256:..."`，应通过可信渠道取得服务器指纹；配置后不匹配会在 SSH 握手时失败。留空保持原来的直接连接行为，不读取或修改 known_hosts；旧 `knownHostsFile` 参数仍拒绝，不恢复兼容别名。

`k8s.kubesphere.showPassword` 和 `kind.kubesphere.showPassword` 默认 false，成功摘要隐藏初始密码；CLI `--show-password`／`--show-password=false` 优先于对应配置。该开关只控制 opskit 的成功摘要，不处理外部安装器自身日志。
