# 高可用中间件部署（v0.1.15）

本轮接入 MySQL InnoDB Cluster、Redis Cluster 和分布式 MinIO，参考 linux 项目的对应 Docker/裸机方案，重新实现 YAML 配置、资料生成、部署和状态检查。不会下载执行 linux 仓库的交互脚本，不读取其生产凭据，也不沿用其中的真实服务器地址。

两条路线均接入；本文介绍 Docker／SSH，Kubernetes 的 Operator 和 Cluster 命令见[KubeBlocks 指南](kubeblocks.md)。

需求范围、实施顺序和验收进度见[需求与验收清单](middleware-requirements.md)。

## 拓扑与验证范围

| 组件 | 部署拓扑 | 访问方式 |
|---|---|---|
| MySQL | 3 个 InnoDB Cluster 成员，单主模式；MySQL Router | Router 写端口 6446，跟随主节点变化 |
| Redis | 3 主 3 从；SSH 模式每台主机一个主实例和另一个主的副本 | 支持 Cluster 的客户端，提供多个种子地址 |
| MinIO | 4 个实例、每实例一个数据卷；HAProxy | Docker 入口端口 9000；SSH 每主机入口 9002，后端 API 9000 |

`driver: docker` 在一个 Docker Engine 内建立隔离容器网络，验证进程故障、自动切换和数据持久性；不提供宿主机故障隔离。`driver: ssh` 将实例分发到不同 Linux 主机，复用现有 SSH/私钥/密码/sudo/可选指纹验证。SSH 路径有渲染和模拟预检覆盖，本轮未在多台物理服务器验收。

本轮实际测试使用 Docker Desktop Linux amd64：MySQL 8.4.13、MySQL Shell/Router 8.4.0、Redis 7.2.4，以及 linux 归档镜像中的 MinIO。镜像引用集中在 `config.example.yaml`，工具和入口镜像使用实际拉取后的固定摘要。版本是本次功能验收组合，不代表所有版本、架构和生产容量均已验证。

## 本机 Docker 验证

先准备 Docker Engine 与 Compose；本命令不自动安装它们。

```sh
opskit middleware deploy --engine redis --dry-run
opskit middleware render --engine redis --directory /path/to/redis-deployment

# 从密钥管理系统或当前终端注入环境变量；不使用明文密码 CLI 参数
export OPSKIT_REDIS_CLUSTER_PASSWORD='<替换为16～128位随机密码>'
opskit middleware deploy --engine redis --directory /path/to/redis-deployment
opskit middleware status --engine redis --directory /path/to/redis-deployment
```

密码支持字母、数字和 `._@%+=:-`，长度 16～128。每种组件独立读取其 `password_env`：`OPSKIT_MYSQL_CLUSTER_PASSWORD`、`OPSKIT_REDIS_CLUSTER_PASSWORD`、`OPSKIT_MINIO_CLUSTER_PASSWORD`；其他组件的变量不会生效。内部 `.env` 为 Compose 提供凭据，宿主机同名 `OPSKIT_CLUSTER_PASSWORD` 不覆盖所选组件密码。

默认只在容器网络内提供服务，不发布到宿主机。需要从宿主机连接 MySQL Router 或 MinIO 时，在 YAML 中设置 `publish_port`；默认发布地址为回环地址。Redis Cluster 的节点地址为容器网络地址，Docker Desktop 外部客户端不能仅靠映射一个端口完成拓扑路由，应从同一网络测试，或使用 SSH 多主机模式的真实节点地址。

```yaml
middleware:
  name: demo-storage
  engine: minio
  driver: docker
  docker_context: desktop-linux  # Windows Docker Desktop 可使用；普通 Linux 通常留空
  bind_address: 127.0.0.1
  publish_port: 19090
```

MySQL 与 Redis 的 SSH 模式需要 3 台主机，MinIO 需要 4 台。Docker 模式不接受 hosts，避免将单宿主机验证误写成多主机部署。

## 多主机 SSH 部署

```yaml
middleware:
  name: orders-db
  engine: mysql
  driver: ssh
  hosts:
    - {name: db1, address: 192.0.2.11, internalAddress: 192.0.2.11, port: 22, user: ubuntu, privateKeyPath: ./keys/db1}
    - {name: db2, address: 192.0.2.12, internalAddress: 192.0.2.12, port: 22, user: ubuntu, privateKeyPath: ./keys/db2}
    - {name: db3, address: 192.0.2.13, internalAddress: 192.0.2.13, port: 22, user: ubuntu, privateKeyPath: ./keys/db3}
  mysql:
    password_env: OPSKIT_MYSQL_CLUSTER_PASSWORD
```

