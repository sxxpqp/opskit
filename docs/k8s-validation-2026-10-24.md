# Kubernetes 验收记录（2026-10-24）

结论：自动创建与手动 kubeadm + 独立 Calico 两条单节点流程均通过真实安装及网络测试。本次使用专用 Vagrant + VMware Ubuntu 虚拟机，首轮验证代码为 main 提交 `7c840e9579f8c69fd35871620ffe55a98aa2f16d`；后续依赖自动安装和下载缓存验证使用其上的未提交工作区修改，不表示新增正式发布版本。

## 环境

| 项目 | 实际值 |
|---|---|
| 宿主机 / 虚拟化 | Windows / VMware Workstation 17.6.3、Vagrant 2.4.9 |
| 测试节点 | Ubuntu 24.04.3 LTS，Linux amd64，内核 6.8.0-86，2 vCPU、4 GiB 内存 |
| 拓扑 | 单控制平面，同节点承载测试工作负载，无独立 worker |
| Kubernetes | kubeadm / kubelet / kubectl v1.24.7 |
| 运行时 | containerd v1.7.28、runc v1.3.3 |
| 配套组件 | crictl v1.24.2、CNI plugins v1.9.1、Calico v3.26.5 |
| 网络 | Pod CIDR `10.244.0.0/16`，Service CIDR `10.96.0.0/12` |
| etcd | kubeadm 管理的镜像 3.5.3-0；单独下载的客户端及服务端二进制为 3.5.5 |

这些版本用于验证项目现有配套行为，不构成新生产集群的版本选型建议。

## 已通过项目

| 检查 | 实际结果 |
|---|---|
| Kubernetes / containerd 相关 Go 回归测试 | `go test -p 1 ./internal/app -run 'Test(K8s\|Cluster\|Calico\|Kubeadm\|Containerd)' -count=1` 通过 |
| `k8s create --dry-run` | 输出单节点部署计划 |
| 空机 `k8s check` | 正确报告缺少 conntrack、socat，返回非零；检查前后 `/etc/fstab` 摘要不变 |
| `k8s download` | 无 SSH hosts 配置下载 9 个组件；独立核对 11 条文件记录的 SHA-256，包含 kubelet systemd 文件 |
| 下载与安装分离 | 下载前后均未安装 kubeadm、kubelet、kubectl、containerd |
| 下载 etcd 二进制 | etcd、etcdctl、etcdutl 的版本输出均为 3.5.5 |
| 自动 `k8s create` | 安装系统依赖、准备主机、安装运行时和 Kubernetes；续跑后节点 Ready、全部系统 Pod Running |
| 受管集群 `k8s check` / `k8s status` | 返回成功，节点 Ready |
| 重复 `k8s create` | 返回成功，节点 UID 和集群 CA 摘要未改变 |
| 实际 etcd 健康检查 | `opskit etcd check` 经 TLS 连接实际单成员 etcd，健康且满足 quorum |
| DNS / Service / Pod IP | 测试客户端解析 Kubernetes 和测试 Service 域名，通过 Service 和 Pod IP 读取预期 HTTP 响应 |
| NetworkPolicy | 拒绝 ingress 后访问失败，增加允许客户端规则后恢复访问 |
| 已有 CNI 保护 | 重复独立安装 Calico 被拒绝 |
| 分步安装入口 | 从安装前快照执行 `kubeadm prepare`、`containerd install`、`mirror containerd`、`kubeadm install`，随后原生 `kubeadm init` 成功 |
| 独立 `calico install` | 消费下载任务内的本地清单，节点由 NotReady 变为 Ready，系统 Pod 全部 Running；清单原文件摘要未改变 |
| 手动集群归属保护 | `k8s check` 和 `k8s create` 均拒绝 unmanaged cluster，未接管或重建手动集群 |
| CIDR 保护 | 使用错误 Pod CIDR 时返回非零，未安装 Calico；正确 CIDR 随后安装成功 |
| 手动集群网络 | 同一套 DNS、Service、Pod IP 和 NetworkPolicy 拒绝 / 放行测试通过 |

