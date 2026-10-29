# 高可用中间件需求与验收清单

更新日期：2026-10-29。首批功能已随 [v0.1.15](https://github.com/sxxpqp/opskit/releases/tag/v0.1.15) 发布，验收边界仍以本清单为准。

## 目标和实施顺序

参考 linux 项目的中间件方案，将部署能力接入 opskit 的统一命令和 YAML 配置。两条路线都实现，先完成 Docker／裸机，再接入 Kubernetes KubeBlocks；不直接执行参考仓库的交互脚本或复用其生产地址、密码。

首批覆盖 MySQL、Redis、MinIO。Kafka、MongoDB、PostgreSQL、RocketMQ 等其他引擎列为后续扩展，本轮不宣称已支持。

## 功能范围

| 路线 | 首批拓扑 | 交付能力 |
|---|---|---|
| Docker | MySQL 三成员 InnoDB Cluster + Router；Redis 三主三从；MinIO 四实例 + HAProxy | 渲染资料、实际部署、严格状态检查；MySQL 显式完整停机恢复 |
| 裸机／SSH | MySQL、Redis 分布到三台 Linux 主机；MinIO 分布到四台 | 复用 SSH/sudo/指纹校验；全节点预检后分发部署资料，不同主机独立持久化 |
| Kubernetes | KubeBlocks MySQL 半同步三副本、Redis 三分片各两副本、MinIO 四副本 | 固定版 Operator/addon 首次安装；Cluster 渲染、创建、重复部署续等、Pod/PVC 状态检查 |

裸机路线当前通过 Docker Engine 和 Compose 运行服务，不包含不依赖容器的 systemd 原生安装器。

统一约束：

- 名称、组件、主机、版本、镜像、文件下载 URL/摘要和资源参数从 YAML 管理；默认下载源只在 config.example.yaml 定义。
- dry-run 不联网、不读秘密、不创建部署文件；render 不冒充已部署。
- 凭据不进入仓库或命令输出；Docker/SSH 按所选组件读取独立密码变量，KubeBlocks 由 Operator 生成 Secret。
- 拒绝接管已有无归属部署和覆盖不匹配配置；重试保留已有数据。不自动升级、扩缩容、轮换密码或强制重建。
- Kubernetes 显式选择存储类，默认保护删除并强制按节点分散；只有实验环境可显式允许同节点部署。
- 每个用户可见功能同步 README、配置模板、使用指南和验收记录；未发布版本统一标记 main。

## 验收要求与当前进度

| 验收项 | 状态 | 证据／限制 |
|---|---|---|
| Docker 三类组件首次部署及严格健康检查 | 已通过 | 实际 Docker Desktop Linux amd64 部署 |
| Docker 单成员故障后读写与入口切换 | 已通过 | MySQL Router、Redis Cluster、MinIO HAProxy |
| Docker 全部容器停止再启动的数据保留 | 已通过 | 三类均执行；MySQL 经显式完整停机恢复 |
| SSH 多主机编排、预检失败时不上传、跨主机 Redis 副本位置 | 自动化覆盖 | 尚未进行多台真实服务器部署／断电验收 |
| KubeBlocks 固定 Operator、MySQL/Redis/MinIO 定义安装 | 已通过 | 专用 Vagrant Kubernetes 集群；MinIO addon 单独安装 |
| KubeBlocks 三类集群的 Pod 与持久卷就绪 | 已通过 | MySQL 3、Redis 6、MinIO 4 个实例；允许同节点部署的实验配置 |
| KubeBlocks Redis 主 Pod 替换、读写和 PVC 保留 | 已通过 | 先确认副本同步，再替换键所在主 Pod；检查 PVC UID 和绑定卷 |
| KubeBlocks MinIO Pod 替换、同一入口读写和 PVC 保留 | 已通过 | 随机测试桶和对象，通过测试私网 NodePort 验收 |
| KubeBlocks MySQL 主 Pod 替换、主 Service 读写和 PVC 保留 | 已通过 | 先核对三个副本数据，再替换主 Pod；写 Service 转向其他可写主节点，故障前后数据正确，PVC UID 和绑定卷保持不变 |
| Windows 回归、Linux race、vet、差异检查 | 已通过 | 包含无依赖预览、归属拒绝、资源约束和配置修改保护 |
| 跨物理主机／机架／可用区故障、磁盘损坏和压力容量 | 未验收 | 单 Docker Engine 或同节点实验不能作为该项证据 |

Kubernetes 默认的保守调度约束为每实例一个节点，MySQL 至少 3 个、Redis 至少 6 个、MinIO 至少 4 个可调度 Linux 节点。这是当前实现约束；Vagrant 实验使用显式 allow_colocation，不将它算作物理故障域验收。

## 交付入口

- [Docker／SSH 使用指南](middleware.md)
- [KubeBlocks 使用指南](kubeblocks.md)
- [配置模板](../config.example.yaml)与[配置指南](configuration.md)
- [实际验收记录](middleware-validation-2026-10-28.md)
- [Vagrant 与可复验程序](../scripts/e2e/README.md)

已实现、已通过实际测试、仅有自动化覆盖及后续计划分别记录；本清单不将功能实现等同于生产环境验收。
