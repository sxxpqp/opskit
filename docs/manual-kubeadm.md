# 组件下载与手动 kubeadm 部署

当前 main 提供分步入口，可只准备组件，再自行运行 kubeadm；完整自动部署仍使用 `opskit k8s create`。这些新增入口尚未发布，版本与下载入口以 README 正式 Release 为准。

## 命令分工

| 命令 | 行为 |
|---|---|
| `k8s download` | 只下载选中 Linux 组件，校验并保留文件，不连接 SSH、不安装 |
| `k8s check` | 通过 SSH 只读检查完整部署配置对应的节点环境 |
| `k8s status` | 使用 kubectl 查询节点版本、Ready 和 kube-system Pod |
| `calico install` | 在 kubeadm 初始化后独立安装 Calico，默认等待网络就绪 |
| `kubeadm install` | 本机安装 kubeadm/kubelet/kubectl/crictl 和 systemd 配置，仍需下载 |
| `kubeadm prepare` | 本机准备内核模块、转发与 swap；会修改系统配置 |
| `k8s create` | 执行已有 SSH 全流程，不把下载命令当作完整离线安装包 |

## 只下载组件

```sh
opskit config init
# 编辑生成配置中的组件版本和下载源；下载不要求配置真实节点密码
opskit k8s download --dry-run
opskit k8s download --download-dir ./downloads --arch amd64
opskit k8s download --components kubeadm,kubelet,kubectl,crictl,calico --arch arm64
opskit k8s download --components containerd,runc,cni-plugins --arch amd64
opskit k8s download --components etcd --etcd-version v3.5.5
```

默认组件为 `kubeadm,kubelet,kubectl,crictl`。完整支持列表：`kubeadm,kubelet,kubectl,crictl,etcd,containerd,runc,cni-plugins,docker,cri-dockerd,calico`。使用精确组件名，重复或未知组件会被拒绝。`--version` 覆盖 Kubernetes 版本，并按现有配套规则选择未显式配置的组件版本；运行时版本覆盖继续使用 YAML 对应字段。

支持在 Windows、macOS 或 Linux 管理机上下载 Linux amd64/arm64 文件。`--arch` 默认管理机架构，下载不同架构必须显式指定。下载阶段不执行二进制、不创建 systemd 服务，也不连接 SSH。`--dry-run` 不下载或创建目录。`--timeout` 默认 30 分钟，覆盖整个下载任务。

etcd 示例版本仅演示固定版本下载，不表示当前推荐或与任意 Kubernetes 版本兼容。下载 etcd 时必须设置 `--etcd-version` 或 `k8s.etcd.version`；不自动猜测版本。包保留 etcd/etcdctl/etcdutl 等上游内容，不自动解包、安装服务或部署外置 etcd。kubeadm 管理的本地 etcd 使用容器镜像，其版本由 kubeadm 决定，不受该字段影响。

### 产物结构与校验

所有选中资源成功后，输出独立任务目录。例如同时选择 kubelet、etcd、Calico 时：

```text
downloads/k8s-v1.24.7-linux-amd64-<任务标识>/
  downloads.json
  kubelet/
    kubelet
    kubelet.sha256
    kubelet.service
    10-kubeadm.conf
  etcd/
    etcd-v3.5.5-linux-amd64.tar.gz
    SHA256SUMS
  calico/
    network.yaml
    upstream/...
```

其余组件同样使用组件名子目录与上游文件名。`downloads.json` 记录产物相对路径、本地 SHA-256、来源 URL 与 `upstream_verified`。二进制包核对上游摘要后才发布完成目录；Docker/cri-dockerd 沿用现有下载限制，无上游摘要验证，记录为 `false`。kubelet 配套文本与渲染后的 `network.yaml` 记录本地摘要，`false` 不表示下载失败；Calico 原始下载仍核对模板中的固定 SHA-256。

上游摘要不匹配、下载失败或取消时停止并移除本次临时产物，不覆盖已有完成目录。每次执行创建新任务，不自动复用旧下载。下载文件没有安装为可执行命令，手动安装需设置权限。

这不是完整离线集群包：不包含控制平面/etcd/Calico 容器镜像、操作系统软件包或离线镜像仓库；后续拉镜像与系统依赖需自行准备。

## 下载源与优先级

各组件来源遵循 CLI > 对应组件环境变量 > YAML > 嵌入模板；组件变量只影响自身。默认根地址只定义在 `config.example.yaml`。

| 组件 | YAML | CLI | 环境变量 |
|---|---|---|---|
| kubeadm | `install.kubeadm.download_base` | `--kubeadm-download-base` | `KUBEADM_DOWNLOAD_BASE` |
| kubelet | `install.kubelet.download_base` | `--kubelet-download-base` | `KUBELET_DOWNLOAD_BASE` |
| kubectl | `install.kubectl.download_base` | `--kubectl-download-base` | `KUBECTL_DOWNLOAD_BASE` |
| crictl | `install.cri_tools.download_base` | `--cri-tools-download-base` | `CRI_TOOLS_DOWNLOAD_BASE` |
| etcd | `install.etcd.download_base` | `--etcd-download-base` | `ETCD_DOWNLOAD_BASE` |
| containerd | `install.containerd.download_base` | `--containerd-download-base` | `CONTAINERD_DOWNLOAD_BASE` |
| runc | `install.runc.download_base` | `--runc-download-base` | `RUNC_DOWNLOAD_BASE` |
| CNI plugins | `install.cni_plugins.download_base` | `--cni-download-base` | `CNI_PLUGINS_DOWNLOAD_BASE` |
| Docker | `install.docker.binary_base` | `--docker-binary-base` | `DOCKER_BINARY_BASE` |
| cri-dockerd | `install.cri_dockerd.download_base` | `--cri-dockerd-download-base` | `CRI_DOCKERD_DOWNLOAD_BASE` |
| Calico | `install.calico.download_base` | `--calico-download-base` | `CALICO_DOWNLOAD_BASE` |

