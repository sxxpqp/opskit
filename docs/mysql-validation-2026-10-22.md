# MySQL 镜像备份验收记录（2026-10-22）

结论：Windows Docker Desktop + MySQL 8.4.13 的指定测试数据已通过镜像导出、文件校验与隔离恢复；同时修复了 Windows bind 凭据权限导致 MySQL 忽略配置的问题。这是 main 未发布代码的验收，不表示生产环境、所有版本或所有后端已经验证。

## 环境与复现标识

| 项目 | 实际值 |
|---|---|
| 宿主机 | Windows、PowerShell、Docker Desktop Linux 引擎 |
| Docker Engine | 29.8.2 |
| 镜像标签 | `mysql:8.4`，本次实际服务端及客户端版本为 8.4.13 |
| 镜像 digest | `mysql@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69` |
| 成功任务 | `6e8f101ce4` |
| 执行时间（北京时间） | 2026-10-22 17:20:58 至 17:21:34 |
| 原始本地报告 | `.opskit/mysql-e2e/6e8f101ce4/report.json`（忽略目录，未提交） |
| 测试代码 | [tests/e2e/mysql/verify.ps1](../tests/e2e/mysql/verify.ps1) |

源库和恢复库均为本次新建的临时容器，使用独立 internal 网络，无宿主机端口映射；以临时 root 账号验证，未验证最小权限账号。备份命令显式把本地 mysql/mysqldump 路径设为不存在的名称，确认镜像方式不依赖本地客户端。容器及匿名数据卷、网络、临时凭据在结束后清理，镜像缓存和测试数据产物保留。

## 通过项目

| 检查 | 结果 |
|---|---|
| 两个 InnoDB 数据库的 gzip 导出 | 通过，未使用本地 mysql/mysqldump |
| 不压缩 SQL 导出 | 通过 |
| 产物大小与重新计算 SHA-256 | 与 manifest 一致 |
| 中文、单引号文本、十进制金额、含 NUL 的二进制字段 | 恢复后逐行一致 |
| 视图、存储过程、触发器、禁用事件 | 数量一致；视图查询、过程调用、触发器效果一致；事件保持禁用 |
| 真实服务器 CA + VERIFY_CA | 备份成功，证明 CA 文件经 tmpfs 路径正常读取 |
| 使用另一实例的 CA | 拒绝，无成功 manifest |
| MyISAM 表 | 默认拒绝，无成功 manifest |
| 成功备份临时认证目录 | 已清理 |
| 测试容器、客户端容器、网络 | 检查时无残留 |

关键数据比较值包含 `E4B8ADE69687`（中文 UTF-8）、`000127FF` / `00FF`（二进制）、`12.34` / `-99.50`（金额）。恢复后的过程返回 2 行，触发器为 NULL 文本填入 `default`，视图返回相同中文数据。

## 本次发现与修复

第一次就绪探测返回 `World-writable config file ... is ignored`，随后是无密码认证失败。虽然宿主机已限制 Windows ACL，Docker Desktop 的 bind 文件在 Linux 中仍被 MySQL 认定为不安全配置，原先直接读取 bind 中的客户端配置同样受影响。

修复后，只读挂载的认证/TLS 文件先由固定脚本复制至容器 tmpfs，目录 0700、文件 0600，再以独立 argv 执行客户端；用户参数不会拼入脚本。真实密码认证、CA 校验及恢复随后通过。

第二次数据比对定位到测试工具问题：备份 SQL 本身保留正确 UTF-8，但 Windows PowerShell stdin 管道把中文转为问号。脚本改为 `docker cp` 按字节传输 SQL，再由容器内客户端读取文件。最终逐行数据及对象行为比较通过，没有修改备份内容来迎合测试。

## 复跑

在 Windows 项目根目录准备 Docker Desktop Linux 引擎及 Go 工具链：

```powershell
go build -o .opskit/build/opskit_windows_amd64.exe ./cmd/opskit
docker pull mysql:8.4  # 本次验收版本示例；也可使用记录中的 digest
powershell -NoProfile -File tests/e2e/mysql/verify.ps1 -Image mysql:8.4
```

Docker 或 opskit 不在默认位置时使用 `-DockerBinary`、`-OpskitBinary`。省略 `-Image` 时从 opskit 嵌入模板获取默认镜像；脚本只使用本机已有镜像，不自行更换源或隐式拉取。输出报告和备份位于 `.opskit/mysql-e2e/<任务>/`，脚本不连接已有数据库。

本次还通过完整 `go test ./... -count=1` 与 `go vet ./...`。Race 因本机 CGO 环境未具备而未运行。MySQL 8.0、本地客户端的真实运行、Linux/macOS、VERIFY_IDENTITY/双向 TLS、真实 S3/MinIO、真实取消场景和生产恢复仍待验收；这些能力不能由本次结果外推。
