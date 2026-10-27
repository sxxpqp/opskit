# Linux → OpsKit 能力迁移清单与安全边界

> 状态：分阶段实施。不能把本文件中的规划当作已发布能力。来源：[`sxxpqp/linux`](https://github.com/sxxpqp/linux)。本项目继续维持 Windows/Linux/macOS 构建目标，不将 shell-only 脚本直接拼进通用 CLI。
>
> 核心分工：**linux = 真实场景、参考配置、操作记录和实验；opskit = 可测试、有边界的工具化实现。**

## 当前第一阶段（本 PR）

新增 `opskit k8s diagnose POD -n NAMESPACE --kubeconfig FILE`（只读）：

- 获取 Pod 当前 `nodeName`、阶段及容器等待原因（如 `ContainerCreating`、`ImagePullBackOff`）。
- 关联 Pod 使用的 PVC、PV 名称与访问模式，并对照 Kubernetes CSI `VolumeAttachment` 的节点与 attached 状态。
- 对 StorageClass 名称包含 longhorn 的卷，可选查询 `longhorn-system` 下 Longhorn Volume 的 state、robustness、currentNodeID。
- 当 Pod 节点与 CSI/Longhorn attached 节点不一致时只**提出风险线索**；不推断卷一定已无人使用，不调用 detach/delete/patch。
- stdout 仅返回 schema_version=1 的 JSON、状态和 findings/warnings；未读取 Secret、Pod env 值和完整业务配置。证据缺失或发现问题返回非零，避免虚报健康。
- 最小测试覆盖跨节点挂载不一致、RBAC 拒绝读取和无 PVC Pod。
- 此命令依赖可用 kubectl；读取部分集群级对象需要 RBAC `get/list volumeattachments`，不应该为了运行诊断给整个生产账号授予 cluster-admin。

### 典型场景

源于 Pod `uat/redis-cluster-5` 被调度到 `k8swork6`，但 RWO Longhorn 卷仍在 `node05` 的故障排查经验：

```sh
opskit k8s diagnose redis-cluster-5 -n uat --kubeconfig /path/to/kubeconfig
```

程序只输出证据；旧节点上是否有活动进程占用卷、是否允许切换、是否有可用备份，**都不是该工具可以自动推断或执行的决定**。

## 后续按价值和风险分批迁移

| 优先级 | linux 现有素材 | OpsKit 目标能力 | 迁移前门槛 |
|---|---|---|---|
| P0 | [K8s 网络联通测试](https://github.com/sxxpqp/linux/blob/main/kubernetes/calico/test-connectivity.sh) | `k8s network diagnose`：只读组件/IPPool检查，再显式开启隔离连通性测试 | 使用单独命名空间、固定超时、保证清理；禁止在默认路径创建/删除集群资源 |
| P0 | [Containerd 安装与镜像源](https://github.com/sxxpqp/linux/tree/main/docker/containerd) | 扩充已有 `containerd install` / `mirror containerd` 的版本检测、配置预检和离线镜像验证，不新建重复命令 | 兼容 1.7/2.x、回滚配置、各平台验收、下载校验、无敏感地址硬编码 |
| P1 | [TLS Secret 批量更新](https://github.com/sxxpqp/linux/blob/main/kubernetes/cert/update-tls-secret-all-namespaces.sh) | `k8s secret cert-plan`（只读预览）→ 单独审批的 cert-apply | 校验证书/私钥匹配、禁止输出私钥、备份权限、资源版本冲突、幂等/失败恢复、测试环境 E2E |
| P1 | [etcd 备份与恢复](https://github.com/sxxpqp/linux/tree/main/kubernetes/etcd) | 加强现有 `etcd backup/check` 的恢复验证和可恢复证据 | 先实现隔离恢复验证，不宣称上传等于可恢复，不允许生产自动恢复 |
| P1 | [Node Cordon Watcher](https://github.com/sxxpqp/linux/tree/main/kubernetes/node-cordon-watcher) | 先提取状态机测试/决策模型；保留独立控制器，OpsKit 只负责检查或部署 | 修正 README 写的 DRY_RUN 默认 true 与 main.go 实际默认 false 的差异；验证 Leader Election、手工 cordon 保留、指标缺失和网络分区 |
| P2 | [Argo CD 发布](https://github.com/sxxpqp/linux/tree/main/kubernetes/argocd) | 可复用 GitOps 上线与回滚验收模板 | 与现有流水线去重、明确仓库和集群权限边界 |
| P2 | [GPU / Ollama / Dify](https://github.com/sxxpqp/linux/tree/main/ai) | AI 基础设施诊断、部署前检查；不是无条件复制各种安装脚本 | 校验系统/GPU/驱动兼容性，拒绝不可信下载，记录实测版本 |

## 必须先清理的代码风险

1. `linux` 包含真实环境配置、镜像源、集群地址及可能的凭据，不允许未经审计直接搬迁到公共发布产物。
2. 比如部分旧安装脚本有 `curl -k` 或远程脚本执行形式；迁移时必须用可信来源、摘要验证、显式确认和回滚替代。
3. 现有 `opskit` 已有 `k8s create/status/check`、`containerd install`、`mirror`、`etcd backup/check`，禁止复制出功能重叠且语义不同的入口。
4. 强制保护 `--kubeconfig` / `--context`，所有写操作需要 `--dry-run`、目标确认、最小权限、审计和隔离测试；诊断命令默认只读。
5. 每项功能分别区分“源码存在、单元测试通过、集成测试通过、真实环境验证、正式发布”，不因原仓库标注“生产验证”就直接认定迁移版合格。

## 每个后续 PR 的验收格式

- 要解决的实际场景、用户/使用者、减少的重复工作或风险。
- 原实现路径、目标 CLI 命令、与现有命令的功能交叉点。
- 失败场景与风险边界；尤其是 Secret、存储、证书与 HA 节点操作。
- 单元测试、负例、端到端证据；缺失的验证明确列出。
- 同 PR 更新 README、配置样例（如引入可配置下载源）、帮助及对应文档。
- 保留回滚方法；不自动合并、不自动操作生产环境。

## 为什么选择迁移而不是整体重写

`linux` 保留跨年真实实践知识，`opskit` 提供少量可靠功能。先让每个功能具备可验证的价值，再决定是否扩展。工具规模、文件数量、AI 生成代码量不应成为成功指标；真正的指标是修复所需时间、误操作风险、重复人工步骤及用户愿意持续使用的频率。
