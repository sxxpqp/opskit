package app

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type versionSetting struct {
	Version string `yaml:"version" json:"version"`
}
type sphereSettings struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	Version         string `yaml:"version" json:"version"`
	StorageClass    string `yaml:"storageClass" json:"storageClass"`
	InstallerSource string `yaml:"installerSource" json:"installerSource"`
	Password        string `yaml:"password" json:"password"`
	ShowPassword    bool   `yaml:"showPassword" json:"showPassword"`
}
type clusterHost struct {
	Name                    string `yaml:"name" json:"name"`
	Address                 string `yaml:"address" json:"address"`
	InternalAddress         string `yaml:"internalAddress" json:"internalAddress"`
	Port                    int    `yaml:"port" json:"port"`
	User                    string `yaml:"user" json:"user"`
	Password                string `yaml:"password" json:"-"`
	PasswordEnv             string `yaml:"passwordEnv" json:"-"`
	PrivateKeyPath          string `yaml:"privateKeyPath" json:"-"`
	HostKeySHA256           string `yaml:"hostKeySHA256" json:"-"`
	PrivateKeyPassphraseEnv string `yaml:"privateKeyPassphraseEnv" json:"-"`
	SudoPassword            string `yaml:"sudoPassword" json:"-"`
	SudoPasswordEnv         string `yaml:"sudoPasswordEnv" json:"-"`
}
type clusterSettings struct {
	Name       string        `yaml:"name" json:"name"`
	Hosts      []clusterHost `yaml:"hosts" json:"-"`
	RoleGroups struct {
		Etcd   []string `yaml:"etcd" json:"etcd"`
		Master []string `yaml:"master" json:"master"`
		Worker []string `yaml:"worker" json:"worker"`
	} `yaml:"roleGroups" json:"roleGroups"`
	ControlPlaneEndpoint struct {
		Address string `yaml:"address" json:"address"`
		Port    int    `yaml:"port" json:"port"`
	} `yaml:"controlPlaneEndpoint" json:"controlPlaneEndpoint"`
	Kubernetes struct {
		Version          string `yaml:"version" json:"version"`
		ContainerManager string `yaml:"containerManager" json:"containerManager"`
		ClusterName      string `yaml:"clusterName" json:"clusterName"`
	} `yaml:"kubernetes" json:"kubernetes"`
	Containerd versionSetting `yaml:"containerd" json:"containerd"`
	Etcd       versionSetting `yaml:"etcd" json:"etcd"`
	Runc       versionSetting `yaml:"runc" json:"runc"`
	CRITools   versionSetting `yaml:"criTools" json:"criTools"`
	CNIPlugins versionSetting `yaml:"cniPlugins" json:"cniPlugins"`
	Docker     versionSetting `yaml:"docker" json:"docker"`
	CRIDockerd versionSetting `yaml:"criDockerd" json:"criDockerd"`
	Network    struct {
		PodSubnet     string         `yaml:"kubePodsCIDR" json:"kubePodsCIDR"`
		ServiceSubnet string         `yaml:"kubeServiceCIDR" json:"kubeServiceCIDR"`
		Manifest      string         `yaml:"manifest" json:"manifest"`
		Calico        versionSetting `yaml:"calico" json:"calico"`
	} `yaml:"network" json:"network"`
	Registry struct {
		ImageRepository string `yaml:"imageRepository" json:"imageRepository"`
	} `yaml:"registry" json:"registry"`
	KubeSphere sphereSettings `yaml:"kubesphere" json:"kubesphere"`
	Deploy     struct {
		Concurrency  int               `yaml:"concurrency" json:"concurrency"`
		Timeout      string            `yaml:"timeout" json:"timeout"`
		NodeBinaries map[string]string `yaml:"nodeBinaries" json:"-"`
		OutputDir    string            `yaml:"outputDir" json:"-"`
	} `yaml:"deploy" json:"-"`
}

