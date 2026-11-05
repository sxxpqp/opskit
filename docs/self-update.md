# MinIO 发布与 opskit 自更新

GitHub 保存源码、标签及正式 Release，MinIO 保存相同构建产物和签名版本清单，不需要 opskit-server。
下载桶为 `opskit`，桶域名为 `https://opskit.sxxpqp.top`，URL 中不重复桶名。上传使用独立的 S3 API 根地址，存放在 GitHub Secrets。

## GitHub Actions 配置

在仓库 Settings → Secrets and variables → Actions 中配置：

| 类型 | 名称 | 内容 |
|---|---|---|
| Secret | `MINIO_ENDPOINT` | HTTPS S3 API 根地址，包含所需端口，不含桶名或 Console 路径 |
| Secret | `MINIO_ACCESS_KEY` | 仅用于发布的访问密钥 |
| Secret | `MINIO_SECRET_KEY` | 对应私钥 |
| Secret | `UPDATE_SIGNING_PRIVATE_KEY` | base64 Ed25519 私钥，解码后 64 字节 |
| Variable | `UPDATE_PUBLIC_KEY` | 对应 base64 Ed25519 公钥，解码后 32 字节，构建时嵌入客户端 |
| Variable，可选 | `MINIO_BUCKET` | 默认 `opskit` |
| Variable，可选 | `MINIO_REGION` | 默认 `us-east-1`，与服务端设置保持一致 |

在可信本地终端执行下面命令生成签名密钥。输出含私钥，不要在 CI、共享日志或聊天中运行，不要提交输出：

```sh
go run ./cmd/release-publish --generate-key
```

将输出的两个值分别保存到同名 Secret / Variable。公钥是客户端信任根，不从下载桶自动获取；私钥泄露需要更换密钥并通过可信途径分发新公钥。更换公钥后，已有 stable 清单会因旧签名而阻止发布，需要管理员安排迁移，不能直接覆盖绕过。

标签发布构建会先检查必要配置；缺少配置则失败，不会默默跳过 MinIO。普通 main/PR 构建和测试无需上传凭据。首次启用时需发布包含 `update` 命令的新客户端，历史客户端不会自动获得新命令。

## MinIO 权限和路径

预先创建桶，配置 HTTPS、DNS 和桶域名访问。公开下载只需 `s3:GetObject`，不要开放匿名上传、删除或桶管理权限：

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"AWS": ["*"]},
    "Action": ["s3:GetObject"],
    "Resource": [
      "arn:aws:s3:::opskit/releases/*",
      "arn:aws:s3:::opskit/channels/*"
    ]
  }]
}
```

将此策略通过 MinIO 管理工具应用到桶；它只是匿名读策略，不是发布账号策略。发布账号仅需对上述两个前缀的 `s3:GetObject` 和 `s3:PutObject` 权限；不需要删除对象或列举整个桶。生产建议启用桶版本控制，以便管理员恢复错误发布的渠道指针。服务端需支持 S3 `If-None-Match` / `If-Match` 条件写入，工具不会降级为无条件覆盖。

对象结构：

```text
channels/stable.json
releases/<tag>/manifest.json
releases/<tag>/opskit_linux_amd64
releases/<tag>/opskit_linux_arm64
releases/<tag>/opskit_darwin_amd64
releases/<tag>/opskit_darwin_arm64
releases/<tag>/opskit_windows_amd64.exe
releases/<tag>/opskit_windows_arm64.exe
releases/<tag>/checksums.txt
releases/<tag>/config.example.yaml
```

发布作业在 GitHub Release 成功后运行：先检查全部构建文件和摘要，条件写入固定版本对象，再从公开桶域名逐个回读验证，发布签名 manifest，最后更新 stable。已有固定版本只允许复用内容完全相同的对象；不同内容必须使用新标签。渠道指针带条件写入，旧版本作业晚完成也不会将 stable 降级。

`releases/` 使用长期缓存；`channels/stable.json` 使用 `no-cache, max-age=0, must-revalidate`。反向代理/CDN 必须保留这些规则，不缓存渠道 404；首次发布前渠道文件不存在时，客户端报错，不会将其解释为“已是最新”。作业失败可在修复配置后重跑 `publish-minio`，不要重复创建 GitHub Release。GitHub Release 成功而 MinIO 作业失败时，stable 不会提前切换。

## 客户端使用

```sh
opskit update --check
sudo opskit update
opskit update --download-only --download-dir ./downloads
opskit update --help
```

Linux/macOS 自动更新：默认替换当前可执行文件，可用 `--install-dir /usr/local/bin` 指定公共 PATH 目录，实体文件权限为 `0755`。系统目录由 root 管理，普通用户均可读取和执行，无需 `0777`。还必须保证所有父目录可遍历，不能放在 `/root` 等私有目录内。初次安装时，在下载并核对摘要后执行 `sudo install -m 0755 ./opskit /usr/local/bin/opskit`。

`--check` 只读取签名清单，不下载或替换程序。`--version` 可选择指定的 `vX.Y.Z` 正式版本；相同版本不重复安装，拒绝降级。`--download-only` 验证摘要和可执行文件报告的版本后保存原发布文件名，已有文件不覆盖。Windows 支持检查与校验下载，需退出后手动替换 exe，暂不支持运行中的自动替换。

客户端仅在显式执行 update 时访问更新源，不在普通部署、备份等命令中自动更新。升级只替换二进制，不自动改写 opskit YAML 配置；指定的新目录不在 PATH 时，沿用工具安装的 shell PATH 注册流程。使用同目录更新锁、暂存和原子替换；成功保留 `opskit.backup-*`，替换后版本验证失败会恢复原文件。目录写权限不足会报错，请使用相应管理员权限。进程异常退出可能留下 `.opskit-update.lock`；确认无更新进程后再移除该锁。备份清理由管理员按需处理。

自更新拒绝软链接目标，避免悄悄修改私有目录里的旧文件。迁移旧安装时先按上述安装命令将公共 PATH 入口替换为实体文件。当前程序来自 `go install` 时，后续改用 Release 二进制后将不再由 Go 模块安装流程维护。

## 配置与信任

```yaml
update:
  download_base: https://opskit.sxxpqp.top
  public_key: ""  # 留空使用发布构建嵌入的可信公钥
```

根地址优先级为 `--update-download-base` > `OPSKIT_UPDATE_DOWNLOAD_BASE` > `update.download_base` > 嵌入模板。公钥优先级为 `--public-key` > `update.public_key` > 发布构建嵌入公钥。地址必须为 HTTPS，不含凭据或查询参数。使用旧配置无需增加字段即可读取模板默认地址；无嵌入公钥的本地构建必须从可信管理员获取并配置公钥。

清单使用 Ed25519 签名，记录完整构建版本、各平台文件大小和 SHA-256；客户端下载后先核对签名与摘要，再执行版本验证。此机制不等同于完整 TUF：没有自动轮换密钥、过期元数据或完整的防冻结保护。当前仅实现 stable 正式版本，beta 渠道、启动检查和定时自动升级仍为后续计划。

## 验证范围

Windows 本地全仓库测试通过，Linux WSL 定向测试通过（实体文件迁移、0755 权限、自更新安装与失败恢复），六种发布平台交叉构建通过。本地自动化覆盖签名篡改、错误公钥、摘要和大小不符、版本比较、HTTPS 下载、固定对象重试保护以及客户端检查与下载。实际 MinIO 凭据、DNS、TLS、匿名读策略、条件写入兼容性和完整发布流程，需要配置 Secrets 后通过发布流水线验收。未通过该流程的版本不能宣称已发布到 MinIO。
