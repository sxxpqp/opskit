package app

// Runtime defaults follow the Docker-bundled pairs in KubeKey's component
// matrix (snapshot 2026-10-19), within OpsKit's supported Kubernetes range.
// https://github.com/kubesphere/kubekey/blob/main/docs/en/installation/components.md
func applyClusterComponentDefaults(c *clusterSettings) error {
	minor, err := kubeadmMinor(c.Kubernetes.Version)
	if err != nil {
		return err
	}
	cri := map[int]string{24: "v1.24.2", 25: "v1.25.0", 26: "v1.26.1", 27: "v1.27.1", 28: "v1.28.0", 29: "v1.29.0", 30: "v1.30.1"}[minor]
	containerd, runc, docker := "v1.7.28", "v1.3.3", "v28.5.2"
	if minor >= 30 {
		containerd, runc, docker = "v2.2.6", "v1.3.6", "v29.6.2"
	}
	for _, item := range []struct {
		setting *versionSetting
		version string
	}{
		{&c.Containerd, containerd}, {&c.Runc, runc}, {&c.CRITools, cri},
		{&c.CNIPlugins, "v1.9.1"}, {&c.Docker, docker}, {&c.CRIDockerd, "v0.4.7"},
	} {
		if item.setting.Version == "" {
			item.setting.Version = item.version
		}
	}
	return nil
}
