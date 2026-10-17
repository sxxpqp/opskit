package app

import (
	"flag"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKindIngressPortMappings(t *testing.T) {
	for _, custom := range []bool{false, true} {
		f := flag.NewFlagSet("kind", flag.ContinueOnError)
		o := clusterFlags(f)
		var cfg fileConfig
		if custom {
			cfg.Kind.HTTPPort = 8080
			cfg.Kind.HTTPSPort = 8443
			cfg.Kind.ConsolePort = 30881
			if err := f.Parse([]string{"--http-port", "18080"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := applyClusterConfig(f, o, cfg); err != nil {
			t.Fatal(err)
		}
		b, err := renderKind(*o)
		if err != nil {
			t.Fatal(err)
		}
		var cluster struct {
			Nodes []struct {
				Role   string            `yaml:"role"`
				Labels map[string]string `yaml:"labels"`
				Ports  []struct {
					Container int    `yaml:"containerPort"`
					Host      int    `yaml:"hostPort"`
					Listen    string `yaml:"listenAddress"`
					Protocol  string `yaml:"protocol"`
				} `yaml:"extraPortMappings"`
			} `yaml:"nodes"`
		}
		if err = yaml.Unmarshal(b, &cluster); err != nil {
			t.Fatal(err)
		}
		if len(cluster.Nodes) != 3 || cluster.Nodes[0].Role != "control-plane" || cluster.Nodes[0].Labels["ingress-ready"] != "true" {
			t.Fatal(string(b))
		}
		expected := map[int]int{80: 80, 443: 443, 30880: 30880}
		if custom {
			expected = map[int]int{80: 18080, 443: 8443, 30880: 30881}
		}
		if len(cluster.Nodes[0].Ports) != 3 {
			t.Fatal(string(b))
		}
		for _, p := range cluster.Nodes[0].Ports {
			listen := "0.0.0.0"
			if p.Container == 30880 {
				listen = "127.0.0.1"
			}
			if expected[p.Container] != p.Host || p.Listen != listen || p.Protocol != "TCP" {
				t.Fatal(string(b))
			}
			delete(expected, p.Container)
		}
		if len(expected) != 0 {
			t.Fatal("missing port mappings", expected)
		}
		for _, n := range cluster.Nodes[1:] {
			if len(n.Ports) != 0 {
				t.Fatal("worker should not duplicate host port bindings")
			}
		}
	}
}
func TestKindIngressRejectsInvalidPorts(t *testing.T) {
	for _, args := range [][]string{{"--http-port", "0"}, {"--https-port", "65536"}, {"--http-port", "443"}, {"--http-port", "30880"}, {"--https-port", "30880"}} {
		f := flag.NewFlagSet("kind", flag.ContinueOnError)
		o := clusterFlags(f)
		if err := f.Parse(args); err != nil {
			t.Fatal(err)
		}
		if err := applyClusterConfig(f, o, fileConfig{}); err == nil {
			t.Fatalf("accepted invalid or conflicting ports %v", args)
		}
	}
}
