package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newK8sDownloadCommand(parent context.Context, out io.Writer) (*cobra.Command, error) {
	var defaults fileConfig
	if err := yamlClusterDefaults(&defaults); err != nil {
		return nil, err
	}
	cmd := &cobra.Command{Use: "download", Long: `只下载 Linux 组件，不连接 SSH、不安装、不启动服务或创建集群。
默认下载 kubeadm、kubelet、kubectl、crictl；--components 可指定组件列表。
支持 kubeadm,kubelet,kubectl,crictl,etcd,containerd,runc,cni-plugins,docker,cri-dockerd,calico。
etcd 必须显式配置版本；Calico 输出按 Pod CIDR 渲染的 network.yaml。
Docker/cri-dockerd 上游摘要未配置，仅记录本地 SHA-256，不宣称上游校验。
结果存入下载目录下独立的 k8s-* 子目录，保留原文件名和上游校验文件。
同一下载根目录复用完整且本地 SHA-256 校验通过的下载文件，仍执行上游摘要校验。
下载二进制不包含容器镜像或系统软件包；选择 kubelet 时附带 systemd 配置。`}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置")
	version := f.String("version", "", "Kubernetes 版本；默认 k8s.kubernetes.version")
	components := f.StringSlice("components", []string{"kubeadm", "kubelet", "kubectl", "crictl"}, "逗号分隔的组件名")
	arch := f.String("arch", runtime.GOARCH, "目标 Linux 架构：amd64 / arm64，与管理机系统无关")
	directory := f.String("download-dir", "", "输出根目录；默认 install.download_dir 或 ~/.opskit/downloads")
	etcdVersion := f.String("etcd-version", "", "etcd 完整版本；默认 k8s.etcd.version，选择 etcd 时必填")
	dry := f.Bool("dry-run", false, "只显示资源计划，不下载、不创建目录")
	timeout := f.Duration("timeout", 30*time.Minute, "整个下载任务的总超时")
	type sourceOption struct {
		flag, env string
		value     *string
		get       func(*fileConfig) *string
	}
	var sources []sourceOption
	for _, item := range []struct {
		flag, env string
		get       func(*fileConfig) *string
	}{
		{"kubeadm-download-base", "KUBEADM_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.Kubeadm.DownloadBase }},
		{"kubelet-download-base", "KUBELET_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.Kubelet.DownloadBase }},
		{"kubectl-download-base", "KUBECTL_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.Kubectl.DownloadBase }},
		{"cri-tools-download-base", "CRI_TOOLS_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.CRITools.DownloadBase }},
		{"etcd-download-base", "ETCD_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.Etcd.DownloadBase }},
		{"containerd-download-base", "CONTAINERD_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.Containerd.DownloadBase }},
		{"runc-download-base", "RUNC_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.Runc.DownloadBase }},
		{"cni-download-base", "CNI_PLUGINS_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.CNIPlugins.DownloadBase }},
		{"docker-binary-base", "DOCKER_BINARY_BASE", func(c *fileConfig) *string { return &c.Install.Docker.BinaryBase }},
		{"cri-dockerd-download-base", "CRI_DOCKERD_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.CRIDockerd.DownloadBase }},
		{"calico-download-base", "CALICO_DOWNLOAD_BASE", func(c *fileConfig) *string { return &c.Install.Calico.DownloadBase }},
	} {
		value := f.String(item.flag, *item.get(&defaults), "该组件下载根地址；"+item.env+" / install 对应配置")
		sources = append(sources, sourceOption{item.flag, item.env, value, item.get})
	}
	bindCommand(cmd, func() (int, error) {
		if f.NArg() != 0 || *timeout <= 0 || (*arch != "amd64" && *arch != "arm64") {
			return 2, errors.New("参数无效：仅支持 Linux amd64/arm64，timeout 必须大于零")
		}
		c, err := commandConfig(f, *config)
		if err != nil {
			return 2, err
		}
		for _, source := range sources {
			setEnvFlag(f, source.flag, source.env)
			setConfigFlag(f, source.flag, *source.get(&c))
			*source.get(&c) = *source.value
		}
		setConfigFlag(f, "version", c.K8s.Kubernetes.Version)
		if *version == "" {
			*version = defaults.K8s.Kubernetes.Version
		}
		c.K8s.Kubernetes.Version = *version
		setConfigFlag(f, "etcd-version", c.K8s.Etcd.Version)
		c.K8s.Etcd.Version = *etcdVersion
		setConfigFlag(f, "download-dir", c.Install.DownloadDir)
		if *directory == "" {
			*directory, err = defaultDownloadDir()
			if err != nil {
				return 1, err
			}
		}
		if err := applyClusterComponentDefaults(&c.K8s); err != nil {
			return 2, err
		}
		// Download-only commands do not validate SSH hosts or deployment roles.
		if c.K8s.Network.Calico.Version == "" {
			c.K8s.Network.Calico.Version = defaults.K8s.Network.Calico.Version
		}
		if c.K8s.Network.PodSubnet == "" {
			c.K8s.Network.PodSubnet = defaults.K8s.Network.PodSubnet
		}
		if c.Install.Calico.SHA256 == "" {
			c.Install.Calico.SHA256 = defaults.Install.Calico.SHA256
		}
		if c.Install.Calico.FallbackBases == nil {
			c.Install.Calico.FallbackBases = defaults.Install.Calico.FallbackBases
		}
		assets, calico, err := selectedClusterAssets(c, *arch, *components)
		if err != nil {
			return 2, err
		}
		for _, asset := range assets {
			fmt.Fprintf(out, "%s: %s；上游校验=%t\n", asset.Name, asset.URL, asset.CheckURL != "")
		}
		if calico {
			source := c.K8s.Network.Manifest
			if source == "" {
				source, _ = clusterCalicoSource(c)
			}
			fmt.Fprintf(out, "calico: %s，Pod CIDR=%s，清单=%s\n", c.K8s.Network.Calico.Version, c.K8s.Network.PodSubnet, source)
		}
		fmt.Fprintf(out, "输出根目录: %s（linux/%s）\n", *directory, *arch)
		if *dry {
			return 0, nil
		}
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		if err := saveClusterDownloads(ctx, c, assets, calico, *directory, *arch, out); err != nil {
			return taskError(ctx, err)
		}
		return 0, nil
	})
	return cmd, nil
}

func selectedClusterAssets(c fileConfig, arch string, names []string) ([]clusterAsset, bool, error) {
	if len(names) == 0 {
		return nil, false, errors.New("components 不能为空")
	}
	available := map[string]clusterAsset{}
	for _, manager := range []string{"containerd", "docker"} {
		c.K8s.Kubernetes.ContainerManager = manager
		for _, asset := range clusterAssets(c, arch) {
			available[asset.Name] = asset
		}
	}
	var assets []clusterAsset
	calico := false
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return nil, false, fmt.Errorf("重复组件: %s", name)
		}
		seen[name] = true
		if name == "calico" {
			minor, err := kubeadmMinor(c.K8s.Kubernetes.Version)
			if err != nil {
				return nil, false, err
			}
			if err := validateClusterCalico(&c.K8s, minor); err != nil {
				return nil, false, err
			}
			if c.K8s.Network.Manifest == "" {
				if _, err := clusterCalicoSource(c); err != nil {
					return nil, false, err
				}
			} else {
				raw, err := readClusterNetwork(c.K8s.Network.Manifest)
				if err != nil {
					return nil, false, err
				}
				if err := validateStandaloneCalicoManifest(raw, c.K8s.Network.Calico.Version, c.K8s.Network.PodSubnet); err != nil {
					return nil, false, err
				}
			}
			calico = true
			continue
		}
		asset, ok := available[name]
		if name == "etcd" {
			v := c.K8s.Etcd.Version
			if !versionPattern.MatchString(v) {
				return nil, false, errors.New("下载 etcd 需要 --etcd-version vX.Y.Z 或 k8s.etcd.version；不自动猜测版本")
			}
			base := strings.TrimRight(c.Install.Etcd.DownloadBase, "/") + "/etcd-io/etcd/releases/download/" + v
			file := fmt.Sprintf("etcd-%s-linux-%s.tar.gz", v, arch)
			asset = clusterAsset{Name: name, URL: base + "/" + file, CheckURL: base + "/SHA256SUMS", CheckName: file}
			ok = true
		}
		if !ok {
			return nil, false, fmt.Errorf("不支持的组件: %s；使用 k8s download --help 查看支持项", name)
		}
		for _, source := range []string{asset.URL, asset.CheckURL} {
			if source != "" {
				if err := validateKubeadmBase(source); err != nil {
					return nil, false, fmt.Errorf("%s: %w", name, err)
				}
			}
		}
		if name == "crictl" {
			cri, err := kubeadmMinor(c.K8s.CRITools.Version)
			kube, _ := kubeadmMinor(c.K8s.Kubernetes.Version)
			if err != nil || cri != kube {
				return nil, false, errors.New("k8s.criTools.version 必须与 Kubernetes 同 minor")
			}
		}
		versions := map[string]string{"containerd": c.K8s.Containerd.Version, "runc": c.K8s.Runc.Version, "cni-plugins": c.K8s.CNIPlugins.Version, "docker": c.K8s.Docker.Version, "cri-dockerd": c.K8s.CRIDockerd.Version}
		if v, ok := versions[name]; ok && !versionPattern.MatchString(v) {
			return nil, false, fmt.Errorf("%s 必须使用完整 vX.Y.Z 版本", name)
		}
		assets = append(assets, asset)
	}
	return assets, calico, nil
}

type componentDownloadRecord struct {
	Component        string `json:"component"`
	File             string `json:"file"`
	SHA256           string `json:"sha256"`
	Source           string `json:"source,omitempty"`
	UpstreamVerified bool   `json:"upstream_verified"`
}

func saveClusterDownloads(ctx context.Context, c fileConfig, assets []clusterAsset, calico bool, root, arch string, out io.Writer) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(root, ".k8s-download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	var records []componentDownloadRecord
	for _, asset := range assets {
		dir := filepath.Join(work, asset.Name)
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		if err := fetchClusterAssets(ctx, []clusterAsset{asset}, dir, root, out); err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(dir, "cache.json"))
		if err != nil {
			return err
		}
		var entries map[string]clusterCacheEntry
		if err := json.Unmarshal(raw, &entries); err != nil {
			return err
		}
		for source, entry := range entries {
			u, err := url.Parse(source)
			if err != nil {
				return err
			}
			name := path.Base(u.Path)
			if name == "." || name == "/" || name == ".." || strings.ContainsAny(name, "\\:") {
				return errors.New("无效的下载文件名")
			}
			if err := os.Rename(filepath.Join(dir, entry.File), filepath.Join(dir, name)); err != nil {
				return err
			}
			if source == asset.URL {
				records = append(records, componentDownloadRecord{asset.Name, asset.Name + "/" + name, entry.SHA256, source, asset.CheckURL != ""})
			}
		}
		if err := os.Remove(filepath.Join(dir, "cache.json")); err != nil {
			return err
		}
		if asset.Name == "kubelet" {
			for name, text := range map[string]string{"kubelet.service": kubeadmKubeletUnit, "10-kubeadm.conf": kubeadmKubeletDropin} {
				file := filepath.Join(dir, name)
				if err := os.WriteFile(file, []byte(text), 0600); err != nil {
					return err
				}
				m, err := inspect(file)
				if err != nil {
					return err
				}
				records = append(records, componentDownloadRecord{Component: "kubelet", File: "kubelet/" + name, SHA256: m.SHA256})
			}
		}
	}
	if calico {
		dir := filepath.Join(work, "calico")
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		c.Install.DownloadDir = root
		if err := prepareClusterNetwork(ctx, &c, dir, out); err != nil {
			return err
		}
		m, err := inspect(c.K8s.Network.Manifest)
		if err != nil {
			return err
		}
		// The rendered manifest has a different digest; upstream checks apply only to downloaded input.
		records = append(records, componentDownloadRecord{Component: "calico", File: "calico/network.yaml", SHA256: m.SHA256})
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "downloads.json"), raw, 0600); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target := filepath.Join(root, "k8s-"+c.K8s.Kubernetes.Version+"-linux-"+arch+"-"+strings.TrimPrefix(filepath.Base(work), ".k8s-download-"))
	if err := os.Rename(work, target); err != nil {
		return err
	}
	fmt.Fprintln(out, "下载完成（仅准备文件，未安装）:", target)
	return nil
}
