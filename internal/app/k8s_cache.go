package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	templates "github.com/sxxpqp/opskit"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type clusterCacheKey struct{}
type clusterCacheEntry struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type clusterAsset struct {
	Name      string
	URL       string
	CheckURL  string
	CheckName string
}

func cachedClusterDownload(ctx context.Context, source string, dst io.Writer) (bool, error) {
	root, ok := ctx.Value(clusterCacheKey{}).(string)
	if !ok {
		return false, nil
	}
	raw, err := os.ReadFile(filepath.Join(root, "cache.json"))
	if err != nil {
		return true, err
	}
	var entries map[string]clusterCacheEntry
	if err = json.Unmarshal(raw, &entries); err != nil {
		return true, err
	}
	entry, ok := entries[source]
	if !ok {
		return true, fmt.Errorf("离线分发缺少资源，不回源下载: %s", source)
	}
	if filepath.Base(entry.File) != entry.File {
		return true, errors.New("离线资源路径无效")
	}
	f, err := os.Open(filepath.Join(root, entry.File))
	if err != nil {
		return true, err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return true, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return true, errors.New("离线资源校验失败")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	_, err = io.Copy(dst, f)
	return true, err
}
func clusterAssets(c fileConfig, arch string) []clusterAsset {
	v := c.K8s.Kubernetes.Version
	var assets []clusterAsset
	for _, x := range []struct{ name, base string }{{"kubeadm", c.Install.Kubeadm.DownloadBase}, {"kubelet", c.Install.Kubelet.DownloadBase}, {"kubectl", c.Install.Kubectl.DownloadBase}} {
		u := fmt.Sprintf("%s/%s/bin/linux/%s/%s", strings.TrimRight(x.base, "/"), v, arch, x.name)
		assets = append(assets, clusterAsset{Name: x.name, URL: u, CheckURL: u + ".sha256"})
	}
	base := func(s string) string { return strings.TrimRight(s, "/") }
	cv := c.K8s.CRITools.Version
	u := fmt.Sprintf("%s/kubernetes-sigs/cri-tools/releases/download/%s/crictl-%s-linux-%s.tar.gz", base(c.Install.CRITools.DownloadBase), cv, cv, arch)
	assets = append(assets, clusterAsset{Name: "crictl", URL: u, CheckURL: u + ".sha256"})
	cni := c.K8s.CNIPlugins.Version
	u = fmt.Sprintf("%s/containernetworking/plugins/releases/download/%s/cni-plugins-linux-%s-%s.tgz", base(c.Install.CNIPlugins.DownloadBase), cni, arch, cni)
	assets = append(assets, clusterAsset{Name: "cni-plugins", URL: u, CheckURL: u + ".sha256"})
	if c.K8s.Kubernetes.ContainerManager == "containerd" {
		v := c.K8s.Containerd.Version
		u = fmt.Sprintf("%s/containerd/containerd/releases/download/%s/containerd-%s-linux-%s.tar.gz", base(c.Install.Containerd.DownloadBase), v, strings.TrimPrefix(v, "v"), arch)
		assets = append(assets, clusterAsset{Name: "containerd", URL: u, CheckURL: u + ".sha256sum"})
		rv := c.K8s.Runc.Version
		u = fmt.Sprintf("%s/opencontainers/runc/releases/download/%s/runc.%s", base(c.Install.Runc.DownloadBase), rv, arch)
		assets = append(assets, clusterAsset{Name: "runc", URL: u, CheckURL: fmt.Sprintf("%s/opencontainers/runc/releases/download/%s/runc.sha256sum", base(c.Install.Runc.DownloadBase), rv), CheckName: "runc." + arch})
	} else {
		machine := "x86_64"
		if arch == "arm64" {
			machine = "aarch64"
		}
		u = fmt.Sprintf("%s/%s/docker-%s.tgz", base(c.Install.Docker.BinaryBase), machine, strings.TrimPrefix(c.K8s.Docker.Version, "v"))
		assets = append(assets, clusterAsset{Name: "docker", URL: u})
		v := c.K8s.CRIDockerd.Version
		u = fmt.Sprintf("%s/Mirantis/cri-dockerd/releases/download/%s/cri-dockerd-%s.%s.tgz", base(c.Install.CRIDockerd.DownloadBase), v, strings.TrimPrefix(v, "v"), arch)
		assets = append(assets, clusterAsset{Name: "cri-dockerd", URL: u})
	}
	return assets
}
func fetchClusterAssets(ctx context.Context, assets []clusterAsset, root string, out io.Writer) error {
	entries := map[string]clusterCacheEntry{}
	if raw, err := os.ReadFile(filepath.Join(root, "cache.json")); err == nil {
		if err = json.Unmarshal(raw, &entries); err != nil {
			return err
		}
	}
	fetch := func(source string) (string, error) {
		if old, ok := entries[source]; ok {
			return filepath.Join(root, old.File), nil
		}
		key := sha256.Sum256([]byte(source))
		name := fmt.Sprintf("%x", key[:])
		path := filepath.Join(root, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		err = download(ctx, source, io.MultiWriter(f, hash))
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		entries[source] = clusterCacheEntry{File: name, SHA256: hex.EncodeToString(hash.Sum(nil))}
		return path, nil
	}
	for _, asset := range assets {
		if err := validateKubeadmBase(asset.URL); err != nil {
			return err
		}
		fmt.Fprintf(out, "下载并校验: %s\n", asset.URL)
		path, err := fetch(asset.URL)
		if err != nil {
			return err
		}
		if asset.CheckURL != "" {
			checksum, err := fetch(asset.CheckURL)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(checksum)
			if err != nil {
				return err
			}
			want := ""
			for _, line := range strings.Split(string(raw), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				if asset.CheckName == "" || len(fields) > 1 && strings.TrimPrefix(fields[1], "*") == asset.CheckName {
					want = fields[0]
					break
				}
			}
			if len(want) != 64 {
				return fmt.Errorf("资源校验摘要无效: %s", asset.URL)
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			hash := sha256.New()
			_, err = io.Copy(hash, f)
			f.Close()
			if err != nil {
				return err
			}
			if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), want) {
				return fmt.Errorf("资源 SHA-256 不匹配: %s", asset.URL)
			}
		}
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "cache.json"), raw, 0600)
}
func clusterNodeBinary(c clusterSettings, arch string) (string, error) {
	path := c.Deploy.NodeBinaries[arch]
	if path == "" && runtime.GOOS == "linux" && runtime.GOARCH == arch {
		var err error
		path, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	if path == "" {
		return "", fmt.Errorf("需要 k8s.deploy.nodeBinaries.%s 指定当前代码构建的 Linux opskit", arch)
	}
	return path, nil
}
func writeClusterArchive(dst io.Writer, files map[string]string) error {
	tw := tar.NewWriter(dst)
	for name, path := range files {
		if filepath.Base(name) != name {
			return errors.New("bundle 文件名无效")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		mode := int64(0600)
		if name == "opskit" {
			mode = 0755
		}
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: info.Size()}); err != nil {
			f.Close()
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	return tw.Close()
}
func mergeClusterDefaults(c *fileConfig) error {
	var defaults fileConfig
	// Keep sources in the embedded template, never duplicate provider URLs in code.
	if err := yamlClusterDefaults(&defaults); err != nil {
		return err
	}
	if c.Install.Calico.DownloadBase == "" {
		c.Install.Calico.DownloadBase = defaults.Install.Calico.DownloadBase
	}
	if c.Install.Calico.SHA256 == "" {
		c.Install.Calico.SHA256 = defaults.Install.Calico.SHA256
	}
	if c.K8s.Network.Calico.Version == "" {
		c.K8s.Network.Calico = defaults.K8s.Network.Calico
	}
	if c.Mirrors.Docker == nil {
		c.Mirrors.Docker = defaults.Mirrors.Docker
	}
	if c.Mirrors.DockerInsecure == nil {
		c.Mirrors.DockerInsecure = defaults.Mirrors.DockerInsecure
	}
	if c.Mirrors.Containerd == nil {
		c.Mirrors.Containerd = defaults.Mirrors.Containerd
	}
	if c.Install.Kubeadm.DownloadBase == "" {
		c.Install.Kubeadm = defaults.Install.Kubeadm
	}
	if c.Install.Kubelet.DownloadBase == "" {
		c.Install.Kubelet = defaults.Install.Kubelet
	}
	if c.Install.Kubectl.DownloadBase == "" {
		c.Install.Kubectl = defaults.Install.Kubectl
	}
	if c.Install.CRITools.DownloadBase == "" {
		c.Install.CRITools = defaults.Install.CRITools
	}
	if c.Install.Containerd.DownloadBase == "" {
		c.Install.Containerd = defaults.Install.Containerd
	}
	if c.Install.Runc.DownloadBase == "" {
		c.Install.Runc = defaults.Install.Runc
	}
	if c.Install.CNIPlugins.DownloadBase == "" {
		c.Install.CNIPlugins = defaults.Install.CNIPlugins
	}
	if c.Install.CRIDockerd.DownloadBase == "" {
		c.Install.CRIDockerd = defaults.Install.CRIDockerd
	}
	if c.Install.Docker.BinaryBase == "" {
		c.Install.Docker.BinaryBase = defaults.Install.Docker.BinaryBase
	}
	return nil
}

// Written separately so archive transfers can use streams without exposing secrets in shell commands.
func bytesFile(path string, data []byte) error { return os.WriteFile(path, bytes.Clone(data), 0600) }

func yamlClusterDefaults(c *fileConfig) error {
	if err := yaml.Unmarshal(templates.ConfigTemplate, c); err != nil {
		return err
	}
	c.projectComponents()
	return nil
}