etcd 根地址追加 `/etcd-io/etcd/releases/download/<版本>/etcd-<版本>-linux-<架构>.tar.gz`，并从同目录读取 `SHA256SUMS` 中精确文件名的摘要。Calico 沿用模板的备用根地址和固定 SHA-256，下载后按配置 Pod CIDR 渲染；`k8s.network.manifest` 指定本地清单时原样复制。下载目录为 `--download-dir` > `install.download_dir` > `~/.opskit/downloads`。YAML 相对路径基于配置文件，CLI 相对路径基于当前目录。

## 手动安装与初始化示例

以下使用项目历史测试组合说明操作顺序，不表示完成了新增入口的真实集群验收。先按环境准备 Linux/systemd、系统依赖、CRI 运行时、CNI plugins 及可访问镜像仓库。运行时与主机配置操作见[配置指南](configuration.md)，有 sudo 权限并确认目标主机后执行。

1. 在管理机下载 `kubeadm,kubelet,kubectl,crictl,calico`，选择与节点一致的架构；将完整任务目录复制到节点。
2. 在节点安装组件及 kubelet 服务文件：

```sh
cd /path/to/download-task
sudo install -m 0755 kubeadm/kubeadm kubelet/kubelet kubectl/kubectl /usr/local/bin/
sudo tar -xzf crictl/crictl-v1.24.2-linux-amd64.tar.gz -C /usr/local/bin crictl
sudo chmod 0755 /usr/local/bin/crictl
sudo install -m 0644 kubelet/kubelet.service /etc/systemd/system/kubelet.service
sudo mkdir -p /etc/systemd/system/kubelet.service.d
sudo install -m 0644 kubelet/10-kubeadm.conf /etc/systemd/system/kubelet.service.d/10-kubeadm.conf
sudo systemctl daemon-reload
sudo systemctl enable --now kubelet
```

crictl 文件名须替换为实际版本和架构。kubelet 在 kubeadm 写入配置前可能反复重启。若使用 `opskit kubeadm install` 安装工具，则无需手工安装上述文件，但该命令仍会下载，不会直接消费这份目录。

3. 配置主机与运行时，确认 CRI 可用后运行原生 kubeadm。以下命令需按实际 IP、版本、CIDR 和镜像仓库调整；原生 kubeadm 不读取 opskit YAML。

```sh
# 在每台节点执行；会修改内核设置、swap 和 fstab
sudo opskit kubeadm prepare --config /path/to/config.yaml
# 控制平面节点；运行时已安装并可用
sudo kubeadm init --kubernetes-version v1.24.7 \
  --apiserver-advertise-address 192.168.10.10 \
  --pod-network-cidr 10.244.0.0/16 --service-cidr 10.96.0.0/12 \
  --cri-socket unix:///run/containerd/containerd.sock
```

worker 按 kubeadm 输出的加入命令手动加入。容器运行时和镜像仓库需匹配实际配置；不要用自动创建命令接管手动集群。

4. 控制平面初始化后安装网络；可读取下载好的清单，不再下载 Calico YAML：

```sh
sudo opskit calico install --config /path/to/config.yaml \
  --kubeconfig /etc/kubernetes/admin.conf --manifest ./calico/network.yaml
sudo opskit k8s status --kubeconfig /etc/kubernetes/admin.conf
```

不传 `--manifest` 且 `k8s.network.manifest` 留空时，从 `install.calico` 下载并校验。独立入口只支持首次安装 Calico v3.26.5 / Kubernetes 1.24～1.28，检查实际服务端版本、kubeadm-config 的 Pod CIDR，以及已有 DaemonSet 中识别的 Calico/flannel/Cilium/Weave/kube-router/Antrea/Canal 名称或镜像；不承诺识别所有 CNI，不提供网络替换或升级。未安装网络时节点可以处于 NotReady，不以节点 Ready 阻止首次网络安装。

本地清单必须包含固定版本的 calico-node、calico-kube-controllers，并显式设置匹配的 `CALICO_IPV4POOL_CIDR`，不改写本地文件。默认预检、下载、apply 和就绪等待共用 10 分钟超时；`--wait=false` 只提交清单，`--dry-run` 只校验本地配置，不连接集群或下载。

## 预检与状态

```sh
opskit k8s check --config ./config/config.yaml --timeout 2m
opskit k8s status --kubeconfig /path/to/kubeconfig --context my-cluster
```

`check` 需要完整部署目标配置，复用 create 的基础主机检查；报告根分区与 `/var/lib` 的磁盘用量、缺少的系统工具，继续检查其他可连接节点，任一失败返回非零。不会上传节点程序或安装软件。已有手动集群触发 create 的初步保护是预期行为，应通过 `status` 查询其状态。该入口不验证节点间端口连通性、磁盘可用量门槛、镜像可拉取性或完整持久化归属。

`status` 和 `calico install` 默认遵循 kubectl 的 KUBECONFIG/默认文件，解析一次 current-context 后固定目标；不从 SSH hosts 或 kind 配置推断集群。opskit 自动部署的 kubeconfig 位于 `~/.opskit/clusters/<名称>/kubeconfig`，应显式传入。`status` 无节点或任一节点非 Ready 返回非零；kube-system Pod 表仅供诊断，不作为所有应用健康的证明。

这些入口已有模拟下载、摘要失败、取消、SSH 与 kubectl 调用边界的自动化覆盖；真实 kubeadm + Calico 的独立流程、多节点与恢复验收仍待完成。
