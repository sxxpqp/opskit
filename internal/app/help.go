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
	"go": `用法: opskit go install [参数]
安装完整 Go SDK，校验 SHA-256、实际版本并创建 go/gofmt 命令入口。
支持 Linux/macOS amd64、arm64；版本读取 go.version，--version 可覆盖。
安装后根据 mirrors.go 设置当前用户的持久 GOPROXY；默认执行，--dry-run 仅预览。
示例: opskit go install --version 1.26.2
使用 opskit go install --help 查看参数。`,
	"k8s": `用法: opskit k8s create [参数]
集群名填写在配置的 k8s.name（例如 sxxpqp），不接受 create sxxpqp 这样的参数。
首次执行 opskit config init，编辑节点、角色和版本后创建。
network.manifest 留空自动安装固定版本 Calico；填写本地清单可覆盖。
--calico-version 覆盖 k8s.network.calico.version；当前支持 v3.26.5 / Kubernetes 1.24～1.28。
原生 SSH 集群编排，不依赖 KK；使用 k8s.hosts / roleGroups 和组件 version。
支持单 master（同节点 etcd）、可选 worker、containerd 或 Docker 二进制运行时。
默认执行，--dry-run 只校验配置和展示计划；KubeSphere 默认关闭。
示例: opskit k8s create --config config/config.yaml --dry-run`,
	"kubeadm": `用法: opskit kubeadm install|prepare|init|join [参数]
install 安装固定版本 kubeadm/kubelet/kubectl/crictl 和 kubelet systemd unit，不安装 containerd。
prepare 配置内核模块、转发和 bridge sysctl、禁用 swap（备份 fstab）。
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
	"s3": `用法: opskit s3 <命令> [参数]
命令: upload（上传文件） | check（连通性检查）
示例: opskit s3 upload --help`,
}
