# 高可用中间件验收（2026-10-28～29）

对应 `a4f353b` 之上的未提交工作区，不是正式 Release。参考 `D:/code/linux` 的 MySQL InnoDB Cluster、Redis Cluster 和 MinIO 部署记录，使用 opskit 自己生成的资料实际部署，没有执行参考仓库脚本或连接其中的生产地址。

## 环境

- Windows 宿主机，Docker Desktop Linux amd64，Docker Engine 29.8.2、Compose 5.6.0。
- 新建专用项目：`opskit-ha-20261028-mysql`、`opskit-ha-20261028-redis`、`opskit-ha-20261028-minio`。
- 每个项目使用独立容器网络、命名数据卷和随机测试密码，资料位于 Git 忽略的 `dist/.middleware-e2e/`。
- MySQL 8.4.13，MySQL Shell/Router 8.4.0；Redis 7.2.4；MinIO RELEASE.2025-02-18T16-25-55Z，HAProxy 3.0.30。实际镜像引用及工具/入口固定摘要见 `config.example.yaml`。

## 实际结果

| 组件 | 验证 | 结果 |
|---|---|---|
| MySQL | 通过 deploy 创建三成员 InnoDB Cluster | 三成员均 ONLINE，一主两从，状态 OK |
| MySQL | Router 写端口 | 连接 Router，确认后端可写且 `read_only=0` |
| MySQL | 主节点停止 | Router 自动转向其他主节点，原数据可读、新数据可写 |
| MySQL | 原主启动 | 自动重入，三成员重新 ONLINE |
| MySQL | 全量停机 | 明确先 stop 全部 MySQL 和 Router，完成后再 start，避免将滚动重启当完整停机 |
| MySQL | 显式完整停机恢复 | recover 核对所有成员与 GTID，选择包含其余事务集合的种子，调用官方 rebootClusterFromCompleteOutage；未使用 force 或强制 bootstrap |
| MySQL | 恢复数据 | 故障前与故障后写入的两条记录均正确，Router 恢复写入口 |
| Redis | deploy 初始化 | 六个成员、三主三从，全部 16384 slots 正常 |
| Redis | 副本数据 | 停主前以 READONLY 连接指定副本确认合成数据已复制 |
| Redis | 主节点停止 | 副本自动提升，Cluster 客户端可继续读取和写入 |
| Redis | 全部停止再启动 | 六个实例全部 stop 后再 start，故障后写入数据仍正确 |
| MinIO | deploy 初始化 | 四节点分布式集群，各配置端点达到写 quorum |
| MinIO | 同一入口故障验证 | 经 HAProxy 回环地址写入随机对象，停止 minio1 后，相同地址仍可读原对象并写新对象 |
| MinIO | 全部停止再启动 | 四个后端全部 stop 后再 start，故障前后对象内容逐字节一致 |

最终隔离故障脚本均退出 0：

```json
{"engine":"redis","full_restart_persistence":true,"primary_failure_read_write":true,"success":true}
{"engine":"minio","full_restart_persistence":true,"single_node_failure_read_write":true,"success":true}
{"engine":"mysql","explicit_complete_outage_recovery":true,"full_restart_persistence":true,"primary_failure_router_read_write":true,"success":true}
```

## 验证中修正的问题

1. linux 参考脚本中的部分 Shell/Router 镜像标签已不可获取；改用实际拉取并验证的官方工具镜像，固定摘要。
2. 不能给 MySQL 服务端入口预先设置 MYSQL_PWD，否则官方镜像的首次无密码初始化被干扰；改为仅在客户端健康检查中临时设置。
3. 当前 Shell 不接受参考脚本的 interactive、waitRecovery 参数；使用当前 AdminAPI 的非交互选项和默认同步恢复行为。
4. MySQL 全部成员停止后会出现 GR OFFLINE；新增显式恢复，不把容器 running 当数据库集群恢复。
5. HAProxy 健康检查必须发送有效 HTTP/1.1 Host，否则 MinIO 返回 400，造成全部后端被误判 DOWN；已修正并实测同一入口故障读写。
6. 增加内部环境变量隔离，避免宿主机通用 OPSKIT_CLUSTER_PASSWORD 覆盖所选组件的私密部署配置。
7. 工具容器可能使用非 root 用户；挂载的无密码脚本/配置可读，凭据仍保留在私密目录与 0600 .env 中。

## 自动化与边界

Go 回归覆盖默认值来源、无依赖 dry-run、三类拓扑、跨主机 Redis 副本位置、全节点预检失败时不上传、参数归属与密码轮换拒绝、宿主机环境变量隔离和 render 不冒充已部署记录。

Docker 验证证明容器进程级故障行为和持久性，不代表跨物理主机、交换网络、磁盘损坏、跨机房或容量压力验收。SSH 多主机驱动本轮仅有渲染/模拟编排覆盖。MySQL 分叉 GTID 与数据损坏不自动强行恢复；MinIO、MySQL 的跨宿主机入口 VIP 不包含在本轮功能中。

