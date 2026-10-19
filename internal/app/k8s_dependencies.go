package app

// Install only packages providing missing commands; retain existing OS packages.
// Run after all read-only node checks and management-side downloads succeed.
const clusterDependenciesScript = `set -eu
packages=""
if command -v apt-get >/dev/null; then
  manager=apt
elif command -v dnf >/dev/null; then
  manager=dnf
elif command -v yum >/dev/null; then
  manager=yum
else
  manager=none
fi
for pair in tar:tar modprobe:kmod sysctl:procps swapoff:util-linux conntrack:conntrack socat:socat iptables:iptables ip:iproute2 tc:iproute2 mount:mount nsenter:util-linux ethtool:ethtool; do
  cmd=${pair%%:*}
  pkg=${pair#*:}
  if command -v "$cmd" >/dev/null; then continue; fi
  if test "$manager" = none; then echo "missing dependency and unsupported package manager: $cmd" >&2; exit 1; fi
  if test "$manager" != apt; then
    case "$pkg" in
      procps) pkg=procps-ng ;;
      conntrack) pkg=conntrack-tools ;;
      iproute2) if test "$cmd" = tc; then pkg=iproute-tc; else pkg=iproute; fi ;;
      mount) pkg=util-linux ;;
    esac
  fi
  case " $packages " in *" $pkg "*) ;; *) packages="$packages $pkg" ;; esac
done
if test -n "$packages"; then
  echo "Installing missing OS packages from configured repositories:$packages"
  case "$manager" in
    apt) apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $packages ;;
    dnf) dnf install -y $packages ;;
    yum) yum install -y $packages ;;
  esac
fi
for cmd in tar systemctl modprobe sysctl swapoff conntrack socat iptables ip tc mount nsenter ethtool; do
  command -v "$cmd" >/dev/null || { echo "missing dependency after installation: $cmd" >&2; exit 1; }
done
echo "System dependencies ready"
`
