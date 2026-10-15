# 配置示例：Kubernetes etcd + MinIO / S3

完整字段说明在 [config.example.yaml](../config.example.yaml)。以下示例均使用当前实现支持的字段。

## kubeadm 控制平面节点：只备份到本地

```sh
./opskit config init
# 确认 opskit.yaml 中地址、证书路径正确，且 etcdctl 已安装：
./opskit etcd backup
```

生成模板已启用这些值：

```yaml
schema_version: 1
timeout: 1h
etcd:
  endpoint: https://127.0.0.1:2379
  directory: ./data/backups/etcd
  etcdctl: etcdctl
  cacert: /etc/kubernetes/pki/etcd/ca.crt
  cert: /etc/kubernetes/pki/etcd/healthcheck-client.crt
  key_file: /etc/kubernetes/pki/etcd/healthcheck-client.key
s3:
  bucket: ""
```

证书路径依据 [Kubernetes kubeadm PKI 文档](https://kubernetes.io/docs/setup/best-practices/certificates/#certificate-paths)。这是一套常见 kubeadm 配置，不代表所有 Kubernetes 发行版。K3s、RKE2、外置 etcd 或自定义 `certificatesDir` 需替换为实际路径。在远程电脑或容器执行时还需提供能访问的 etcd endpoint 和可读取的证书；`127.0.0.1` 指运行 opskit 的机器或容器自身。

`cacert` 用于验证 etcd 服务端，`cert` 和 `key_file` 是配套客户端证书与私钥。程序不会生成、复制证书，也不会从 kubeconfig 获取它们。模板中的值会传给 etcdctl；未加载配置时不自动套用 Kubernetes 路径。

## 上传到 MinIO

保留上方 `etcd` 配置，将模板的 `s3` 节修改为：

```yaml
s3:
  bucket: k8s-backups
  endpoint: https://minio.example.com:9000
  region: us-east-1
  path_style: true
  key: ""
```

域名、端口与桶名是示例，需替换成实际环境；endpoint 必须是 S3 API 入口。桶需提前创建。将 MinIO 服务账号凭据传给 AWS SDK：

```sh
export AWS_ACCESS_KEY_ID='replace-with-minio-access-key'
export AWS_SECRET_ACCESS_KEY='replace-with-minio-secret-key'
# 长期凭据不需要会话令牌，避免沿用旧的临时令牌：
unset AWS_SESSION_TOKEN
./opskit etcd backup
```

PowerShell 的环境变量写法：

```powershell
$env:AWS_ACCESS_KEY_ID = 'replace-with-minio-access-key'
$env:AWS_SECRET_ACCESS_KEY = 'replace-with-minio-secret-key'
Remove-Item Env:AWS_SESSION_TOKEN -ErrorAction SilentlyContinue
.\opskit.exe etcd backup
```

Windows 上使用时要将 etcd endpoint 与证书路径改为实际可用值。S3 凭据通过 [AWS SDK 默认凭据链](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html) 读取；也可使用共享凭据配置或工作负载角色。不要把占位符当成真实凭据；YAML 当前没有 `access_key` / `secret_key` 字段，也不会展开 `${AWS_ACCESS_KEY_ID}`。

## 三种“key”分别是什么

| 名称 | 用途 | 在哪里设置 |
|---|---|---|
| `etcd.key_file` | etcd TLS 客户端证书的私钥文件路径 | YAML 或 `--key-file` |
| Access Key / Secret Key | 对象存储账号的访问凭据 | `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` |
| `s3.key` | 桶内对象的完整名称 | YAML 或 `--key` |

例如桶为 `k8s-backups`，对象 key 为 `clusters/prod/snapshot.db`，其对象位置可表示为 `s3://k8s-backups/clusters/prod/snapshot.db`。但 **key 字段只填 `clusters/prod/snapshot.db`**。

定时备份建议保留 `key: ""`，每次自动生成唯一名称：

```text
k8s-backups（bucket）
└── etcd/
    └── 20261015T120000Z-<随机任务号>/
        ├── snapshot.db
        └── snapshot.db.manifest.json
```

自定义 key 不支持日期变量、环境变量或占位符展开，不会自动追加文件名。固定 key 重复运行会触发防覆盖错误，本地新快照仍保留。手工上传时可明确指定完整名称：

```sh
./opskit s3 upload --key manual/first-snapshot.db ./snapshot.db
```

## AWS S3

将 `s3` 节改为桶所在区域，endpoint 通常留空：

```yaml
s3:
  bucket: replace-with-your-bucket
  endpoint: ""
  region: ap-southeast-1
  path_style: false
  key: ""
```

临时凭据需同时提供匹配的 `AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY`、`AWS_SESSION_TOKEN`。上传账号需具备对象写入、HEAD 查询以及分段上传失败时的清理权限；后端必须支持条件写入。opskit 不创建桶、不修改桶权限。

## 排查常见配置问题

| 现象 | 检查方向 |
|---|---|
| etcd 证书文件找不到 | 是否为 kubeadm 节点；文件路径是否正确；容器是否挂载了证书 |
| etcd 证书认证或权限失败 | 客户端证书是否成对、未过期，etcd 认证授权是否允许快照 |
| etcd 服务端证书验证失败 | CA 是否属于该 etcd，连接地址是否在服务端证书 SAN 中 |
| S3 找不到凭据 | 环境变量是否导出到了运行任务的进程；cron/systemd 不自动继承交互 shell |
| S3 403 | 访问密钥、会话令牌、桶权限及后端策略；不会自动改用匿名上传 |
| 对象已存在 | 自定义 key 是否重复；改用新 key 或让 etcd 备份自动生成 |
| S3 私有 CA 不受信任 | 将 CA 安装到运行机器的系统信任库；当前未提供 S3 自定义 CA 配置字段 |

上传后 HEAD 比对大小与 SHA-256 metadata，不能替代内容回读校验或恢复演练。
