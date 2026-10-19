#!/usr/bin/env python3
"""Stream OpsKit install output to CI, removing credential material first.

Use as a pipe between the installer and tee so neither public Actions logs
nor uploaded install.log receive unfiltered kubeadm join tokens or keys.
This is best-effort scrubbing for the disposable E2E test, not a general
secret-handling boundary for production credentials.
"""
import os
import re
import sys
import subprocess
import threading
from pathlib import Path

ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
JOIN_TOKEN = re.compile(r"(?<![A-Za-z0-9])[a-z0-9]{6}\.[a-z0-9]{16}(?![A-Za-z0-9])", re.I)
SECRET_FLAG = re.compile(
    r"(?i)(--(?:token|certificate-key|password|client-key|secret|access-key)"
    r"(?:=|\s+))([^\s]+)"
)
SECRET_FIELD = re.compile(
    r"""(?ix)
    (
      ["']?
      (?:password|passwd|sudoPassword|passwordEnv|privateKey|privateKeyPassphrase|
         token|client-key-data|client-certificate-data|certificate-authority-data|
         access_key|secret_key|session_token|authorization)
      ["']?
      \s*[:=]\s*
    )
    (\S+)
    """
)
BEARER = re.compile(r"(?i)(\bBearer\s+)[A-Za-z0-9._~+/-]+")
BASIC_AUTH_URL = re.compile(r"(?i)(https?://)[^/@\s]+:[^/@\s]+@")
BEGIN_PEM = re.compile(r"-----BEGIN [A-Z ]*(?:PRIVATE KEY|CERTIFICATE)-----")
END_PEM = re.compile(r"-----END [A-Z ]*(?:PRIVATE KEY|CERTIFICATE)-----")
KNOWN_SECRETS = [
    value
    for name in ("OPSKIT_DOCKER_E2E_PASSWORD",)
    if (value := os.environ.get(name, "")) and len(value) >= 4
]


def redact(line: str) -> str:
    line = ANSI.sub("", line).rstrip("\r\n")
    for secret in KNOWN_SECRETS:
        line = line.replace(secret, "<redacted>")
    line = BASIC_AUTH_URL.sub(r"\1<redacted>@", line)
    # Bearer must be removed BEFORE the authorization: field. Otherwise the
    # field filter hides only the word "Bearer" and leaves its token behind.
    line = BEARER.sub(r"\1<redacted>", line)
    line = SECRET_FLAG.sub(r"\1<redacted>", line)
    line = SECRET_FIELD.sub(r"\1<redacted>", line)
    line = JOIN_TOKEN.sub("<join-token>", line)
    # The Actions runner parses workflow commands sent by programs; never
    # allow subprocess output to create a GitHub Actions command.
    if line.startswith("::"):
        line = "[installer] " + line
    if len(line) > 4096:
        return "[installer line omitted: exceeds 4096 characters]"
    return line


def stream_redacted(stream, emit) -> None:
    """Each input stream has its own PEM-block suppression state."""
    in_pem = False
    for line in stream:
        if BEGIN_PEM.search(line):
            emit("[PEM key/certificate redacted]")
            in_pem = not bool(END_PEM.search(line))
            continue
        if in_pem:
            if END_PEM.search(line):
                in_pem = False
            continue
        emit(redact(line))


def run_command(log_path: str, command: list[str]) -> int:
    """Capture subprocess stdout and stderr separately; never publish raw bytes.

    The two readers run concurrently to avoid deadlocks when one pipe fills.
    A shared lock ensures each sanitized line is atomically written to both
    GitHub Actions stdout and the persistent diagnostic artifact.
    """
    if not command:
        raise ValueError("Missing installer command")
    failures = []
    with Path(log_path).open("w", encoding="utf-8", buffering=1) as log:
        proc = subprocess.Popen(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            errors="replace",
            bufsize=1,
        )
        lock = threading.Lock()

        def read(channel, pipe) -> None:
            try:
                def emit(line: str) -> None:
                    # Keep the original source of errors visible in CI.
                    message = f"[{channel}] {line}"
                    with lock:
                        # Preserve the actual stdout/stderr file descriptors,
                        # as well as a tagged copy in the artifact log.
                        target = sys.stderr if channel == "STDERR" else sys.stdout
                        print(message, file=target, flush=True)
                        log.write(message + "\n")
                        log.flush()
                with pipe:
                    stream_redacted(pipe, emit)
            except Exception:
                failures.append(channel)

        streams = [
            threading.Thread(target=read, args=("STDOUT", proc.stdout)),
            threading.Thread(target=read, args=("STDERR", proc.stderr)),
        ]
        for worker in streams:
            worker.start()
        status = proc.wait()
        for worker in streams:
            worker.join()

    if failures:
        print("[diagnostics] Output capture failed; refusing to report success.", flush=True)
        return 1
    # Preserve the original installer exit code; a negative signal exit maps
    # to the usual shell convention (128 + signal number).
    return status if status >= 0 else 128 - status


def main() -> int:
    if len(sys.argv) >= 3 and sys.argv[1] == "--run":
        if len(sys.argv) < 4:
            print("Usage: stream-install-log.py --run LOGFILE COMMAND [ARGS...]", file=sys.stderr)
            return 2
        return run_command(sys.argv[2], sys.argv[3:])
    stream_redacted(sys.stdin, lambda line: print(line, flush=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
