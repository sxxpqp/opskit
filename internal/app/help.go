package app

func helpArgument(s string) bool { return s == "-h" || s == "--help" || s == "help" }

var commandGroupHelp = map[string]string{
	"kind": `用法: opskit kind <命令> [参数]

命令:
  install  安装指定版本 kind，并设置命令入口
  create   创建 kind 集群（默认执行，--dry-run 预览）
  status   查看集群状态
  logs     查看集群日志
  use      切换到 kind 集群
  unuse    恢复之前的 kubeconfig
  delete   删除集群（需要 --apply 和 --confirm-name）

示例:
  opskit kind install --version v0.17.0
  opskit kind create --version v1.24.7
  opskit kind status

使用 opskit kind <命令> --help 查看具体参数。`,
	"mirror": `用法: opskit mirror <类型> [参数]

类型: docker | containerd | go | npm | pip | maven
默认预览，--apply 备份后写入配置。
示例: opskit mirror containerd --help`,
	"docker": `用法: opskit docker install [参数]
安装 Docker；默认使用官方脚本和 Aliyun 软件包源。
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
