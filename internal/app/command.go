package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// Keep existing task exit codes while Cobra owns dispatch, parsing and help.
type commandExitError struct {
	code int
	err  error
}

func (e *commandExitError) Error() string { return e.err.Error() }
func (e *commandExitError) Unwrap() error { return e.err }
func bindCommand(cmd *cobra.Command, run func() (int, error)) {
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		code, err := run()
		if err == nil {
			return nil
		}
		if code == 0 {
			code = 1
		}
		return &commandExitError{code: code, err: err}
	}
}

func Run(ctx context.Context, args []string, out, diag io.Writer, version string) (code int, err error) {
	report := &reportOutput{writer: out}
	root, err := newRootCommand(ctx, report, diagnosticOutput{report, diag}, version)
	if err != nil {
		return 1, err
	}
	report.root = root
	command := "opskit"
	defer func() {
		if writeErr := report.finish(command, code, err); writeErr != nil {
			code, err = 1, writeErr
		}
	}()
	root.SetArgs(args)
	root.SetContext(ctx)
	executed, runErr := root.ExecuteC()
	if executed != nil {
		command = executed.CommandPath()
	}
	if err = runErr; err != nil {
		var exit *commandExitError
		if errors.As(err, &exit) {
			return exit.code, exit.err
		}
		return 2, err
	}
	return 0, nil
}