本轮未注入真实 GTID 分叉或磁盘损坏，不将正常完整停机恢复结果外推为这两类故障已验收。Windows `go test ./...`、`go vet ./...` 和差异检查均通过。

可复验程序为 [scripts/e2e/middleware/main.go](../scripts/e2e/middleware/main.go)。日志和最终 JSON 在 `dist/.middleware-e2e/`；记录中不包含测试密码。用法见[中间件指南](middleware.md)及[测试说明](../scripts/e2e/README.md)。

## KubeBlocks／Vagrant 补充验收

使用既有专用 Vagrant Kubernetes v1.24.7 集群，master1 为 3 GiB、worker1 从 2 GiB 扩容至 4 GiB，各 2 vCPU。首次通过 `kubeblocks install` 安装固定 KubeBlocks 1.0.2 Operator、MySQL/Redis 1.0.2 bundled addon，以及单独的 MinIO 1.0.2 addon。Operator 与数据保护控制器各两副本；为实验降低其资源请求至 100m / 256Mi，保留 500m / 1Gi 上限，移除专用 master 的两个控制节点污点。此调优没有改写产品的生产默认值。

专用 namespace 与 StorageClass 为 `opskit-kb-e2e-20261028`。使用 16 个预先准备的 2 GiB 静态 hostPath PV、Retain 策略和 WaitForFirstConsumer，所有卷限制在 worker1。业务配置明确 `allow_colocation: true`、每实例申请 1 GiB；它是持久化功能与 Pod 替换实验，不提供宿主机故障隔离，不能作为默认强制反亲和策略下的跨节点 HA 验收。

| 项目 | 实际结果 |
|---|---|
| Operator 与定义 | 两类控制器各 2/2，mysql-8.0-1.0.2、redis-cluster-7-1.0.2、minio-1.0.2 定义存在 |
| MySQL 8.0.33 | 三副本均 3/3 Ready、三个 PVC Bound；写入独立测试库后核对所有副本，再替换主 Pod；主 Service 转向其他主节点且 read_only=0，旧数据可读、新数据可写，恢复后数据一致，PVC UID 与绑定 PV 保持不变 |
| Redis 7.2.4 | 六个 Pod 均 3/3 Ready、六个 PVC Bound；随机测试键 SET 与 WAIT 在同一连接确认副本同步，再替换键所在主 Pod；旧值可读、新值可写，Pod UID 改变，PVC UID 与绑定 PV 保持不变 |
| MinIO 2025.10.15 | 四个 Pod 均 2/2 Ready、四个 PVC Bound；通过固定测试私网 NodePort 写入随机桶和对象，替换一个 Pod；同一入口可继续读取旧对象、写入新对象，恢复后字节一致，PVC UID 与绑定 PV 保持不变 |
| 相同配置重跑 | 识别 opskit 归属与匹配 spec，继续等待现有 Cluster；没有重建资源或 PVC |
| 默认隔离约束 | 将 allow_colocation 改回 false，实际在两个可调度节点的实验集群运行 Redis deploy；预检以“至少需要 6”拒绝，没有修改现有 Cluster |

首次大镜像下载曾导致部署等待超时和 syncer ImagePullBackOff。资源和 PVC 均保留；MySQL、syncer 经 Docker Engine 预取原始镜像引用并导入测试 worker，未修改组件定义。国内候选缓存与原始 Docker Hub 引用的摘要分别一致：MySQL 8.0.33 为 `sha256:ea68e51ffe9b96fef6076f1218af11301aeaf13c6201e0ec9aaef5791d5ddc5d`，syncer 0.6.5 为 `sha256:894f13ed2608e2318588f5bbb02f605ad13bedceaf9e0fd4777883cb5d0d1169`。本次缓存复用不代表节点到 Docker Hub 的直接连接已修复。

官方 CRD 大文件下载超时后，使用 linux 仓库缓存；其 SHA-256 与 GitHub 官方 v1.0.2 Release 元数据的资产 digest 一致，再由安装命令校验。主 Chart 不包含 MinIO addon，已单独下载、校验和安装，避免沿用参考脚本的假设。

可复验程序：[KubeBlocks 业务与 Pod 替换验证](../scripts/e2e/kubeblocks/main.go)、[仅生成实验夹具的脚本](../scripts/e2e/kubeblocks-fixture.ps1)。原始日志与结果在忽略目录 `dist/.kubeblocks-e2e/`。凭据由 Operator 生成，测试程序仅在内存读取对应 Secret，数据库密码通过 exec stdin 传递，不放进参数或日志。

Windows 全量测试与 vet、Linux 全量 race 与 vet 均通过；最终节点筛选改动另通过 Linux KubeBlocks 定向 race 和 vet。Linux 日志为 `dist/kubeblocks-linux-go-verify.log`、`dist/kubeblocks-linux-final.log`。Vagrant 只验收了上述实验组合，没有验证多物理主机、机架／可用区失效、磁盘损坏、生产容量、在线升级或备份恢复。
