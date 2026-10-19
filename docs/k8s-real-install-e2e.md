# Experimental: real OpsKit kubeadm installation in Docker

## Purpose

This **actually runs `opskit k8s create` without `--dry-run`** against a
fresh Ubuntu 22.04 privileged Docker container running systemd and sshd. The
control container uses OpsKit's real SSH, artifact downloads, containerd/runc,
CNI plugin installation, kubeadm prepare/init, Calico application and node
readiness checks.

Unlike Kind, the target image intentionally **does not contain Kubernetes,
containerd, kubelet or kubeadm at build time**. All Kubernetes binaries must be
downloaded and installed by OpsKit. The separate test in
[`k8s-docker-e2e.md`](k8s-docker-e2e.md) remains fast and checks SSH/systemd
plus Kind; neither test is silently substituted for this job.

This experiment starts with **one control-plane node only**. A real Worker
join and cross-node networking require an additional independent acceptance
stage after the one-node installation succeeds.

## Run it

Only on an isolated, disposable Linux Docker host (the GitHub Actions job
enforces a GitHub-hosted ephemeral runner):

```bash
OPSKIT_ALLOW_PRIVILEGED_INSTALL_E2E=1 bash tests/e2e/docker/run-install.sh
```

**Never run this on a production Docker host or your daily development
machine.** Privileged containers can affect their host's kernel/modules and
network configuration; kubeadm host preparation loads modules and modifies
swap/sysctl settings. No Docker socket or host SSH port is mounted or exposed.

The script:
1. Starts an empty Ubuntu 22.04 systemd/SSH node with short-lived credentials.
2. Builds OpsKit from the current commit in a separate controller container.
3. Generates a private YAML config referencing that node's assigned Docker IP.
4. Disables the repository example's private Nexus and insecure image proxies,
   substituting public upstream download endpoints and TLS-verified registries.
5. Runs a harmless `--dry-run`, then runs **the actual**
   `opskit k8s create --config ... --timeout 32m` command.
6. Requires the installer to exit successfully and produce a kubeconfig.
7. Downloads the matching kubectl binary with SHA-256 verification, checks the
   API readiness endpoint, master Node Ready, Calico and CoreDNS rollout.
8. Records logs, gathers node journal diagnostics and destroys this run's
   containers, private volume and network even when installation fails.

Initial GitHub Actions run reached real artifact downloads and **kubeadm
prepare**, then failed because `modprobe overlay` could not find the host
kernel's `/lib/modules` metadata inside Ubuntu 22.04. The follow-up test
mounts the disposable runner's module metadata read-only and treats already
loaded kernel modules as available, while still failing if a missing module
cannot be loaded. Its first run also exposed root-only report file permissions;
the report uploader now receives read access only to diagnostic files. In the second real
run, kubeadm preparation reached and applied bridge/IP-forwarding sysctls,
but `swapoff -a` returned 32 in the systemd container. The host preparation
now reads `/proc/swaps` first: it skips a needless swapoff only when there
are no active swap devices, and still fails if active swap cannot be disabled.
This behavior is covered by an isolated unit test.

On a GitHub-hosted disposable runner, CI explicitly checks and disables
host-level swap before creating Docker nodes. This is required because
`/proc/swaps` reports **host kernel swap**, which privileged containers cannot
reliably deactivate. The local script instead refuses to run while host swap
is active; it never calls host `swapoff` automatically. This is not suitable
for shared Docker hosts.

With host swap disabled, the next run installed real containerd, runc, CNI
plugins, kubeadm, kubelet and kubectl and reached **kubeadm init**. It failed
the mandatory `SystemVerification` preflight because the Ubuntu container
could not read the host's kernel configuration (`modprobe configs` unavailable).
The dedicated test now mounts host `/boot` and `/usr/src` read-only alongside
`/lib/modules`, allowing kubeadm to use actual kernel configuration if the
GitHub-hosted runner provides it. **No preflight errors are ignored.**
The kernel config may still be absent on a particular hosted image; such a
failure is recorded rather than passed.

