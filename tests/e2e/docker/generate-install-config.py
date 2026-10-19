#!/usr/bin/env python3
"""Generate isolated, non-production OpsKit config for one real kubeadm node."""
import os
import socket
import sys
import yaml

if len(sys.argv) != 3:
    sys.exit("usage: generate-install-config.py <template> <output>")

with open(sys.argv[1], encoding="utf-8") as stream:
    cfg = yaml.safe_load(stream)

ips = socket.gethostbyname_ex("master")[2]
ip = next((v for v in ips if ":" not in v and not v.startswith("127.")), None)
if not ip:
    sys.exit("no Docker master IPv4 found")

k8s = cfg["k8s"]
k8s["name"] = "opskit-real-install"
k8s["hosts"] = [{
    "name": "master",
    "address": "master",
    "internalAddress": ip,
    "port": 22,
    "user": "opskit",
    "privateKeyPath": "/run/e2e/id_ed25519",
    "sudoPasswordEnv": "OPSKIT_DOCKER_E2E_PASSWORD",
}]
k8s["roleGroups"] = {"etcd": ["master"], "master": ["master"], "worker": []}
k8s["controlPlaneEndpoint"] = {"address": ip, "port": 6443}
k8s["kubernetes"]["version"] = "v1.28.15"
k8s["kubernetes"]["containerManager"] = "containerd"
k8s["containerd"]["version"] = "v1.7.24"
k8s["runc"]["version"] = "v1.2.6"
k8s["criTools"]["version"] = "v1.28.0"
k8s["cniPlugins"]["version"] = "v1.5.1"
k8s["network"]["manifest"] = ""
k8s["network"]["calico"]["version"] = "v3.26.5"
k8s["registry"]["imageRepository"] = "registry.k8s.io"
k8s["kubesphere"]["enabled"] = False
k8s["deploy"]["concurrency"] = 1
k8s["deploy"]["timeout"] = "32m"
k8s["deploy"]["outputDir"] = "/tmp/opskit-real-install-output"
k8s["deploy"]["nodeBinaries"] = {"amd64": "/usr/local/bin/opskit"}

install = cfg["install"]
for name in ("kubeadm", "kubelet", "kubectl"):
    install[name]["download_base"] = "https://dl.k8s.io/release"
for name in ("cri_tools", "containerd", "runc", "cni_plugins"):
    install[name]["download_base"] = "https://github.com"
install["download_dir"] = "/tmp/opskit-real-install-downloads"

# Replace the project owner's example private registry addresses with upstream
# public registries; do not disable TLS certificate validation.
cfg["mirrors"]["containerd"] = {
    registry: {"server": url}
    for registry, url in (
        ("docker.io", "https://registry-1.docker.io"),
        ("registry.k8s.io", "https://registry.k8s.io"),
        ("quay.io", "https://quay.io"),
    )
}
cfg["mirrors"]["docker"] = []
cfg["mirrors"]["docker_insecure_registries"] = []

with open(sys.argv[2], "w", encoding="utf-8") as stream:
    yaml.safe_dump(cfg, stream, allow_unicode=True, sort_keys=False)
os.chmod(sys.argv[2], 0o600)
print(f"Isolated installation target: master={ip}, Kubernetes=v1.28.15")