网络测试使用一个客户端 Pod 和两个 HTTP 服务副本，完成后删除测试 namespace。这里验证的是同节点 Pod 网络及策略，不包含跨节点网络。

## 下载条件与失败记录

原配置下载源在本次环境吞吐较低，首次组件下载达到测试脚本设置的 12 分钟上限，未形成完成任务目录。随后从同一上游预取原始文件和校验文件，在虚拟机回环地址提供临时 HTTP 缓存，通过测试 YAML 的统一下载配置运行实际 `k8s download` 和安装入口；没有跳过程序的上游摘要校验。该结果证明下载配置和产物完整性，不等于默认外网源在任意网络条件下均可及时完成。

自动创建首次在 25 分钟总超时处退出，期间控制平面已经初始化，Calico 镜像仍在下载。镜像通过原配置的 containerd 代理最终下载完成，未替换代理配置。保留原失败日志后，对同一受管集群重新执行 `create` 成功，并完成网络测试。

独立流程从安装前虚拟机快照开始，复用自动流程导出的容器镜像；归档在宿主机与恢复后的虚拟机之间传输，SHA-256 一致。辅助脚本首次导入包含重复摘要别名的归档时，containerd 报 `already exists`，改为按 registry 名称筛选导入后继续；这不是项目安装入口的错误。

## 自动依赖与下载缓存补测

恢复同一安装前快照后，使用修改后的程序重新验证。事先确认 conntrack、socat 均不存在，未通过测试脚本手动安装这两个软件包。

| 检查 | 结果 |
|---|---|
| `kubeadm prepare` 自动前置依赖 | 自动使用 Ubuntu 软件源安装缺失的 conntrack、socat，完成内核、sysctl 与 swap 准备 |
| 重复 prepare | 成功，不再次执行系统软件包安装 |
| 首次下载 9 个组件 | 17 次本地 HTTP 文件请求，生成 11 条文件记录 |
| 相同根目录再次下载 | HTTP 请求增量为 0；两个完成任务中的 11 条记录均独立通过 SHA-256 检查 |
| 后续 `containerd install` + `kubeadm install` | 复用同一下载目录，HTTP 请求增量为 0，安装与版本校验成功 |
| 后续手动集群流程 | 原生 kubeadm 初始化、独立 Calico、DNS、Service、Pod IP 和 NetworkPolicy 拒绝 / 放行测试通过 |
| Ubuntu 自动化 | `go test -p 1 -race ./...` 与 `go vet ./...` 通过 |
| Windows 缓存回归 | 通过跨入口复用、损坏缓存修复、不同 URL 隔离、取消、中断响应不入缓存及重复组件下载测试 |

本轮仍使用回环 HTTP 服务提供已预取的原始组件与校验文件，以服务访问日志计数确认程序是否重复获取文件，不是测量上游代理自身的命中率。安装时继续执行原有上游摘要和二进制版本校验。测试脚本和脱敏日志位于 `.opskit/vagrant/k8s-cache-deps-test.sh`、`.opskit/vagrant/k8s-cache-deps-logs.json`。容器镜像复用之前导出的镜像归档。

## 记录与边界

本机脱敏日志位于 `.opskit/vagrant/k8s-automatic-logs.json` 和 `.opskit/vagrant/k8s-manual-logs.json`；该目录被 Git 忽略，不包含在仓库发布内容中。原始 kubeconfig、SSH 私钥和 bootstrap token 不纳入本文档。测试结束保留可用的手动安装集群与安装前快照，临时工作负载 namespace 已删除。

尚未验证多节点 join、跨节点通信、HA、升级、集群删除、故障恢复、其他架构、Docker / cri-dockerd 运行时，以及其他 Kubernetes / Calico 版本组合。etcd 健康检查不等于快照恢复验收。
