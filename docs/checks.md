# S3 连通性与 etcd 集群检查

两个命令使用与备份、上传相同的当前目录配置、证书和厂家选择，命令行参数仍优先。所有探测均由用户显式执行，不在启动时自动访问远端。

## S3

```sh
opskit s3 check --timeout 15s
opskit s3 check --config ./config/config.yaml --key existing/snapshot.db --timeout 15s
```

必须配置 bucket。先使用 SDK 默认重试策略与任务总超时进行 HeadBucket，成功后如有 key 再做 HeadObject。不会上传测试文件或修改 bucket。

| 字段 | 含义 |
|---|---|
| `service_reachable` | 是否确认收到 HTTP 响应；false 也可能是凭据加载、DNS、TLS、连接或取消导致无法确认 |
| `bucket_access` | `ok`、`access-denied`、`not-found-or-hidden`、`http-error`、`connection-or-credentials-error`、`timeout-or-cancelled` |
| `object_access` | 仅成功检查 bucket 且配置了 key 后出现，状态含义同上 |
| `http_status` | 最近一次 HEAD 的响应状态码；无 HTTP 响应时可能缺省 |
| `elapsed_ms` | SDK 初始化、重试和请求总耗时 |
| `write_access` | 固定为 `not-tested` |

403 和 404 会返回非零退出码，但不被当作“服务断网”。HeadBucket 在不存在桶或权限不足时可能只返回通用 HTTP 错误，无法总是区分具体原因。HEAD 通过不证明 PutObject 可用，HEAD 被拒绝也不证明 PutObject 不可用。详见 [AWS HeadBucket 文档](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadBucket.html)。

如只想检查桶，可用 `--key=` 覆盖配置中的固定 key。检查不包含实际上传、下载内容校验或吞吐测速。

## etcd

```sh
opskit etcd check --timeout 30s --probe-timeout 5s
```

需要能执行匹配版本的 etcdctl，并具有成员查询、状态及健康探测所需权限。使用 member list 获取成员，随后按通告的 clientURLs 逐个执行 endpoint status 和 endpoint health；一个成员有多个地址时尝试到成功或该成员期限耗尽。不修改成员关系或用户键值，不创建快照；health 采用官方 etcdctl 探测机制。协议依据 [etcd 集群状态文档](https://etcd.io/docs/v3.5/tutorials/how-to-check-cluster-status/)。

| 汇总字段 | 含义 |
|---|---|
| `member_count` | member list 返回的注册成员总数，不是用户预期副本数 |
| `voter_count` / `learner_count` | 投票节点与不参与投票的 learner 数 |
| `healthy_members` / `healthy_voters` | 此次探测成功的成员及投票成员数 |
| `quorum_required` | 投票节点数的一半向下取整再加一 |
| `quorum_observed` | 此次成功探测的投票节点是否达到所需数量 |
| `healthy` | 存在观察到的 quorum，且所有注册成员均探测成功 |
| `nodes` | 每个成员的地址、健康、Leader、版本、数据库大小等详情 |

成员 ID 以十六进制字符串输出，避免 JSON 消费端把 uint64 当作浮点数造成精度丢失。status 中的 member ID 和 cluster ID 必须与发现信息匹配，才继续健康判断。成员没有通告 clientURL（例如尚未启动）也算异常，不会从节点总数中排除。

部分节点不可达时仍输出其他节点结果；member list 获取失败时输出 error，不能得出完整成员数量。总超时后保留已发现节点与已得到的结果。quorum 是此次顺序探测的观察值，不是持续可用性承诺；本命令不执行 hashkv 数据一致性检查。

输出 stdout 为 JSON，诊断与进程退出码可供脚本使用：0 全部检查通过，1 检查异常，2 参数/配置错误，3 缺少 etcdctl，4 总超时/取消。单成员探测超时计入该节点失败；默认单次发现或单节点探测期限为 5 秒。权限或认证失败不会被报告为“零节点且健康”。
