package app

import (
	"fmt"
)

// Version profiles are deliberately pinned. They are compatibility candidates,
// not evidence that every Kubernetes minor has passed a real cluster install.
// New Kubernetes minors must be reviewed and added explicitly.
type clusterVersionProfile struct {
	containerd string
	runc       string
	cni        string
	crictl     string
	calico     string
}

func clusterProfile(minor int) (clusterVersionProfile, error) {
	// Keep old kubeadm-era profiles usable without forcing the newer
	// containerd 2.x on Kubernetes 1.24/1.25.
	profile := clusterVersionProfile{
		containerd: "v1.7.24",
		runc:       "v1.2.6",
		cni:        "v1.5.1",
	}
	switch minor {
	case 24:
		profile.crictl = "v1.24.2"
	case 25:
		profile.crictl = "v1.25.0"
	case 26:
		profile.crictl = "v1.26.0"
	case 27:
		profile.crictl = "v1.27.0"
	case 28:
		profile.crictl = "v1.28.0"
	case 29:
		profile.crictl = "v1.29.0"
	case 30:
		profile.crictl = "v1.30.1"
	case 31:
		profile.crictl = "v1.31.1"
	case 32, 33, 34, 35, 36, 37:
		profile.crictl = fmt.Sprintf("v1.%d.0", minor)
	default:
		return clusterVersionProfile{}, fmt.Errorf("Kubernetes 1.%d 尚无经过审查的依赖版本配置；支持范围 1.24～1.37，不能使用 latest 自动猜测", minor)
	}
	if minor >= 31 {
		profile.containerd = "v2.1.3"
	}
	if minor <= 28 {
		profile.calico = "v3.26.5"
	}
	return profile, nil
}

func autoVersion(value *string, pinned string) {
	if *value == "" || *value == "auto" {
		*value = pinned
	}
}

// resolveClusterComponentVersions applies only entries that were set to auto
// (or omitted). Explicit component pins always win and remain validated by
// the normal compatibility checks.
func resolveClusterComponentVersions(c *fileConfig) error {
	minor, err := kubeadmMinor(c.K8s.Kubernetes.Version)
	if err != nil {
		return err
	}
	profile, err := clusterProfile(minor)
	if err != nil {
		return err
	}
	autoVersion(&c.K8s.Containerd.Version, profile.containerd)
	autoVersion(&c.K8s.Runc.Version, profile.runc)
	autoVersion(&c.K8s.CNIPlugins.Version, profile.cni)
	autoVersion(&c.K8s.CRITools.Version, profile.crictl)
	if profile.calico != "" {
		autoVersion(&c.K8s.Network.Calico.Version, profile.calico)
	}
	// For newer Kubernetes versions there is no verified pinned Calico
	// manifest/checksum in the existing renderer. Validation requires users
	// to supply network.manifest instead of silently installing old Calico.
	c.projectComponents()
	return nil
}
