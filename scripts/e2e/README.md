# Vagrant 验证环境

这里的脚本只用于专用测试虚拟机。验收结果见 [2026-10-28 记录](../../docs/validation-2026-10-28.md)。

高可用中间件的需求与进度见[需求清单](../../docs/middleware-requirements.md)，Docker 和 KubeBlocks 实际结果见[2026-10-28～29 中间件记录](../../docs/middleware-validation-2026-10-28.md)。

## 两节点环境

Windows 宿主机已有 Vagrant、VMware provider 与对应 utility 时：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/e2e/vagrant.ps1 up --provider vmware_desktop
powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/e2e/vagrant.ps1 ssh master1 -c 'hostname -I'
powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/e2e/vagrant.ps1 ssh worker1 -c 'hostname -I'
```

包装脚本优先使用已有 `VAGRANT_HOME`；未设置时使用仓库忽略目录 `.opskit/vagrant/home`。`OPSKIT_VAGRANT_BOX`／`OPSKIT_VAGRANT_BOX_VERSION` 可选择本机缓存的 box，默认复用本项目已验证的 Ubuntu box。机器状态和私钥位于本目录的 `.vagrant`，已忽略；不修改系统执行策略，只对该 PowerShell 进程允许执行脚本。

两个节点名为 `master1`／`worker1`，SSH 转发端口为 2211／2212；端口被占用时报错，不自动换端口。Vagrantfile 为 VMware 设置 3 GiB／4 GiB 内存与各 2 vCPU（worker 为中间件测试扩容；此前 Kubernetes 基础验收使用 2 GiB）。其他 provider 未在本轮验收。

按模板在忽略目录生成测试 YAML，配置两台实际节点 IP、角色与私钥绝对路径。Windows 管理机还需从同一源码交叉编译 Linux opskit，并在 `k8s.deploy.nodeBinaries.amd64` 指定该文件。镜像、下载源和系统软件源需在测试环境可达。

```sh
opskit k8s create --config /path/to/test.yaml --dry-run
opskit k8s create --config /path/to/test.yaml
```

上传 `multinode.sh` 到测试 master，然后执行：

```sh
sudo env KUBECONFIG=/etc/kubernetes/admin.conf bash ./multinode.sh
```

脚本只接受节点恰好为 master1 和 worker1 的拓扑；创建唯一测试 namespace，在两节点分别放置服务端和客户端，测试 DNS、Service、双向 Pod IP 和策略拒绝／放行，结束清理 namespace。默认使用 `OPSKIT_E2E_IMAGE` 或已验证的 busybox:1.36，目标节点需已有该镜像或能拉取。

## 独立 etcd 恢复

将匹配版本的官方 etcd、etcdctl、etcdutl 二进制放入专用目录。可通过项目 `k8s download --components etcd --etcd-version v3.5.5` 准备，下载源沿用模板和测试 YAML，不在脚本中定义另一套源。

```sh
OPSKIT=/path/to/opskit ETCD_BIN_DIR=/path/to/etcd-binaries bash ./etcd-recovery.sh
```

脚本使用 12379/12380 和 22379/22380 回环端口、独立临时数据目录，仅写两条合成数据。端口需空闲；不修改 kubeadm 的 etcd 服务或数据目录。日志和快照保留在输出的临时目录，进程结束时停止本次启动的 etcd。

## S3 与远端快照恢复

使用明确授权的测试桶配置，脚本读取 `s3.minio`。需要列举、HEAD、GET、PUT、删除本次对象版本和清理本次多段上传的权限；所有写入使用随机 `opskit-validation/<时间>-<随机值>/` 前缀，结束核对清理结果，不覆盖已有对象。配置文件请放在仓库忽略目录，权限设为仅当前用户可读。

```sh
OPSKIT=/path/to/opskit go run ./scripts/e2e/s3 /path/to/private-test-config.yaml
# 额外验证上一步生成的合成数据快照经 S3 下载后仍能恢复
OPSKIT=/path/to/opskit ETCD_BIN_DIR=/path/to/etcd-binaries \
  OPSKIT_E2E_SNAPSHOT=/path/to/synthetic-task/snapshot.db \
  go run ./scripts/e2e/s3 /path/to/private-test-config.yaml
