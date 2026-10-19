# Docker-first Kubernetes integration checks

This test suite separates **real container execution** from **real Kubernetes installation**.

## Test matrix

| Layer | What actually runs | Valid claim |
| --- | --- | --- |
| Existing Go unit suite | Go tests with mock cluster remotes, fake tools and HTTP fixtures | Cluster orchestration logic, failure behavior and version validation |
| Docker SSH/systemd integration | Two Ubuntu 22.04 containers with real systemd (PID 1), sshd and sudo; OpsKit's real SSH client and host preflight; real uploads | SSH password/key auth, sudo via stdin, root/dependency preflight, private file upload, CLI dry run |
| Kind smoke | Two Kind nodes with running Kubernetes, API readiness and CoreDNS rollout | Kubernetes-in-Docker boots and basic Kubernetes services become ready |
| VM/cloud installation (future) | Clean OS nodes running \`opskit k8s create\` through \`kubeadm init/join\` | Full installation from scratch, OS/kernel and network compatibility |

**Neither the SSH/systemd container test nor Kind proves that OpsKit can install Kubernetes from scratch.** Kind already includes its own Kubernetes components and initialization. Do not mark this as production-ready installation acceptance.

## Run on Linux, Windows or macOS

Requirements: Docker Engine with Docker Compose v2, or Docker Desktop using the Linux container backend. This harness needs privileged Linux containers, PID 1 systemd and usable cgroups; Docker Desktop support is backend-dependent. No manually provisioned VM, external SSH ports, cloud credentials or KVM setup is required.

From the repository root, run:

\`\`\`bash
bash tests/e2e/docker/run.sh
\`\`\`

On Windows, execute that script from WSL or a Bash environment with Docker CLI access. All platforms can alternatively run these commands with Docker Compose v2 from the repository root:

\`\`\`bash
docker compose -f tests/e2e/docker/compose.yaml up -d --build --wait master worker
docker compose -f tests/e2e/docker/compose.yaml run --build --rm tester
docker compose -f tests/e2e/docker/compose.yaml down --volumes --remove-orphans
\`\`\`

The helper script additionally captures \`compose.log\` and performs cleanup on failure. Test output is stored in \`tests/e2e/docker/artifacts/\` (ignored by Git). CI uploads the reports as short-lived artifacts.

Use **dedicated/disposable** Docker hosts only. The test nodes use \`privileged: true\`, which grants elevated access to the Docker host's kernel. Do not use this suite on a shared production Docker daemon or expose its Docker socket to the test containers. All services reside on a private Docker network; no SSH port is published. Each run creates fresh SSH key and password material in a dedicated Compose volume, which is removed during cleanup. Treat this volume and generated logs as secrets until deleted.

For portability, the integration test deliberately **does not run kubeadm init**, install a container runtime inside the host, disable host swap, or modify host networking. It runs the *exact same preflight command* used by \`k8s create\`, exercising OpsKit's own SSH code. The default code currently skips SSH host-key verification; that is an existing project security limitation, not a recommended production configuration.

## Kind smoke

The GitHub workflow \`.github/workflows/k8s-docker-e2e.yml\` creates a two-node Kind cluster and verifies the API, all Node Ready conditions and CoreDNS rollout. To run this manually, install Kind and kubectl:

\`\`\`bash
kind create cluster --name opskit-e2e-kind --config tests/e2e/docker/kind.yaml --wait 300s
kubectl --context kind-opskit-e2e-kind get --raw=/readyz
kubectl --context kind-opskit-e2e-kind wait --for=condition=Ready nodes --all --timeout=180s
kubectl --context kind-opskit-e2e-kind -n kube-system rollout status deployment/coredns --timeout=180s
kind delete cluster --name opskit-e2e-kind
\`\`\`

Kind uses Docker under the hood. Windows/macOS developers do not need to create separate VMs by hand, but Docker Desktop itself uses a Linux virtualization backend.

## Diagnosing failures

- **Node startup/health timeout:** inspect \`compose.log\` and check \`/run/systemd/system\`, cgroup support and sshd state. A Docker Desktop limitation is not automatically an OpsKit bug.
- **SSH authentication/sudo failure:** inspect \`ssh-systemd.log\`; secrets and SSH key are generated only for this test.
- **Preflight missing dependency:** add the fixture's missing package, not a permissive bypass in OpsKit.
- **Kind failure:** inspect the uploaded Kind cluster logs and \`kind-smoke.log\`; no claim about OpsKit's kubeadm installer follows from Kind failures.

GitHub Actions runs these jobs separately on ephemeral hosted Ubuntu workers. The existing OS matrix tests still cover Go correctness on Windows/macOS; actual privileged systemd container support on each Docker Desktop backend needs explicit verification.
