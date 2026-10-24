# OpenEBS Operator 安装

main 提供 `opskit openebs install`，尚未发布。使用 kubectl 应用官方 3.x `openebs-operator.yaml`，不需要 Helm 或 OLM。

## 安装范围

本项目主要用这条 Operator 路径为 KubeSphere 3.x 提供持久存储。当前内嵌 KubeSphere 安装入口固定支持 v3.4.1；需要先有可用且唯一的默认 StorageClass，衔接步骤见下一节。

默认清单固定到官方仓库提交 `4143c90d07948b84024a7a00c790ce1449e244e1`，其整体标签为 3.5.0，Local PV provisioner 镜像为 3.4.0、NDM 为 2.1.0。不同组件标签不等于统一镜像版本。当前 4.x 安装文档主要采用 Helm，本命令不实现该路径。

清单创建 `openebs` namespace、ServiceAccount/RBAC、BlockDevice/BlockDeviceClaim CRD、NDM 与 Operator、导出器、Local PV provisioner 和 `openebs-hostpath`／`openebs-device` StorageClass。需要集群管理权限、可运行这些镜像的 Linux 节点及可用的 Pod 网络。镜像源由节点现有容器运行时配置决定，下载 YAML 的代理不替代镜像代理。

默认不选择磁盘、不格式化、不创建 PVC，不部署 cStor、Jiva、Mayastor，也不自动安装 iSCSI。Hostpath 使用节点目录 `/var/openebs/local/`；Device 需要额外准备与选择符合要求的空闲设备。Local PV 不提供跨节点副本，节点不可用时不能靠其他节点读取该目录的数据。

## 使用

```sh
opskit openebs install --kubeconfig /etc/kubernetes/admin.conf --dry-run
opskit openebs install --kubeconfig /etc/kubernetes/admin.conf
```

默认固定 kubectl 的 current-context，`--context` 可显式指定。每个后续查询与写入都使用同一 kubeconfig/context，不读取 SSH hosts 推断集群。

安装前检查 API 可用性与清单内同名资源；除预先创建的 namespace 外，已有任何同名资源均拒绝覆盖。不是升级或接管入口；部分失败不自动删除已经创建的 CRD、资源或存储数据，重试前先检查并处理现场。

`--timeout` 默认 15 分钟，下载、预检、安装和就绪等待共享总期限。默认等待 CRD Established 和清单内全部 Deployment、DaemonSet rollout；`--wait=false` 仅提交，不表示存储可用。`--dry-run` 不下载、不连接集群、不改变配置。

## 为 KubeSphere 3.x 准备存储

在新集群上先安装 OpenEBS 并设置默认类，再预检、安装 KubeSphere；以下 kubeconfig 必须指向同一集群：

```sh
sudo opskit openebs install --kubeconfig /etc/kubernetes/admin.conf --set-default
sudo opskit kubesphere install --version v3.4.1 \
  --kubeconfig /etc/kubernetes/admin.conf --storage-class openebs-hostpath --dry-run
# 预检通过后安装
sudo opskit kubesphere install --version v3.4.1 \
  --kubeconfig /etc/kubernetes/admin.conf --storage-class openebs-hostpath
```

也可将 `openebs.set_default` 设为 `true`，在 `k8s.kubesphere.storageClass` 中填 `openebs-hostpath`。新建集群时先保持 `k8s.kubesphere.enabled: false`，完成 `k8s create` 后再按上述顺序安装 OpenEBS 和 KubeSphere。安装 OpenEBS 与安装 KubeSphere 是两个独立步骤，开启 KubeSphere 配置不会自动安装 OpenEBS。

已通过本入口安装 OpenEBS、但之前没有设默认类时，不重复执行 install。先确认 OpenEBS Pod 就绪、PVC 供给正常，并用 `kubectl get storageclass` 确认没有其他默认类，再执行：

```sh
sudo kubectl --kubeconfig /etc/kubernetes/admin.conf annotate storageclass openebs-hostpath \
  storageclass.kubernetes.io/is-default-class=true --overwrite
```

设置后再运行上述 KubeSphere `--dry-run`。指定 `--storage-class` 不能绕过当前安装器对唯一默认类的检查。Hostpath 可用于本项目单节点功能验证，其节点本地语义不会因 KubeSphere 使用它而变成多副本存储。

## 配置与下载

```yaml
openebs:
  manifest: ""       # 可选，本地 NDM + Local PV Operator 清单
  set_default: false
install:
  download_dir: /opt/opskit/downloads
  # openebs.operator_url 与 sha256 的固定官方值见 config.example.yaml
```

下载地址优先级为 `--operator-url` > `OPENEBS_OPERATOR_URL` > `install.openebs.operator_url` > 嵌入模板。该环境变量仅影响 OpenEBS。摘要优先级为 `--sha256` > `install.openebs.sha256` > 模板。替换下载源时必须提供相同原始清单字节，或者显式配置对应摘要；下载完整性和 YAML 对象检查都通过后才连接集群安装。

