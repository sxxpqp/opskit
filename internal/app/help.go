package app

const commandHelpTemplate = `用法: {{if .HasAvailableSubCommands}}{{.CommandPath}} <命令> [参数]{{else}}{{.UseLine}}{{end}}
{{if .Long}}
{{.Long}}
{{else if .Short}}
{{.Short}}
{{end}}{{if .HasAvailableSubCommands}}
命令:
{{range .Commands}}{{if .IsAvailableCommand}}  {{rpad .Name .NamePadding}} {{.Short}}
{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}
参数:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}
{{end}}{{if .HasAvailableInheritedFlags}}
公共参数:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}
{{end}}{{if .Example}}
示例:
{{.Example}}
{{end}}
使用 {{.CommandPath}}{{if .HasAvailableSubCommands}} <命令>{{end}} --help 查看参数。
`

var commandGroupHelp = map[string]string{
	"kubeblocks": `用法: opskit kubeblocks install|render|deploy|status [参数]
固定版 KubeBlocks Operator 与 MySQL/Redis/MinIO addon；下载源和摘要统一在 kubeblocks。
install 仅首次安装，拒绝接管已有 CRD；需要 kubectl 和 Helm 3.10+。
deploy 创建保护删除的 Cluster，要求 namespace 与显式 StorageClass 已存在，不自动升级或扩缩容。
默认 required 反亲和；实验环境可显式 allow_colocation，但不代表主机故障隔离。
Operator 生成 Secret，命令不输出凭据；status 检查控制器/Pod/PVC，业务验收另行执行。
示例: opskit kubeblocks deploy --engine redis --dry-run`,
	"middleware": `用法: opskit middleware render|deploy|status|recover [参数]
首批组件: mysql（三节点 InnoDB Cluster + Router）、redis（三主三从）、minio（四节点 + HAProxy）。
配置读取 middleware，镜像与各组件密码环境变量集中在模板。
driver=docker 用于单宿主机容器故障验证；driver=ssh 分发到不同 Linux 主机。
deploy 默认执行，--dry-run 不读取秘密或连接节点；render 只生成 Docker 资料。
recover 仅用于 MySQL 完整停机，检查所有成员/GTID，不使用 force。
示例: opskit middleware deploy --engine redis --dry-run`,
	"openebs": `用法: opskit openebs install [参数]
通过官方 3.x Operator YAML 安装 NDM 和 Local PV，固定 openebs namespace。
为 KubeSphere v3.4.1 准备存储时，首次安装加 --set-default，之后执行 kubesphere install。
清单下载地址与摘要读取 install.openebs；共享 install.download_dir 的下载缓存。
下载失败或单源 60 秒超时后，按 install.openebs.fallback_urls 回退 Nexus 等备用源；摘要不匹配不回退。
--manifest 使用本地清单；--operator-url / OPENEBS_OPERATOR_URL 覆盖下载地址。
默认等待 CRD 和工作负载就绪，--wait=false 仅提交；--dry-run 不下载或连接集群。
拒绝覆盖已有同名资源；--set-default 显式设置 openebs-hostpath，存在其他默认类时拒绝。
不安装复制存储引擎，不格式化或分配裸盘；Local PV 不提供跨节点副本。
示例: opskit openebs install --kubeconfig /etc/kubernetes/admin.conf --dry-run`,
	"go": `用法: opskit go install [参数]
安装完整 Go SDK，校验 SHA-256、实际版本并创建 go/gofmt 命令入口。
支持 Linux/macOS amd64、arm64；版本读取 go.version，--version 可覆盖。
安装后根据 mirrors.go 设置当前用户的持久 GOPROXY；默认执行，--dry-run 仅预览。
示例: opskit go install --version 1.26.2
使用 opskit go install --help 查看参数。`,
	"k8s": `用法: opskit k8s create|download|check|status [参数]
download 只下载所选 Linux 组件，不要求 SSH/root；默认 kubeadm,kubelet,kubectl,crictl。
--components 支持 etcd（需 --etcd-version）、calico 和运行时组件；--arch amd64|arm64。
check 通过 SSH 只读检查部署环境；status 使用 kubeconfig 查询节点和系统 Pod。
手动 kubeadm 流程可先 download，再自行安装组件、初始化集群，最后执行 calico install。
相同下载目录复用完整且 SHA-256 校验通过的文件，缺失或损坏时重新下载；不跳过上游校验。
集群名填写在配置的 k8s.name（例如 sxxpqp），不接受 create sxxpqp 这样的参数。
首次执行 opskit config init，编辑节点、角色和版本后创建。
network.manifest 留空自动安装固定版本 Calico；填写本地清单可覆盖。
--calico-version 覆盖 k8s.network.calico.version；当前支持 v3.26.5 / Kubernetes 1.24～1.28。
原生 SSH 集群编排，不依赖 KK；组件版本按 k8s.kubernetes.version 自动配套，允许显式覆盖。
支持单 master（同节点 etcd）、可选 worker、containerd 或 Docker 二进制运行时。
每台节点需 Linux + systemd、SSH 和 root/sudo 权限；自动用 apt-get/dnf/yum 安装缺失系统依赖。
Ubuntu/Debian: sudo apt-get update
  sudo apt-get install -y conntrack socat iptables iproute2 kmod procps util-linux mount tar ethtool systemd
上述命令使用发行版候选版本；固定版本使用 包名=版本。
SSH 使用配置凭据直接连接，不读取 known_hosts、不确认或校验主机指纹。
默认执行，--dry-run 只校验配置和展示计划；KubeSphere 默认关闭。
示例: opskit k8s create --config config/config.yaml --dry-run`,
	"calico": `用法: opskit calico install [参数]
为手动 kubeadm 初始化后的集群单独安装 Calico，不执行 init/join。
检查实际 Kubernetes 1.24～1.28、kubeadm Pod CIDR 和已识别的网络插件，默认等待就绪。
固定 Calico v3.26.5；配置来自 k8s.network 和 install.calico。
使用 --kubeconfig / --context 选择集群；默认 KUBECONFIG 或 ~/.kube/config。
--dry-run 只检查本地配置，不连接集群或下载。
示例: opskit calico install --kubeconfig /etc/kubernetes/admin.conf`,
	"kubeadm": `用法: opskit kubeadm install|prepare|init|join [参数]
install 安装固定版本 kubeadm/kubelet/kubectl/crictl 和 kubelet systemd unit，不安装 containerd。
prepare 自动安装缺失的 conntrack、socat 等系统依赖，再配置内核模块、转发和 bridge sysctl、禁用 swap（备份 fstab）。
init 初始化单控制平面；join 加入工作节点。均拒绝覆盖已有节点。
版本读取 k8s.kubernetes.version，CLI --version 优先；当前支持 Kubernetes 1.24～1.30。
默认执行，--dry-run 只预览；各命令 --help 查看参数。
网络清单通过 k8s.network.manifest 或 init --network-manifest 指定本地文件。`,
	"kubectl": `用法: opskit kubectl install [参数]
安装 Kubernetes 客户端 kubectl，不创建集群或修改 kubeconfig。
自动识别 Linux/macOS amd64、arm64 或 Windows amd64；校验 SHA-256、客户端版本和 PATH。
默认直接安装，--dry-run 仅预览。
版本优先级: --version > k8s.kubernetes.version。
下载源优先级: --kubectl-download-base > KUBECTL_DOWNLOAD_BASE > install.kubectl.download_base > 模板默认 Nexus 代理。
旧 --download-base 已移除，请使用 --kubectl-download-base。
示例:
  opskit kubectl install
  opskit kubectl install --version v1.24.7
  opskit kubectl install --dry-run
使用 opskit kubectl install -h 或 --help 查看完整参数。`,
	"kind": `用法: opskit kind <命令> [参数]
install 发现缺少 kubectl 或 Docker 时会提示安装命令；创建集群前请完成配套工具安装。

示例:
  opskit kind install --version v0.17.0
  opskit kind create --version v1.24.7
  opskit kind status

使用 opskit kind <命令> --help 查看具体参数。`,
	"mirror": `用法: opskit mirror <类型> [参数]

类型: docker | containerd | go | npm | pip | maven
默认备份后写入，--dry-run 仅预览；Linux 标准 Docker 配置写入后自动重启服务。
示例: opskit mirror containerd --help`,
	"docker": `用法: opskit docker install [参数]
安装 Docker；默认使用官方脚本和配置的软件包源。
脚本/二进制安装均自动配置并验证 inotify 限额（instances≥8192，watches≥524288）。
示例: opskit docker install --version v27.3.1
二进制方式: opskit docker install --version v27.3.1 --method binary
使用 --dry-run 预览；install --help 查看全部参数。`,
	"cri-dockerd": `用法: opskit cri-dockerd install [参数]
安装 Docker 的 Kubernetes CRI 适配器。
示例: opskit cri-dockerd install --version v0.3.22
使用 --dry-run 预览；install --help 查看全部参数。`,
	"containerd": `用法: opskit containerd install [参数]
安装 containerd、runc 和 CNI plugins。
示例: opskit containerd install --version v2.1.3
使用 --dry-run 预览；install --help 查看全部参数。`,
	"kubesphere": `用法: opskit kubesphere install [参数]
默认执行安装，--dry-run 仅做只读预检。
默认读取 KUBECONFIG 或 ~/.kube/config 的 current-context；支持 --kubeconfig、--context 和 --kind 覆盖。
安装 KubeSphere，检查 Kubernetes 版本和存储等前置条件。
示例: opskit kubesphere install --version v3.4.1 --help`,
	"config": `用法: opskit config init [参数]
生成统一配置模板；保留已有配置，不覆盖。
示例: opskit config init --help`,
	"etcd": `用法: opskit etcd <命令> [参数]
命令: backup（快照备份） | check（集群健康检查）
示例: opskit etcd backup --help`,
	"mysql": `用法: opskit mysql backup [参数]
使用本地 mysql/mysqldump，或 --client-mode docker 在 MySQL 镜像内导出，无需本地客户端。
支持 gzip、SHA-256 清单及可选 S3 上传；镜像读取 mysql.docker.image，--image 可覆盖。
Docker 模式仅需可用的本机 Linux Docker 引擎；--docker-network 选择网络，--host 为容器可达地址。
目标来自 mysql.databases，或重复 --database；不隐式导出整个实例。
默认事务模式并拒绝非 InnoDB 表；并发 DDL 和非事务表的一致性需另行安排。
密码通过 mysql.password_env 指定的环境变量读取；临时凭据不进入命令行、日志或清单。
默认要求 TLS；--dry-run 只输出计划，不读取密码或连接数据库。
示例: opskit mysql backup --database app --dry-run
详细参数: opskit mysql backup --help`,
	"s3": `用法: opskit s3 <命令> [参数]
命令: upload（上传） | check（只读 HEAD 检查） | list（列举） | download（下载并校验） | verify（完整回读校验）
download / verify 需要 --key；download 还需 --file，拒绝覆盖。优先使用 --sha256，否则使用对象元数据摘要。
示例: opskit s3 upload --help`,
	"backup": `用法: opskit backup verify|prune [参数]
verify --directory 指向单个备份任务，检查 manifest、大小和 SHA-256。
prune --directory 指向备份根目录，读取 backup.retention；默认只预览。
--dry-run=false 才清理；保留最新 keep 份，且仅选择超出 older-than 的完整任务。
上传失败、损坏、含额外文件或链接的任务受保护；不删除远端对象。`,
	"bundle": `用法: opskit bundle export|import|verify [参数]
export --directory 指向 k8s download 完成目录，--file 指定 tar.gz；可附带 --image-archive 和 --package-file。
import --file 指向归档，--directory 必须尚不存在；校验摘要后发布目录。
verify 检查已导入目录中的文件是否缺失或损坏。
不自动解析或安装系统包与镜像；离线使用须自行准备目标环境所需依赖。`,
}
