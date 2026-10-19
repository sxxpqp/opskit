# Kubernetes 版本自动匹配（从 1.24 起）

OpsKit 的目标是用户只需要设置 `k8s.kubernetes.version: v1.X.Y`（必须包含 patch），便能按**明确、审查过的依赖档案**自动解析其他组件，而不是下载上游的 `latest`。此功能已提交供 CI 审查；版本档案本身不等于所有版本都已真机安装验证。

## 使用

```yaml
k8s:
  kubernetes:
    version: v1.28.15
    containerManager: containerd
  containerd: {version: auto}
  runc: {version: auto}
  criTools: {version: auto}
  cniPlugins: {version: auto}
  network:
    # Kubernetes 1.24～1.28 时可留空，自动选择固定 Calico v3.26.5
    # Kubernetes 1.29～1.37 请填写已核对版本及镜像的本地 CNI 清单
    manifest: ""
    calico: {version: auto}
```

`opskit k8s create --config ./config/config.yaml --version v1.28.15 --dry-run`

`opskit kubeadm install --config ./config/config.yaml --version v1.35.1 --dry-run`

第二条只预览下载 kubeadm、kubelet、kubectl 和对应 crictl 的计划，不安装 CNI 或 Kubernetes 集群。指定 `--version` 优先于 YAML；组件字段设置为 `auto` 才会跟随目标 Kubernetes 版本。**显式配置的组件版本不会被悄悄覆盖**，不兼容的版本组合会在执行前失败。已有旧配置如果固定了 `criTools: v1.24.2` 等，需要手动改为 `auto`，否则选择 1.35 会按显式版本冲突拒绝执行。

## 内置组件版本档案（候选，不代表生产验收通过）

| Kubernetes minor | crictl | containerd | runc | CNI plugins | kubeadm 配置 | 内置网络清单 |
| --- | --- | --- | --- | --- | --- | --- |
| 1.24 | v1.24.2 | v1.7.24 | v1.2.6 | v1.5.1 | v1beta3 | Calico v3.26.5（校验 SHA-256） |
| 1.25～1.28 | 对应同 minor 的固定 crictl tag | v1.7.24 | v1.2.6 | v1.5.1 | v1beta3 | Calico v3.26.5（校验 SHA-256） |
| 1.29～1.30 | v1.29.0 / v1.30.1 | v1.7.24 | v1.2.6 | v1.5.1 | v1beta3 | 需要本地 `network.manifest` |
| 1.31～1.37 | 对应同 minor 的固定 crictl tag | v2.1.3 | v1.2.6 | v1.5.1 | v1beta4 | 需要本地 `network.manifest` |

具体 crictl 候选在 `internal/app/k8s_versions.go` 中固定；不会联网解析不确定的最新版。更高 minor 必须在版本档案、配置 API 和安装测试扩展后显式放行。已知 Kubernetes 1.24 等早期版本已经停止官方安全维护，仅供历史兼容/回归测试。

**重要：** Calico v3.26.5 的内置清单渲染器和固定摘要只适用于现有兼容区间 1.24～1.28。对于 1.29 及以后，本 PR 不会假装通用旧清单可直接运行，也不会无校验下载陌生 manifest；须使用适配目标 Kubernetes、可信来源且经过独立测试的本地网络清单。后续可单独增加各 minor 的 Calico Operator/Helm 安装与摘要锁定。

## 测试与安全原则

- 自动解析不会更改 `schema_version: 1`、命令名称或已有 Manifest JSON。
- kubeadm、kubelet、kubectl 使用同一 Kubernetes 完整版本；crictl 必须具有相同 minor，但补丁号独立。
- Kubernetes 1.31+ 产生 kubeadm `v1beta4` 配置，`kubeletExtraArgs` 采用列表格式。join 和 bootstrap token 配置使用相同 API 选择逻辑。
- `--dry-run` 不读取 SSH 密码、不连接节点、不修改系统；CNI 清单为本地文件时不自动下载或改写。
- 真正安装仍必须通过 Docker（受限环境）与可销毁 Linux 虚拟机的 E2E 验收，覆盖 kubeadm init、join、CRI、Calico、CoreDNS、节点 Ready；本 PR 的跨版本单元测试不能取代该门禁。
- OpsKit 当前 SSH 默认不做主机密钥验证，这是既有安全缺口，与版本自动匹配无关；生产部署前应修复。

参考上游：[Kubernetes kubeadm v1beta4](https://kubernetes.io/docs/reference/config-api/kubeadm-config.v1beta4/)、[Kubernetes 版本偏差规则](https://kubernetes.io/releases/version-skew-policy/)、[cri-tools](https://github.com/kubernetes-sigs/cri-tools)、[Calico 要求](https://docs.tigera.io/calico/latest/getting-started/kubernetes/requirements)。