上面的地址为文档示例，须替换。`internalAddress` 必须是节点间可达且互异的 IPv4；私钥相对路径基于 YAML 文件。SSH 认证字段与 `k8s.hosts` 相同，包含 `passwordEnv`、`sudoPasswordEnv` 和可选 `hostKeySHA256`。

```sh
opskit middleware deploy --config /path/to/ha.yaml --dry-run
opskit middleware deploy --config /path/to/ha.yaml --timeout 30m
opskit middleware status --config /path/to/ha.yaml
```

先完成所有节点的 Linux、Docker、Compose 和 sudo 只读预检，再上传配置。SSH 使用 host 网络，各节点须预先允许组件通信端口：MySQL 3306（MYSQL 通信栈）与 Router 6446；Redis 6379/6380 和总线 16379/16380；MinIO 9000、Console 9001、HAProxy 9002。安装器不开放防火墙或修改网卡。

远端资料位于 `/var/lib/opskit/middleware/<name>-<engine>`；数据使用该项目独立的 Docker 命名卷。Redis 副本轮转放置：主 1 的副本在主机 3、主 2 的副本在主机 1、主 3 的副本在主机 2。必须自行确认这些地址属于独立故障域，而不是同一物理服务器的多个地址。

SSH 模式每台 MySQL 主机均运行 Router，每台 MinIO 主机均运行 HAProxy。客户端需要使用多个入口或已有的高可用负载均衡；这里不部署跨宿主机 VIP、DNS 故障切换或 Keepalived。跨主机 MinIO 默认使用内部 HTTP，公网入口 TLS、访问控制和网络隔离由现有基础设施配置。

## MySQL 完整停机恢复

单个主节点失效由 InnoDB Cluster 选举及 Router 切换处理。全部成员停机后，先启动容器，再显式恢复：

```sh
# Docker 模式：使用该部署资料的 Compose 文件启动已有容器
docker compose --project-name <name>-mysql -f /path/to/deployment/compose.yaml up -d
opskit middleware recover --engine mysql --config /path/to/ha.yaml --directory /path/to/deployment
opskit middleware status --engine mysql --config /path/to/ha.yaml --directory /path/to/deployment
```

SSH 模式应先启动各主机已有容器，再使用同一 YAML 执行 `middleware recover`，不需要本地 directory。恢复命令要求所有配置成员可连接；检查各成员 GR 状态与 GTID，选取包含其他成员事务集合的恢复种子，再调用官方 `dba.rebootClusterFromCompleteOutage`。不会使用 `force`、重置 GTID、强制 quorum 或直接打开 bootstrap 标志。存在部分活动成员、成员不可达、GTID 分叉时停止，保留数据供人工判断；全部成员已活动时不重复恢复。

本命令用于已有 InnoDB Cluster 的完整停机，不导入快照、修复损坏数据或替代备份恢复。`deploy` 发现已有元数据但 GR 未活动时，不会重新创建集群覆盖原状态。

## 归属、密码与数据

`render` 不读取秘密、不连接 Docker 或 SSH；`--dry-run` 不创建目录。部署使用归属记录、参数指纹及部署锁，拒绝无归属目录、同名的未受管资源、镜像/主机/发布参数变更以及隐式密码轮换。脚本兼容性修复可以随相同配置重跑，镜像升级与拓扑迁移需要独立方案。

管理目录为私密目录，`.env` 仅当前用户可读；Windows 设置当前用户与 SYSTEM ACL。挂载给工具容器的脚本和配置不包含密码值，允许非 root 容器读取。Docker 管理权限仍可查看容器环境，应由同一运维账号管理资料目录与 Docker。

Docker 本地部署锁为 `<directory>.deploy.lock`；SSH 锁为 `/var/lib/opskit/middleware/<name>-<engine>.deploy.lock`。异常退出留下锁时，先确认没有相关部署或恢复进程，再处理残留锁。CLI 不提供自动强制解锁或删除数据命令。

`status` 要求全部预期成员健康及写入口可用；单节点故障时业务仍可能可读写，但完整健康检查会返回非零。失败日志保留节点与阶段；重跑使用原资料目录、名称、密码和配置。不要用新密码重新初始化现有数据卷。

## 复验与后续范围

实际结果见[中间件验收记录](middleware-validation-2026-10-28.md)。隔离故障脚本位于 `scripts/e2e/middleware`，只接受带日期的专用 `opskit-ha-YYYYMMDD-<engine>` 项目，核对归属后停止节点、恢复并核对合成数据。

KubeBlocks 路线、Kafka、MongoDB、PostgreSQL、RabbitMQ、独立 etcd 集群部署，以及扩缩容、跨机房和 TLS 自动签发尚未接入本命令。linux 中的单实例模板不会被标记成高可用能力。
