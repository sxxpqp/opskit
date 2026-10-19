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
    line = SECRET_FLAG.sub(r"\1<redacted>", line)
    line = SECRET_FIELD.sub(r"\1<redacted>", line)
    line = BEARER.sub(r"\1<redacted>", line)
    line = JOIN_TOKEN.sub("<join-token>", line)
    # The Actions runner parses workflow commands sent by programs; never
    # allow subprocess output to create a GitHub Actions command.
    if line.startswith("::"):
        line = "[installer] " + line
    if len(line) > 4096:
        return "[installer line omitted: exceeds 4096 characters]"
    return line


def main() -> None:
    in_pem = False
    for line in sys.stdin:
        if BEGIN_PEM.search(line):
            print("[PEM key/certificate redacted]", flush=True)
            in_pem = not bool(END_PEM.search(line))
            continue
        if in_pem:
            if END_PEM.search(line):
                in_pem = False
            continue
        print(redact(line), flush=True)


if __name__ == "__main__":
    main()