同一 `install.download_dir` 复用经本地 SHA-256 校验的完整缓存，随后仍核对配置的清单摘要。默认下载源只在模板定义，不跟随浮动 latest；默认通过项目既有 Nexus 代理获取官方固定文件，代理缓存不是独立镜像。切换官方直连或自有代理时可修改该 URL，清单摘要保持一致。

`install.openebs.fallback_urls` 配置备用清单完整 URL。模板默认同时将 Nexus 保留为备用，因此通过 CLI／环境变量／YAML 指定 GitHub 等其他主源后，下载失败或单源 60 秒超时会继续尝试 Nexus；`[]` 禁用备用源。主源与备用相同会去重，不重复请求。所有尝试共用命令总超时，父任务取消后不再回退；每次尝试独立缓冲，未完成的内容不拼接到下一源。所有源必须通过同一 SHA-256，摘要不匹配立即停止，修改集群前退出。

`--manifest` 或 `openebs.manifest` 指定本地文件时不下载、不改写文件；本地文件视为用户提供的安装内容，仍检查 namespace、必需的 NDM／Local PV／CRD／StorageClass 结构。YAML 相对路径基于配置文件目录，CLI 相对路径基于当前目录。固定支持 `openebs` namespace，不支持直接传入其他引擎或 4.x Helm 渲染清单。

## 默认 StorageClass

默认保留集群现状。在首次安装时按需选择：

```sh
opskit openebs install --kubeconfig /etc/kubernetes/admin.conf --set-default
```

这会将 `openebs-hostpath` 设置为默认；若已有其他默认存储类，则在安装前拒绝，并在设置前再次检查，不自动取消其他默认类。禁止在本地清单中直接声明默认类，请通过该参数或 `openebs.set_default` 表达选择。

Hostpath 类使用 `WaitForFirstConsumer`：仅创建 PVC 时保持 Pending 可能是正常行为，创建消费它的 Pod 后才选择节点并绑定。已有 OpenEBS 不使用本命令重复安装来更改默认类。

## 检查

```sh
kubectl --kubeconfig /etc/kubernetes/admin.conf -n openebs get pods
kubectl --kubeconfig /etc/kubernetes/admin.conf get storageclass
kubectl --kubeconfig /etc/kubernetes/admin.conf get crd blockdevices.openebs.io blockdeviceclaims.openebs.io
```

工作负载就绪不等于 PVC 持久性验证。业务验收应创建指定 `storageClassName: openebs-hostpath` 的 PVC 和消费 Pod，验证写入、删除并重建 Pod 后读回，再按业务要求验证节点故障与备份恢复。

## 实际验证（2026-10-24）

Windows + VMware 的 Ubuntu 24.04.3 单节点，Kubernetes v1.24.7、containerd v1.7.28、Calico v3.26.5，使用本节固定的 Operator 清单完成验证：

- 2 个 CRD Established，NDM、Operator、两个导出器和 Local PV provisioner 共 5 个 Pod 全部 Running。
- 默认未更改默认 StorageClass；重复安装被拒绝。
- 指定 `openebs-hostpath` 的 64Mi RWO PVC 动态绑定成功。
- 消费 Pod 写入测试文件，删除并由 Deployment 重建为不同 UID 的 Pod 后，原文件内容读回一致。
- 测试 namespace、PVC 与 PV 已删除；保留 OpenEBS 安装供后续测试。
- Ubuntu 完整 `go test -p 1 -race ./...` 和 `go vet ./...` 通过；Windows 命令、配置优先级、摘要和预检保护测试通过。

GitHub 直连在虚拟机曾发生 TLS handshake timeout，尚未修改集群即退出；改用 Nexus 地址获取同一 SHA-256 的文件后成功。容器镜像通过既有 containerd 代理下载，未改写清单内镜像。测试脚本位于本机忽略目录 `.opskit/openebs/verify.sh`，原始记录汇总为 `.opskit/openebs/validation.json`。

这些结果仅覆盖单节点 Hostpath。Device 裸盘供给、多节点与节点故障、备份恢复、复制存储引擎、其他 Kubernetes／OpenEBS 组合仍未验收。

随后按 KubeSphere 3.x 用途补测：确认没有其他默认类后，将测试集群的 `openebs-hostpath` 设为唯一默认类，`opskit kubesphere install --version v3.4.1 --storage-class openebs-hostpath --dry-run` 实际预检通过。测试环境当前保留该默认类；未启动 KubeSphere 安装，未创建 `kubesphere-system`。此补测记录位于 `.opskit/openebs/ks-preflight.json`，不代表 KubeSphere 核心服务已经完成验收。

官方参考：[3.x 安装说明](https://openebs.io/docs/3.10.x/user-guides/quickstart)、[固定 Operator 清单](https://github.com/openebs/charts/blob/4143c90d07948b84024a7a00c790ce1449e244e1/openebs-operator.yaml)、[Local PV Hostpath](https://openebs.io/docs/3.10.x/user-guides/localpv-hostpath)、[当前安装方式](https://openebs.io/docs/quickstart-guide/installation)。
