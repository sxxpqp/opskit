package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClusterDependenciesInstallMissingPackages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	for _, manager := range []string{"apt-get", "dnf", "yum"} {
		t.Run(manager, func(t *testing.T) {
			dir := t.TempDir()
			for _, cmd := range strings.Fields("tar systemctl modprobe sysctl swapoff socat iptables mount nsenter ethtool") {
				if err := os.WriteFile(filepath.Join(dir, cmd), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$TEST_LOG"
if test "$1" = update; then exit 0; fi
if test "$TEST_FAIL" = 1; then exit 42; fi
for cmd in conntrack ip tc; do
 printf '#!/bin/sh\nexit 0\n' > "$PATH/$cmd"
 /bin/chmod 700 "$PATH/$cmd"
done
`
			if err := os.WriteFile(filepath.Join(dir, manager), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "packages.log")
			run := func(fail string) ([]byte, error) {
				cmd := exec.Command("/bin/sh", "-c", clusterDependenciesScript)
				cmd.Env = append(os.Environ(), "PATH="+dir, "TEST_LOG="+log, "TEST_FAIL="+fail)
				return cmd.CombinedOutput()
			}
			if _, err := run("1"); err == nil {
				t.Fatal("package installation failure ignored")
			}
			if err := os.Remove(log); err != nil {
				t.Fatal(err)
			}
			if out, err := run("0"); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			raw, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			expected := "install -y conntrack-tools iproute iproute-tc"
			if manager == "apt-get" {
				expected = "install -y --no-install-recommends conntrack iproute2"
			}
			if !strings.Contains(string(raw), expected) {
				t.Fatalf("unexpected packages: %s", raw)
			}
			if out, err := run("1"); err != nil {
				t.Fatalf("complete dependencies should skip package installation: %v %s", err, out)
			}
			after, _ := os.ReadFile(log)
			if string(after) != string(raw) {
				t.Fatal("installed existing packages again")
			}
		})
	}
}
