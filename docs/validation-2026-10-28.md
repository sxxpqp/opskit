# Vagrant 部署与恢复验收（2026-10-28）

本记录对应 `a4f353b` 之上的未提交工作区改动，不是正式 Release。验证使用 Windows 宿主机、Vagrant 2.4.9 与 VMware；既有单节点环境保留，另建两个独立测试节点。

## 环境与边界

| 项目 | 本次实际环境 |
|---|---|
| 控制平面 | Ubuntu 24.04.3 LTS / amd64，2 vCPU、3 GiB 内存 |
| 工作节点 | Ubuntu 24.04.3 LTS / amd64，2 vCPU、2 GiB 内存 |
| Kubernetes | v1.24.7，单控制平面 + 一个 worker |
| 运行时与网络 | containerd v1.7.28、runc v1.3.3、CNI plugins v1.9.1、Calico v3.26.5 |
| 独立恢复实例 | etcd / etcdctl / etcdutl 3.5.5，单独临时数据目录和回环端口 |
| S3 后端 | 用户已有 MinIO 配置；使用本轮随机测试前缀，不改已有对象 |

组件文件和容器镜像复用上次验证留下的缓存，组件继续执行程序原有摘要校验。使用测试 YAML 的统一 `install` 下载源，通过管理虚拟机提供临时 HTTP 文件服务；两台新节点依赖由项目正常调用 Ubuntu 软件源安装。这不是默认外网下载源速度测试，也不是完全断网安装验收。

## 实际通过项目

| 项目 | 结果 |
|---|---|
| 空机两节点 `k8s create` | 自动补齐系统依赖、安装运行时和 Kubernetes，控制平面初始化，worker 加入，两台均 Ready |
| 重复 `k8s create` | 使用同一配置成功，已有节点未重新加入或重建 |
| DNS / Service | worker 客户端解析 Kubernetes 和测试 Service，并通过 Service 访问 master 上的服务 Pod |
| 跨节点 Pod IP | master → worker 和 worker → master 均读回预期 HTTP 内容 |
| NetworkPolicy | worker 访问 master 服务先成功；deny 后失败；增加允许客户端规则后恢复 |
| 故障后重跑 | 暂停 worker kubelet 后，45 秒部署验证在 `master1/ready` 超时，错误包含节点、阶段和重跑指引；启动 kubelet 后相同配置重跑成功 |
| 归属与身份保持 | 故障测试前后两个节点 UID 与集群 CA SHA-256 均一致，两台重新 Ready |
| doctor | Windows 对实际部署配置检查通过，JSON 给出配置、kubectl、下载目录及集群配置结果；无写入权限时如实返回失败 |
| 本地 etcd 恢复 | 新建独立 etcd，只写入两条合成键；`etcd backup`、`backup verify`、`etcdutl snapshot restore` 后新实例读回两条预期值 |
| S3 文件链路 | 64 KiB 与 11 MiB 随机文件上传、列举、完整回读 SHA-256 和下载均通过；11 MiB 覆盖多段上传路径 |
| S3 防覆盖 | 同对象再次上传返回 HTTP 412，原内容保持；本地下载目标已存在时拒绝覆盖；指定错误摘要时拒绝发布目标文件 |
| S3 快照恢复 | 上传上述 20 KiB 合成数据快照，经新下载命令回读校验，再以独立端口恢复 etcd，两条合成键均正确 |
| S3 清理 | 本轮 3 个测试对象版本已清除，剩余版本、删除标记和未完成多段上传均为 0，临时凭据文件已移除 |
| 离线归档 | 已有 9 组件下载任务 + 约 426 MiB 容器镜像归档，生成约 613 MiB tar.gz；导出、导入、逐文件校验均通过 |
| Windows 回归 | `go test ./...` 与 `go vet ./...` 通过 |
| Ubuntu 回归 | `go test -p 1 -race ./...` 通过，app 包耗时 89.329 秒；`go vet ./...` 通过 |

新增自动化还覆盖：配置保留策略与 CLI 覆盖、清理预览不删除、保留最新任务、保护上传失败和损坏任务、归档路径穿越与大小限制、额外配置不导出、模拟 SSH 指纹匹配／不匹配、KS 密码默认隐藏与显式显示。上述模拟覆盖不代表 KS 已完成真实安装登录或所有平台文件系统均已实测。

## 失败与修正记录

- 初次 VMware 启动受宿主机执行权限影响，使用获准的宿主机执行后启动成功。
- 恢复脚本最初将主动终止原 etcd 的进程退出码误判为失败；调整预期的进程回收处理后，完整恢复和数据读回通过，未跳过快照摘要检查。
- 尝试构建额外临时 MinIO 实例时，固定镜像仓库返回 401；本次实际 S3 验证使用已有 MinIO 服务，不宣称新建了隔离 MinIO，也不推广该测试镜像为生产选型。
- S3 SDK 对多段对象输出 skipped multipart checksum 提示；本项目另行完整计算 SHA-256，并与上传前摘要及下载后字节对比，未将 HEAD 或该提示作为内容校验成功的依据。

## 复现与记录

可复用文件在 [scripts/e2e](../scripts/e2e/README.md)：Vagrantfile、双向网络与策略测试、独立 etcd 恢复测试、S3 随机对象与快照恢复测试。凭据和 kubeconfig 不纳入仓库。

本机日志位于 Git 忽略的 `dist/e2e/` 及专用管理虚拟机 `/home/vagrant/opskit/e2e-20261028/`，包含部署日志、`rerun.json`、`fault.json`、`resume.json`、`linux-race.log`、`etcd-recovery.log`、`s3-recovery.json`、`bundle.log`；网络日志位于测试 master 的 `/home/vagrant/multinode.log`。

验证结束后，两台新建测试机和本轮启动的管理验证机均已正常关机，保留磁盘与日志供复验；没有发布或推送版本。

尚未验收 HA、升级、集群删除、物理生产节点、其他版本/架构、大规模并发、生产 Kubernetes etcd 灾难恢复与真实 KS 登录。归档验收是文件交付验证，不包含自动解析完整镜像/系统包依赖或全断网自动部署。