```

快照选项仅用于前述脚本新建的合成数据实例；恢复实例使用 32379/32380。JSON 结果记录成功、阶段、摘要与远端清理状态，不输出访问密钥。不要把生产快照当作测试样本上传。验证完成自行移除私密配置文件。

## Docker 中间件故障验证

仅使用新建、带日期的专用项目，例如 YAML 的 `middleware.name: opskit-ha-20261028`。先通过 `middleware deploy` 初始化，再运行以下脚本；它会核对目录归属、停止成员并恢复，禁止用于生产项目。

```sh
go run ./scripts/e2e/middleware --engine redis \
  --project opskit-ha-20261028-redis --directory /path/to/private-redis-dir
go run ./scripts/e2e/middleware --engine minio \
  --project opskit-ha-20261028-minio --directory /path/to/private-minio-dir \
  --endpoint http://127.0.0.1:19090
go run ./scripts/e2e/middleware --engine mysql \
  --project opskit-ha-20261028-mysql --directory /path/to/private-mysql-dir \
  --opskit /path/to/opskit --config /path/to/private-middleware-config.yaml
```

Windows Docker Desktop 默认 `--docker-context desktop-linux`，其他环境可显式传空字符串或实际 context。MinIO 测试只接受回环 HTTP 地址，使用随机测试桶；MySQL 测试数据库为本专用项目中的 `opskit_ha_validation`，测试开始会重建该库。全量停机通过先 stop 所有成员再 start 实现；MySQL 额外执行显式 recover。测试正常结束恢复成员，但异常强制终止时仍应检查专用容器状态。

## KubeBlocks 实验

仅使用上述专用 Vagrant 集群。`kubeblocks-fixture.ps1` 在空目录生成 namespace、静态 PV、业务 YAML、实验资源补丁和 MinIO 私网入口清单，不连接或修改集群：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/e2e/kubeblocks-fixture.ps1 -Directory dist/.kubeblocks-fixtures -Date 20261028
go build -o dist/kubeblocks-e2e.exe ./scripts/e2e/kubeblocks
```

静态 PV 指向测试 worker 的 `/var/lib/opskit-e2e/kubeblocks-<日期>/pv-1`～`pv-16`，运行前需在专用 VM 准备可供实例用户写入的空目录。应用清单前确认 kubeconfig、节点与日期名称正确，不能用于生产集群。小容量实验可在 Operator 安装等待期间，使用生成的 manager/dataprotection strategic patch 降低测试资源请求；实际参数与调度调整见[验收记录](../../docs/middleware-validation-2026-10-28.md)。默认生产反亲和不会在这两个节点通过，实验必须显式 allow_colocation。

在三类业务 Cluster 为 Running、全部 Pod Ready、PVC Bound 后，分别运行：

```sh
dist/kubeblocks-e2e.exe --engine mysql --namespace opskit-kb-e2e-20261028 --cluster opskit-kb-20261028-mysql --kubeconfig /path/to/test-kubeconfig
dist/kubeblocks-e2e.exe --engine redis --namespace opskit-kb-e2e-20261028 --cluster opskit-kb-20261028-redis --kubeconfig /path/to/test-kubeconfig
dist/kubeblocks-e2e.exe --engine minio --namespace opskit-kb-e2e-20261028 --cluster opskit-kb-20261028-minio --kubeconfig /path/to/test-kubeconfig --endpoint http://<测试worker私网IP>:31090
```

MinIO 需要已应用测试 NodePort 清单；端点只接受私有或回环 IP。脚本仅接受上述日期格式的专用名称和 opskit 管理的 Running Cluster，写入随机测试数据，执行普通 Pod 删除并等待重建，核对读写和 PVC UID／绑定卷。MySQL 使用专用 `opskit_kb_validation` 库；Redis 在同一连接 SET/WAIT 确认复制后替换键所在主 Pod；MinIO 使用随机测试桶。凭据从该测试组件 Secret 读取到内存，不放进命令参数或输出。测试数据保留用于复查，不运行 PVC、PV 或集群删除。

该程序验证 Pod 替换与数据保留，不能替代主机断电、磁盘损坏和容量验收。

## 回归与关机

```sh
go vet ./...
go test -p 1 -race ./...
```

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/e2e/vagrant.ps1 halt
```

关机保留测试磁盘，可再次启动。生产恢复、多种发行版与版本组合需另外建立验证环境。