func newRootCommand(ctx context.Context, out, diag io.Writer, version string) (*cobra.Command, error) {
	root := &cobra.Command{
		Use:     "opskit",
		Short:   "运维工具，随取随用",
		Long:    "运维工具，随取随用。安装工具、部署集群、检查健康并备份数据。\n\n默认配置: ./config/config.yaml，不存在时回退 ./opskit.yaml。\n使用 <命令组> <命令> --config FILE 指定配置。\n安装版本由 --version 或对应配置指定；支持 --dry-run 的命令可仅预览。",
		Example: "  opskit config init\n  opskit go install --version 1.26.2\n  opskit k8s create --config config/config.yaml --dry-run", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(out)
	root.SetErr(diag)
	root.SetHelpTemplate(commandHelpTemplate)
	root.SetUsageTemplate(commandHelpTemplate)
	output := root.PersistentFlags().String("output", "text", "统一结果输出：text / json（JSON 日志最多 1 MiB，诊断仍写 stderr）")
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if *output != "text" && *output != "json" {
			return errors.New("output 必须是 text 或 json")
		}
		return nil
	}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("未知命令: %s；使用 opskit --help 查看命令", args[0])
		}
		return cmd.Help()
	}
	root.AddCommand(&cobra.Command{Use: "version", Short: "查看 opskit 版本", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, _ []string) { fmt.Fprintln(cmd.OutOrStdout(), version) }})
	doctor, err := newDoctorCommand(ctx, out)
	if err != nil {
		return nil, err
	}
	root.AddCommand(doctor)
	groups := map[string]*cobra.Command{}
	for _, item := range []struct{ name, short string }{
		{"openebs", "通过 Operator 安装 OpenEBS"},
		{"backup", "校验本地备份及按保留策略清理"},
		{"bundle", "组件、镜像及系统包离线归档"},
		{"middleware", "部署与检查高可用中间件"},
		{"kubeblocks", "安装 Operator 并部署 Kubernetes 高可用中间件"},
		{"config", "生成配置模板"}, {"go", "安装 Go 并设置 GOPROXY"}, {"k8s", "下载组件、检查或部署 Kubernetes"}, {"calico", "独立安装 Calico 网络插件"}, {"kubeadm", "安装工具、准备节点、初始化或加入集群"}, {"kubectl", "安装 Kubernetes 客户端"}, {"docker", "安装 Docker"}, {"cri-dockerd", "安装 cri-dockerd"}, {"containerd", "安装 containerd、runc 和 CNI plugins"}, {"kind", "安装 kind 或管理测试集群"}, {"mirror", "设置镜像源或代理"}, {"kubesphere", "安装 KubeSphere"}, {"etcd", "备份或检查 etcd"}, {"mysql", "MySQL 逻辑备份与可选 S3 上传"}, {"s3", "上传文件或检查 S3"},
	} {
		long := commandGroupHelp[item.name]
		_, long, _ = strings.Cut(long, "\n")
		group := &cobra.Command{Use: item.name, Short: item.short, Long: long}
		group.RunE = func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("未知 %s 命令: %s；使用 %s --help 查看子命令", cmd.Name(), args[0], cmd.CommandPath())
			}
			return cmd.Help()
		}
		// Preserve the existing '<group> help' spelling using a native help command.
		group.AddCommand(&cobra.Command{Use: "help", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Parent().Help() }})
		groups[item.name] = group
		root.AddCommand(group)
	}
	add := func(group, short string, cmd *cobra.Command, err error) error {
		if err != nil {
			return err
		}
		cmd.Short = short
		groups[group].AddCommand(cmd)
		return nil
	}
	type registration struct {
		group, short string
		build        func() (*cobra.Command, error)
	}
	for _, item := range []registration{
		{"kubeblocks", "首次安装固定版 Operator 与 MySQL/Redis/MinIO 定义", func() (*cobra.Command, error) { return newKubeBlocksCommand(ctx, "install", out, diag) }},
		{"kubeblocks", "输出受保护的持久化 Cluster 清单", func() (*cobra.Command, error) { return newKubeBlocksCommand(ctx, "render", out, diag) }},
		{"kubeblocks", "创建 Cluster 并等待 Pod/PVC 就绪", func() (*cobra.Command, error) { return newKubeBlocksCommand(ctx, "deploy", out, diag) }},
		{"kubeblocks", "只读检查 Cluster、Pod 与 PVC", func() (*cobra.Command, error) { return newKubeBlocksCommand(ctx, "status", out, diag) }},
		{"config", "生成配置模板，保留已有文件", func() (*cobra.Command, error) { return newConfigInitCommand(out, diag) }},
		{"mysql", "导出指定数据库、校验并可选上传 S3", func() (*cobra.Command, error) { return newMySQLBackupCommand(ctx, out, diag) }},
		{"s3", "列举桶内对象，输出 JSON", func() (*cobra.Command, error) { return newS3ReadCommand(ctx, "list", out) }},
		{"s3", "下载并校验对象内容，拒绝覆盖", func() (*cobra.Command, error) { return newS3ReadCommand(ctx, "download", out) }},
		{"s3", "完整回读对象并校验 SHA-256", func() (*cobra.Command, error) { return newS3ReadCommand(ctx, "verify", out) }},
		{"backup", "校验本地任务清单、文件大小与 SHA-256", func() (*cobra.Command, error) { return newBackupManageCommand(ctx, "verify", out) }},
		{"backup", "预览或执行本地保留策略清理", func() (*cobra.Command, error) { return newBackupManageCommand(ctx, "prune", out) }},
		{"bundle", "导出已有组件目录，可附带镜像与系统包", func() (*cobra.Command, error) { return newBundleCommand(ctx, "export", out) }},
		{"bundle", "校验并导入离线归档", func() (*cobra.Command, error) { return newBundleCommand(ctx, "import", out) }},
		{"bundle", "校验已导入的离线文件", func() (*cobra.Command, error) { return newBundleCommand(ctx, "verify", out) }},
		{"middleware", "生成 Docker 验证部署资料，不启动容器", func() (*cobra.Command, error) { return newMiddlewareCommand(ctx, "render", out, diag) }},
		{"middleware", "部署所选中间件并初始化集群", func() (*cobra.Command, error) { return newMiddlewareCommand(ctx, "deploy", out, diag) }},
		{"middleware", "检查所选中间件拓扑和健康", func() (*cobra.Command, error) { return newMiddlewareCommand(ctx, "status", out, diag) }},
		{"middleware", "检查全部成员与 GTID 后恢复 MySQL 完整停机", func() (*cobra.Command, error) { return newMiddlewareCommand(ctx, "recover", out, diag) }},
		{"go", "安装指定版本 Go SDK 并配置代理", func() (*cobra.Command, error) { return newGoInstallCommand(ctx, out, diag) }},
		{"k8s", "按配置部署集群并自动加入节点", func() (*cobra.Command, error) { return newK8sCreateCommand(ctx, out, diag) }},
		{"k8s", "只下载所选 Linux 组件，不安装或连接 SSH", func() (*cobra.Command, error) { return newK8sDownloadCommand(ctx, out) }},
		{"k8s", "通过 SSH 只读检查部署目标", func() (*cobra.Command, error) { return newK8sCheckCommand(ctx, out) }},
		{"k8s", "只读查看节点 Ready 与系统 Pod", func() (*cobra.Command, error) { return newK8sStatusCommand(ctx, out) }},
		{"calico", "在 kubeadm 集群独立安装 Calico", func() (*cobra.Command, error) { return newCalicoInstallCommand(ctx, out, diag) }},
		{"openebs", "通过固定 Operator 清单安装 NDM 和 Local PV", func() (*cobra.Command, error) { return newOpenEBSInstallCommand(ctx, out, diag) }},
		{"kubectl", "安装指定版本 kubectl", func() (*cobra.Command, error) { return newKubectlInstallCommand(ctx, out, diag) }},
		{"kind", "安装指定版本 kind", func() (*cobra.Command, error) { return newKindInstallCommand(ctx, out, diag) }},
		{"kubesphere", "安装 KubeSphere 并等待服务就绪", func() (*cobra.Command, error) { return newKubeSphereCommand(ctx, out, diag) }},
	} {
		cmd, err := item.build()
		if err = add(item.group, item.short, cmd, err); err != nil {
			return nil, err
		}
	}
	node, err := newK8sNodeCommand(ctx, out, diag)
	if err != nil {
		return nil, err
	}
	node.Hidden = true
	groups["k8s"].AddCommand(node)
	for _, tool := range []string{"docker", "cri-dockerd", "containerd"} {
		cmd, err := newRuntimeInstallCommand(ctx, tool, out, diag)
		if err = add(tool, "安装指定版本 "+tool, cmd, err); err != nil {
			return nil, err
		}
	}
	for _, action := range []string{"install", "prepare", "init", "join"} {
		descriptions := map[string]string{"install": "安装 kubeadm、kubelet、kubectl 和 crictl", "prepare": "配置内核与转发并禁用 swap", "init": "初始化单控制平面", "join": "将工作节点加入集群"}
		cmd, err := newKubeadmCommand(ctx, action, out, diag)
		if err = add("kubeadm", descriptions[action], cmd, err); err != nil {
			return nil, err
		}
	}
	for _, action := range []string{"create", "status", "logs", "delete", "use", "unuse"} {
		descriptions := map[string]string{"create": "创建测试集群", "status": "查看集群状态", "logs": "导出诊断日志", "delete": "删除配置指定的集群", "use": "切换到 kind 集群", "unuse": "恢复原 kubeconfig"}
		cmd, err := newKindCommand(ctx, action, out, diag)
		if err = add("kind", descriptions[action], cmd, err); err != nil {
			return nil, err
		}
	}
	for _, tool := range []string{"docker", "containerd", "go", "npm", "pip", "maven"} {
		cmd, err := newMirrorCommand(ctx, tool, out, diag)
		if err = add("mirror", "设置 "+tool+" 镜像源或代理", cmd, err); err != nil {
			return nil, err
		}
	}
	for _, item := range []struct{ group, action, short string }{{"etcd", "backup", "创建 etcd 快照"}, {"etcd", "check", "检查 etcd 集群健康"}, {"s3", "upload", "上传文件到 S3"}, {"s3", "check", "检查 S3 连通性"}} {
		cmd, err := newDataCommand(ctx, item.group+" "+item.action, out, diag)
		if item.action == "upload" && cmd != nil {
			cmd.Use = "upload [参数] FILE"
		}
		if err = add(item.group, item.short, cmd, err); err != nil {
			return nil, err
		}
	}
	// Cobra generates completion from the same registered commands and flags.
	root.CompletionOptions.DisableDefaultCmd = true
	completion := &cobra.Command{Use: "completion", Short: "生成 Shell 补全脚本"}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		completion.AddCommand(&cobra.Command{Use: shell, Short: "生成 " + shell + " 补全脚本", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			switch cmd.Name() {
			case "bash":
				return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			default:
				return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			}
		}})
	}
	completion.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("不支持的 Shell: %s", args[0])
		}
		return cmd.Help()
	}
	root.AddCommand(completion)
	root.InitDefaultHelpCmd()
	localizeHelpFlags(root)
	return root, nil
}

func localizeHelpFlags(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	cmd.Flags().Lookup("help").Usage = "查看帮助"
	for _, child := range cmd.Commands() {
		localizeHelpFlags(child)
	}
}
