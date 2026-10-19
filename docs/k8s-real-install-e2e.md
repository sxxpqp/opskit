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
