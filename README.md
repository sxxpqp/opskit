# opskit

逐步完善的 Go 运维工具箱。首批实现 etcd 快照备份和 S3 兼容存储上传，支持 Windows、Linux、macOS 的 amd64 / arm64。

## 安装与构建

GitHub Actions 在每次 main 提交后运行三平台测试，生成六种平台压缩包及 `checksums.txt`，可在 Actions 的 `opskit-packages` 下载。推送 `v*` 标签会创建附带安装包的 **草稿 Release**，确认后手动发布。

```sh
go build -o opskit ./cmd/opskit
./opskit version
./opskit etcd backup --help
```

Go 版本见 go.mod。二进制内置 S3 SDK；etcd 备份需另行安装与服务端匹配的 `etcdctl`，恢复和结构校验使用 `etcdutl`。

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

## 开发与迭代

```sh
go test ./...
go test -race ./...
go vet ./...
```

- 已实现：etcd 快照、SHA-256 清单、S3 上传、三平台六架构自动打包。
- 后续：配置文件、TCP/DNS/wget/ping、Linux ss/netstat、MySQL/PostgreSQL 备份、S3 下载/列举、备份保留策略、真实集成恢复测试。

完整规划见 [设计文档](go-opskit-design.md)。设计文档是目标规划，README 的进度代表当前实现。
