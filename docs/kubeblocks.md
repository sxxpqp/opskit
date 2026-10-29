# KubeBlocks 中间件（main，未发布）

Kubernetes 路线参考 linux 项目的 KubeBlocks v1 资源，提供 MySQL 半同步三副本、Redis 三分片各两副本、MinIO 四副本。Docker／裸机路线另见[中间件指南](middleware.md)。此处不执行参考仓库脚本，不读取生产地址或凭据。

两条路线的需求范围、实施顺序和验收进度统一见[需求与验收清单](middleware-requirements.md)。

## 首次安装

准备 Kubernetes、可访问目标集群的 kubectl 和 Helm 3.10+。默认下载固定 KubeBlocks 1.0.2、CRD 和 MinIO 1.0.2 addon；URL 与 SHA-256 全部来自 `config.example.yaml` 的 `kubeblocks`，支持在自己的 YAML 覆盖代理源。MySQL/Redis addon 由 Operator Chart 注册；MinIO 单独安装，不能假定主 Chart 已包含它。

```sh
opskit kubeblocks install --config ./config/config.yaml --dry-run
opskit kubeblocks install --config ./config/config.yaml --kubeconfig /path/to/kubeconfig
# 已取得相同版本文件时，可提供本地包；仍检查配置中的摘要
opskit kubeblocks install --chart ./kubeblocks-1.0.2.tgz --crds ./kubeblocks_crds.yaml --minio-addon ./minio-1.0.2.tgz
```

安装先完成下载和摘要校验，再创建 CRD 和 Helm Release，默认等待 Operator 与三类 ComponentDefinition。Operator 和数据保护控制器各两副本，使用上游 Chart 的资源请求（每副本 500m / 1Gi）；须预留控制器和业务实例容量。命令固定一次 current-context，同时传给 kubectl 和 Helm。

`install` 仅首次安装：已有 KubeBlocks CRD 或 Helm Release 时拒绝接管和升级。失败保留现场，不自动卸载控制器、清除 CRD 或数据。部分安装需先检查日志和 Helm 状态，按实际故障修复，不反复强制删除。此命令不配置备份仓库、自动备份、密码轮换或升级流程。

## 业务集群

业务 namespace 和 StorageClass 必须已存在；部署器只创建 Cluster，不修改共享 namespace、默认存储类或卷回收策略。

```yaml
schema_version: 1
kubeblocks:
  name: orders
  namespace: middleware
  engine: mysql
  storage_class: your-storage-class
  storage_size: 20Gi
  allow_colocation: false
```

```sh
kubectl create namespace middleware
opskit kubeblocks render --config ./ha.yaml
opskit kubeblocks deploy --config ./ha.yaml --kubeconfig /path/to/kubeconfig
opskit kubeblocks status --config ./ha.yaml --kubeconfig /path/to/kubeconfig
```

| 引擎 | 固定组件定义与版本 | 默认实例数 | 默认请求 / 上限（每实例） |
|---|---|---:|---|
| MySQL | mysql-8.0-1.0.2 / 8.0.33 | 3 | 500m、1Gi / 2 CPU、2Gi |
| Redis | redis-cluster-7-1.0.2 / 7.2.4 | 6 | 100m、256Mi / 1 CPU、512Mi |
| MinIO | minio-1.0.2 / 2025.10.15 | 4 | 250m、512Mi / 2 CPU、1Gi |

组件可通过 `mysql/redis/minio.component_def`、`version`、`cpu`、`memory`、`limit_cpu`、`limit_memory` 配置。必须使用已安装的相容定义和版本。自定义组合需要自行验收；默认关闭 exporter，监控并未接入本功能。

每副本申请独立 RWO 数据卷，默认 `terminationPolicy: DoNotTerminate`，阻止意外删除 Cluster。此策略不能阻止直接删除 PVC，也不能替代备份。Local PV 仅持久化在原节点，不提供跨节点卷副本或节点损坏后的自动数据修复。

默认强制整个 Cluster 的实例按 `kubernetes.io/hostname` 分散，每节点一个实例：MySQL 至少 3 个、Redis 至少 6 个、MinIO 至少 4 个可调度 Ready 节点。部署前排除被 cordon 或有 NoSchedule/NoExecute 污点的节点；资源不足等约束仍以调度器为准。该保守方案不把多个 Redis 分片的主节点挤在同一节点。节点标签隔离不等同于机架、可用区或物理服务器独立，仍需验证底层故障域。

仅实验环境可设置 `allow_colocation: true`，改为偏好反亲和；输出始终显示此配置。它不能证明宿主机故障高可用。

## 凭据、连接与恢复边界

Operator 随机生成系统账号并存入 Secret。部署和状态命令不读取或输出密码；通过组织已有 Secret 管理流程授权获取。此路线不读取 Docker／SSH 路线的密码变量，不支持注入固定密码。

服务默认只供 Kubernetes 集群内访问，使用 `kubectl get svc -n <namespace>` 查看 Operator 实际生成的服务。MySQL 写入应使用主节点 Service；Redis 需要支持 Cluster 协议且可达所有分片地址的客户端；MinIO 使用对应 API Service。此功能不创建公网入口、NodePort、外部负载均衡或 TLS。

重复 `deploy` 仅对 opskit 管理、配置摘要及期望 spec 均匹配的 Cluster 继续等待；不覆盖外部修改、升级或扩缩容。并发首次部署由 Kubernetes 原子的 create 拒绝重复创建。`status` 要求 Cluster 的当前代次为 Running、所有非完成 Pod Ready、PVC Bound，并核对节点分布；不将控制器状态冒充业务读写成功。

控制器与引擎负责自身支持的故障恢复。本命令不提供强制选主、清空 GTID、丢弃 Redis 槽位、删除 PVC 或重建损坏数据。Vagrant 功能验收与故障边界见[验证记录](middleware-validation-2026-10-28.md)。

上游资料：[官方安装流程](https://www.kubeblocks.io/docs/preview/user_docs/overview/install-kubeblocks)、[addon 管理](https://kubeblocks.io/docs/release-1_0/user_docs/references/install-addons)、[KubeBlocks 1.0.2 Release](https://github.com/apecloud/kubeblocks/releases/tag/v1.0.2)。
