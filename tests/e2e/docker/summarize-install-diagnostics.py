#!/usr/bin/env python3
"""Print a small, high-signal failure report from node-diagnostics.log.

Docker Kubernetes E2E can generate hundreds of near-identical kubelet errors.
The actual Calico container creation failure is in Pod Events, usually well
BEFORE the last 180 lines of journal output. Present root causes first.
Pipe this output through stream-install-log.py before publishing to Actions.
"""
import re
import sys

ID = re.compile(r"\b[a-f0-9]{32,64}\b", re.I)
SPACE = re.compile(r"\s+")
CRITICAL = re.compile(
    r"not a shared mount|mount propagation|failed to generate (?:container|spec)|"
    r"Init:CreateContainerError|CrashLoopBackOff|ImagePullBackOff|"
    r"ErrImagePull|permission denied|operation not permitted|"
    r"no space left on device|failed to mount|OOMKilled|exec format error|"
    r"failed to create container|failed to start container",
    re.I,
)
CALICO_EVENTS = re.compile(r"\bWarning\s+Failed\b.*\bcalico-node", re.I)
KUBELET_EVENTS = re.compile(
    r"(?:\bWarning\b|\blevel=error\b).*(?:calico-node|calico|coredns|"
    r"FailedCreatePodSandBox|NetworkPluginNotReady|mount|containerd)",
    re.I,
)


def normalize(line: str) -> str:
    line = ID.sub("<container-id>", line.strip())
    line = SPACE.sub(" ", line)
    if len(line) > 650:
        line = line[:650] + " [truncated]"
    return line


def pick(lines, matcher, limit: int) -> list[str]:
    chosen = []
    seen = set()
    for raw in lines:
        if not matcher.search(raw):
            continue
        message = normalize(raw)
        # Kubelet often repeats the exact cause with varying event age and
        # container ID; do not let duplicates obscure distinct failures.
        key = re.sub(r"^\S+\s+Warning\s+Failed\s+", "Warning Failed ", message)
        key = re.sub(r"^\d+[smhd]\S*\s+", "", key)
        if key in seen:
            continue
        seen.add(key)
        chosen.append(message)
        if len(chosen) >= limit:
            break
    return chosen


def summarize(lines: list[str]) -> str:
    output = ["=== KUBERNETES INSTALL FAILURE SUMMARY ==="]
    try:
        start = next(i for i, line in enumerate(lines) if line.startswith("=== Kubernetes node and system pod state ==="))
    except StopIteration:
        start = -1
    if start >= 0:
        output.append("-- Node and Pod readiness (first 16 lines) --")
        output.extend(normalize(x) for x in lines[start + 1:start + 17] if x.strip())

    primary = pick(lines, CRITICAL, 8)
    output.append("-- Root-cause candidates from Pod events / CRI --")
    output.extend(primary or ["No specific container/runtime failure found."])

    calico = pick(lines, CALICO_EVENTS, 4)
    if calico:
        output.append("-- Calico Pod warning events --")
        output.extend(calico)

    if not primary and not calico:
        secondary = pick(lines, KUBELET_EVENTS, 8)
        if secondary:
            output.append("-- Other kubelet / containerd warnings --")
            output.extend(secondary)
        else:
            output.append("-- No classified errors; inspect the full node-diagnostics.log artifact. --")

    output.append("Full diagnostic logs: GitHub Actions artifact node-diagnostics.log")
    return "\n".join(output) + "\n"


if __name__ == "__main__":
    sys.stdout.write(summarize(sys.stdin.readlines()))