### CI install failure: nested containerd rootfs mount

The most recent completed installation run (2026-10-19, Actions run
`39212219930`) progressed through actual binary/runtime installation and
`kubeadm init` certificate/static-pod manifest generation. The kubelet service
remained running, but containerd repeatedly failed to create control-plane Pod
sandboxes with `failed to mount rootfs component: invalid argument`. As a
result, no API server started and kubeadm timed out. The `pause:3.8` versus
`pause:3.9` warning is recorded but not proven to cause this failure.

**Hypothesis under test:** the nested containerd overlay snapshotter tries to
mount root filesystems atop the outer Docker overlay filesystem. The test
harness now mounts a fresh, run-scoped Docker volume at `/var/lib/containerd`
and checks that its filesystem is not `overlay` before running the installer.
This is strictly a test-infrastructure adjustment: it does not bypass kubeadm
checks or change OpsKit's production containerd configuration. The new run
must actually reach Ready for a PASS; if the error persists, inspect the
uploaded `node-diagnostics.log` for CRI, snapshotter and kernel details.

### 2026-10-19: actual control plane installed; Calico startup stalled

Actions run [39213308900](https://github.com/sxxpqp/opskit/actions/runs/39213308900)
**successfully executed kubeadm init** after the dedicated containerd storage
volume fix. The static etcd, API server, controller-manager, scheduler, and
kube-proxy containers were running. The manifest applied Calico resources, but
OpsKit's **master/ready** phase hit the total deadline. Collected kubelet and
containerd logs show that CoreDNS and calico-kube-controllers cannot get
network sandboxes because `/var/lib/calico/nodename` is absent. The
`calico-node` main container does **not** appear in `crictl ps -a`, while
its `install-cni` and `upgrade-ipam` init containers have exited.

This evidence establishes that Calico was not initialized, **not why** its
init-to-main transition stalled. The pipeline now collects Calico container
exit codes and logs, Pod descriptions, DaemonSet status, and kube-system
events before removing test containers. Do not fake `/var/lib/calico/nodename`
or bypass the network setup check: those approaches would produce a false
installation success. A bounded 15-minute test timeout (overridable with
`OPSKIT_INSTALL_E2E_TIMEOUT`) makes stalled CNI failures easier to diagnose.
A PASS still requires real node/Calico/CoreDNS readiness.

The Ubuntu GitHub runner has finite CPU, RAM and disk. Public registry
downloads can fail due to rate limits or network outages; inspect stage logs
before classifying failures. Docker uses the host kernel and may not support
all kubelet/containerd cgroup, overlayfs and iptables operations. A **failure
is not skipped or marked successful**; a container-specific limitation should
be reported as such. A pass is strong container-level installation evidence
but is not identical to fresh physical host or VM certification.

The experiment pins Kubernetes **v1.28.15** with containerd **v1.7.24**,
runc **v1.2.6**, crictl **v1.28.0**, CNI plugins **v1.5.1**, and
Calico **v3.26.5**, a historically supported combination in this repo.
These versions are old and chosen for regression testing, **not recommended
as current production deployments**. A version compatibility upgrade needs
separate product changes and testing.

## Results and evidence

Logs are written to `tests/e2e/docker/artifacts/full-install/` and stored as
short-lived GitHub Actions artifacts. `install.log` captures OpsKit output,
`verify.log` contains post-install checks, `node-diagnostics.log` collects
a bounded kubelet/containerd journal tail, and `result.txt` contains the
final status. Generated kubeconfig, SSH key and install bundles are never
included in the uploaded artifact paths.

This does not fix OpsKit's existing insecure SSH host-key handling. Test
credentials are ephemeral and container-only. For real deployments,
host-key verification should be enforced before using persistent secrets.

**Release acceptance:** do not claim bare-metal or VM installation passes on
the strength of this Docker experiment alone. Add a dedicated VM/cloud E2E
with kubeadm master/worker joining and cross-node network checks as a separate
release gate.