// These are private local-command projections, not legacy YAML aliases.
func (c *fileConfig) projectComponents() {
	// Derive defaults without filling the raw config: CLI Kubernetes overrides
	// must select their own matching profile later.
	resolved := c.K8s
	_ = applyClusterComponentDefaults(&resolved)
	c.Versions.Kind = c.Kind.Version
	c.Versions.Kubernetes = c.K8s.Kubernetes.Version
	c.Versions.Kubectl = c.K8s.Kubernetes.Version
	c.Versions.KubeSphere = c.K8s.KubeSphere.Version
	c.Versions.Containerd = resolved.Containerd.Version
	c.Versions.Docker = resolved.Docker.Version
	c.Versions.Runc = resolved.Runc.Version
	c.Versions.CRIDockerd = resolved.CRIDockerd.Version
	c.Versions.CNIPlugins = resolved.CNIPlugins.Version
	c.Versions.CRITools = resolved.CRITools.Version
	c.KubeSphere.StorageClass = c.K8s.KubeSphere.StorageClass
	c.KubeSphere.Password = c.K8s.KubeSphere.Password
	c.KubeSphere.InstallerSource = c.K8s.KubeSphere.InstallerSource
	c.Kubeadm.ClusterName = c.K8s.Kubernetes.ClusterName
	c.Kubeadm.PodSubnet = c.K8s.Network.PodSubnet
	c.Kubeadm.ServiceSubnet = c.K8s.Network.ServiceSubnet
	c.Kubeadm.NetworkManifest = c.K8s.Network.Manifest
	c.Kubeadm.ImageRepository = c.K8s.Registry.ImageRepository
	c.Kubeadm.CRISocket = clusterCRISocket(c.K8s.Kubernetes.ContainerManager)
	if c.K8s.ControlPlaneEndpoint.Address != "" {
		c.Kubeadm.ControlPlaneEndpoint = net.JoinHostPort(c.K8s.ControlPlaneEndpoint.Address, fmt.Sprint(c.K8s.ControlPlaneEndpoint.Port))
	}
}
func clusterCRISocket(manager string) string {
	if manager == "docker" {
		return "unix:///run/cri-dockerd.sock"
	}
	return "unix:///run/containerd/containerd.sock"
}
func validateCluster(c *clusterSettings) error {
	if err := applyClusterComponentDefaults(c); err != nil {
		return err
	}
	if c.Name == "" {
		return errors.New("未配置 k8s.name；请先执行 opskit config init 并编辑配置，或用 --config FILE 指定含 k8s 配置的文件")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`).MatchString(c.Name) {
		return errors.New("k8s.name 必须为 1～63 位小写 DNS 名称")
	}
	minor, err := kubeadmMinor(c.Kubernetes.Version)
	if err != nil {
		return err
	}
	if c.Kubernetes.ContainerManager != "containerd" && c.Kubernetes.ContainerManager != "docker" {
		return errors.New("k8s.kubernetes.containerManager 仅支持 containerd/docker")
	}
	if c.Kubernetes.ClusterName == "" {
		return errors.New("k8s.kubernetes.clusterName 不能为空")
	}
	if c.Kubernetes.ContainerManager == "containerd" {
		if !versionPattern.MatchString(c.Containerd.Version) {
			return errors.New("必须指定 k8s.containerd.version")
		}
		if err := checkKubeadmContainerd(minor, c.Containerd.Version); err != nil {
			return err
		}
	}
	return validateClusterDetails(c, minor)
}
func validateClusterDetails(c *clusterSettings, minor int) error {
	versions := []string{c.CNIPlugins.Version, c.CRITools.Version}
	if c.Kubernetes.ContainerManager == "containerd" {
		versions = append(versions, c.Runc.Version)
	} else {
		versions = append(versions, c.Docker.Version, c.CRIDockerd.Version)
	}
	for _, version := range versions {
		if !versionPattern.MatchString(version) {
			return errors.New("k8s 各组件必须显式指定完整 vX.Y.Z 版本")
		}
	}
	criMinor, err := kubeadmMinor(c.CRITools.Version)
	if err != nil || criMinor != minor {
		return errors.New("k8s.criTools.version 必须与 Kubernetes 同 minor")
	}
	if len(c.RoleGroups.Master) != 1 || len(c.RoleGroups.Etcd) != 1 || c.RoleGroups.Master[0] != c.RoleGroups.Etcd[0] {
		return errors.New("首版要求一个 master，etcd 与 master 同节点；不支持独立 etcd/HA")
	}
	known := map[string]bool{}
	ips := map[string]bool{}
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if !regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`).MatchString(h.Name) || known[h.Name] {
			return errors.New("hosts.name 非法或重复")
		}
		known[h.Name] = true
		if h.Address == "" || strings.ContainsAny(h.Address, " /\\\t\r\n") {
			return fmt.Errorf("%s: SSH address 无效", h.Name)
		}
		if h.InternalAddress == "" {
			h.InternalAddress = h.Address
		}
		ip := net.ParseIP(h.InternalAddress)
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ips[h.InternalAddress] {
			return fmt.Errorf("%s: internalAddress 必须为唯一可达 IPv4", h.Name)
		}
		ips[h.InternalAddress] = true
		if h.Port == 0 {
			h.Port = 22
		}
		if h.Port < 1 || h.Port > 65535 {
			return errors.New("SSH port 无效")
		}
		if h.User == "" {
			h.User = "root"
		}
		if h.Password != "" && h.PasswordEnv != "" {
			return errors.New("password 与 passwordEnv 互斥")
		}
		if (h.Password != "" || h.PasswordEnv != "") && h.PrivateKeyPath != "" {
			return errors.New("SSH 密码与私钥互斥")
		}
		if h.SudoPassword != "" && h.SudoPasswordEnv != "" {
			return errors.New("sudoPassword 与 sudoPasswordEnv 互斥")
		}
	}
	assigned := map[string]bool{}
	for _, name := range append(append([]string{}, c.RoleGroups.Master...), c.RoleGroups.Worker...) {
		if !known[name] || assigned[name] {
			return fmt.Errorf("roleGroups 节点不存在或 master/worker 重复: %s", name)
		}
		assigned[name] = true
	}
	if len(assigned) != len(c.Hosts) {
		return errors.New("每个 hosts 节点都必须出现在 master 或 worker 中")
	}
	if c.ControlPlaneEndpoint.Port == 0 {
		c.ControlPlaneEndpoint.Port = 6443
	}
	if c.ControlPlaneEndpoint.Address == "" {
		for _, h := range c.Hosts {
			if h.Name == c.RoleGroups.Master[0] {
				c.ControlPlaneEndpoint.Address = h.InternalAddress
			}
		}
	}
	if err := validateKubeadmEndpoint(net.JoinHostPort(c.ControlPlaneEndpoint.Address, fmt.Sprint(c.ControlPlaneEndpoint.Port))); err != nil {
		return err
	}
	if c.Network.Manifest == "" {
		if err := validateClusterCalico(c, minor); err != nil {
			return err
		}
	}
	if c.KubeSphere.Enabled {
		if c.KubeSphere.Version != "v3.4.1" {
			return errors.New("当前仅支持 KS v3.4.1")
		}
		if minor > 26 {
			return errors.New("KS v3.4.1 需要 Kubernetes 1.20～1.26")
		}
		if err := validateKSPassword(c.KubeSphere.Password); err != nil {
			return err
		}
	}
	if c.Deploy.Concurrency == 0 {
		c.Deploy.Concurrency = 3
	}
	if c.Deploy.Concurrency < 1 || c.Deploy.Concurrency > 32 {
		return errors.New("k8s.deploy.concurrency 需要 1～32")
	}

	var master clusterHost
	for _, h := range c.Hosts {
		if h.Name == c.RoleGroups.Master[0] {
			master = h
		}
	}
	_, err = renderKubeadm("init", c.Kubernetes.Version, kubeadmSettings{AdvertiseAddress: master.InternalAddress, ControlPlaneEndpoint: net.JoinHostPort(c.ControlPlaneEndpoint.Address, fmt.Sprint(c.ControlPlaneEndpoint.Port)), PodSubnet: c.Network.PodSubnet, ServiceSubnet: c.Network.ServiceSubnet, CRISocket: clusterCRISocket(c.Kubernetes.ContainerManager), ImageRepository: c.Registry.ImageRepository})
	return err
}
func resolveClusterPaths(c *clusterSettings, base string) {
	resolve := func(s string) string {
		if s != "" && !filepath.IsAbs(s) {
			return filepath.Join(base, s)
		}
		return s
	}
	c.Network.Manifest = resolve(c.Network.Manifest)
	c.Deploy.OutputDir = resolve(c.Deploy.OutputDir)
	for arch, path := range c.Deploy.NodeBinaries {
		c.Deploy.NodeBinaries[arch] = resolve(path)
	}
	for i := range c.Hosts {
		c.Hosts[i].PrivateKeyPath = resolve(c.Hosts[i].PrivateKeyPath)
	}
}
func secretValue(value, env string) (string, error) {
	if env == "" {
		return value, nil
	}
	secret := os.Getenv(env)
	if secret == "" {
		return "", fmt.Errorf("缺少环境变量 %s", env)
	}
	return secret, nil
}
