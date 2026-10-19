FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive
ENV container=docker

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       systemd systemd-sysv openssh-server openssh-client sudo \
       ca-certificates iproute2 iptables conntrack socat kmod procps \
       util-linux ethtool tar \
    && useradd --create-home --shell /bin/bash opskit \
    && printf 'opskit ALL=(ALL:ALL) ALL\n' > /etc/sudoers.d/opskit \
    && chmod 0440 /etc/sudoers.d/opskit \
    && mkdir -p /run/sshd /etc/ssh/sshd_config.d \
    && printf 'PasswordAuthentication yes\nPermitRootLogin no\n' > /etc/ssh/sshd_config.d/99-opskit-e2e.conf \
    && systemctl enable ssh.service \
    && rm -rf /var/lib/apt/lists/*

COPY tests/e2e/docker/node-entrypoint.sh /usr/local/bin/opskit-e2e-entrypoint
RUN chmod 0755 /usr/local/bin/opskit-e2e-entrypoint

STOPSIGNAL SIGRTMIN+3
ENTRYPOINT ["/usr/local/bin/opskit-e2e-entrypoint"]
