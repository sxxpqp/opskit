package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// podDiagnosis contains only sanitized status fields, not Pod environment or Secret values.
type podDiagnosis struct {
	SchemaVersion int                `json:"schema_version"`
	Namespace     string             `json:"namespace"`
	Pod           string             `json:"pod"`
	Node          string             `json:"node,omitempty"`
	Phase         string             `json:"phase,omitempty"`
	Containers    []containerDiagnosis `json:"containers"`
	Volumes       []volumeDiagnosis   `json:"volumes"`
	Findings      []string           `json:"findings"`
	Warnings      []string           `json:"warnings"`
}

type containerDiagnosis struct {
	Name string `json:"name"`
	Ready bool `json:"ready"`
	WaitingReason string `json:"waiting_reason,omitempty"`
}

type volumeDiagnosis struct {
	Claim string `json:"claim"`
	PVCPhase string `json:"pvc_phase,omitempty"`
	PV string `json:"pv,omitempty"`
	StorageClass string `json:"storage_class,omitempty"`
	AccessModes []string `json:"access_modes,omitempty"`
	Attachments []attachmentDiagnosis `json:"attachments,omitempty"`
	Longhorn *longhornDiagnosis `json:"longhorn,omitempty"`
}

type attachmentDiagnosis struct {
	Node string `json:"node"`
	Attached bool `json:"attached"`
}

type longhornDiagnosis struct {
	State string `json:"state,omitempty"`
	Robustness string `json:"robustness,omitempty"`
	CurrentNode string `json:"current_node,omitempty"`
}

type kubeRead func(...string) ([]byte, error)

func newK8sDiagnoseCommand(parent context.Context, out io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use: "diagnose POD",
		Short: "只读检查 Pod 容器状态和 PVC / Longhorn 挂载差异",
		Long: "只读取 Pod、PVC、VolumeAttachment 和 Longhorn 状态；不执行删除、重启或卸载。部分 RBAC 权限缺失会报告 warnings。",
		Args: cobra.ExactArgs(1),
	}
	f := cmd.Flags()
	config := f.String("config", defaultConfigPath, "统一 YAML 配置")
	namespace := f.StringP("namespace", "n", "default", "Pod Namespace")
	kubeconfig := f.String("kubeconfig", "", "指定 kubeconfig")
	clusterContext := f.String("context", "", "指定 Kubernetes context")
	timeout := f.Duration("timeout", time.Minute, "整体诊断超时")
	bindCommand(cmd, func() (int, error) {
		if *timeout <= 0 || strings.TrimSpace(*namespace) == "" || f.NArg() != 1 {
			return 2, errors.New("需要 POD、有效的 Namespace 和正数 timeout")
		}
		if _, err := commandConfig(f, *config); err != nil { return 2, err }
		ctx, cancel := context.WithTimeout(parent, *timeout)
		defer cancel()
		// Keep stdout machine-readable; selectedKubectl normally prints the context.
		kube, err := selectedKubectl(ctx, *kubeconfig, *clusterContext, io.Discard)
		if err != nil { return 3, err }
		report, err := diagnosePod(ctx, kube, *namespace, f.Arg(0))
		if err != nil { return taskError(ctx, err) }
		if err := json.NewEncoder(out).Encode(report); err != nil { return 1, err }
		if len(report.Findings) > 0 || len(report.Warnings) > 0 {
			return 1, errors.New("诊断发现问题或证据不完整；查看 JSON findings/warnings")
		}
		return 0, nil
	})
	return cmd, nil
}

func diagnosePod(ctx context.Context, kube kubeRead, namespace, name string) (podDiagnosis, error) {
	report := podDiagnosis{SchemaVersion: 1, Namespace: namespace, Pod: name,
		Containers: []containerDiagnosis{}, Volumes: []volumeDiagnosis{},
		Findings: []string{}, Warnings: []string{}}
	var pod struct {
		Spec struct {
			NodeName string `json:"nodeName"`
			Volumes []struct {
				PersistentVolumeClaim *struct { ClaimName string `json:"claimName"` } `json:"persistentVolumeClaim"`
			} `json:"volumes"`
		} `json:"spec"`
		Status struct {
			Phase string `json:"phase"`
			ContainerStatuses []struct {
				Name string `json:"name"`
				Ready bool `json:"ready"`
				State struct { Waiting *struct { Reason string `json:"reason"` } `json:"waiting"` } `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	}
	raw, err := kube("-n", namespace, "get", "pod", name, "-o", "json")
	if err != nil { return report, fmt.Errorf("无法读取 Pod（检查名称/权限/连接）: %w", err) }
	if json.Unmarshal(raw, &pod) != nil { return report, errors.New("Pod JSON 无法解析") }
	report.Node, report.Phase = pod.Spec.NodeName, pod.Status.Phase
	if report.Phase != "Running" { report.Findings = append(report.Findings, "Pod 未处于 Running: "+report.Phase) }
	if report.Node == "" { report.Findings = append(report.Findings, "Pod 尚未调度到节点") }
	for _, c := range pod.Status.ContainerStatuses {
		item := containerDiagnosis{Name: c.Name, Ready: c.Ready}
		if c.State.Waiting != nil { item.WaitingReason = c.State.Waiting.Reason }
		report.Containers = append(report.Containers, item)
		if item.WaitingReason != "" { report.Findings = append(report.Findings, "容器 "+item.Name+" 等待: "+item.WaitingReason)
		} else if !item.Ready { report.Findings = append(report.Findings, "容器 "+item.Name+" 尚未 Ready") }
	}
	claims := map[string]bool{}
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName != "" {
			claims[v.PersistentVolumeClaim.ClaimName] = true
		}
	}
	var attachments struct {
		Items []struct {
			Spec struct {
				Source struct { PersistentVolumeName string `json:"persistentVolumeName"` } `json:"source"`
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct { Attached bool `json:"attached"` } `json:"status"`
		} `json:"items"`
	}
	if len(claims) > 0 {
		raw, err = kube("get", "volumeattachments", "-o", "json")
		if err != nil { report.Warnings = append(report.Warnings, "无法读取 VolumeAttachment：诊断不完整")
		} else if json.Unmarshal(raw, &attachments) != nil {
			report.Warnings = append(report.Warnings, "VolumeAttachment JSON 无法解析")
		}
	}
	names := make([]string, 0, len(claims))
	for claim := range claims { names = append(names, claim) }
	sort.Strings(names)
	for _, claim := range names {
		vol := volumeDiagnosis{Claim: claim, Attachments: []attachmentDiagnosis{}}
		var pvc struct {
			Spec struct {
				VolumeName string `json:"volumeName"`
				StorageClassName string `json:"storageClassName"`
				AccessModes []string `json:"accessModes"`
			} `json:"spec"`
			Status struct { Phase string `json:"phase"` } `json:"status"`
		}
		raw, err = kube("-n", namespace, "get", "pvc", claim, "-o", "json")
		if err != nil { report.Warnings = append(report.Warnings, "无法读取 PVC "+claim)
		} else if json.Unmarshal(raw, &pvc) != nil {
			report.Warnings = append(report.Warnings, "PVC "+claim+" JSON 无法解析")
		} else {
			vol.PVCPhase, vol.PV, vol.StorageClass, vol.AccessModes =
				pvc.Status.Phase, pvc.Spec.VolumeName, pvc.Spec.StorageClassName, pvc.Spec.AccessModes
			if vol.PVCPhase != "Bound" {
				report.Findings = append(report.Findings, "PVC "+claim+" 未绑定: "+vol.PVCPhase)
			}
			for _, a := range attachments.Items {
				if vol.PV != "" && a.Spec.Source.PersistentVolumeName == vol.PV {
					vol.Attachments = append(vol.Attachments, attachmentDiagnosis{Node: a.Spec.NodeName, Attached: a.Status.Attached})
					if a.Status.Attached && report.Node != "" && a.Spec.NodeName != report.Node {
						report.Findings = append(report.Findings,
							"CSI 卷节点不一致：PVC "+claim+" 附加到 "+a.Spec.NodeName+"，Pod 在 "+report.Node+"；需先确认旧节点有无活跃使用者")
					}
				}
			}
			if vol.PV != "" && strings.Contains(strings.ToLower(vol.StorageClass), "longhorn") {
				raw, err = kube("-n", "longhorn-system", "get", "volumes.longhorn.io", vol.PV, "-o", "json")
				if err != nil { report.Warnings = append(report.Warnings, "无法读取 Longhorn 卷 "+vol.PV)
				} else {
					var lh struct { Status struct {
						State string `json:"state"`
						Robustness string `json:"robustness"`
						CurrentNodeID string `json:"currentNodeID"`
					} `json:"status"` }
					if json.Unmarshal(raw, &lh) != nil {
						report.Warnings = append(report.Warnings, "Longhorn JSON 无法解析")
					} else {
						vol.Longhorn = &longhornDiagnosis{State: lh.Status.State, Robustness: lh.Status.Robustness, CurrentNode: lh.Status.CurrentNodeID}
						if lh.Status.State == "attached" && report.Node != "" &&
							lh.Status.CurrentNodeID != "" && lh.Status.CurrentNodeID != report.Node {
							report.Findings = append(report.Findings, "Longhorn "+vol.PV+" 当前挂载在 "+lh.Status.CurrentNodeID+"，与 Pod 节点 "+report.Node+" 不一致")
						}
						if lh.Status.Robustness != "" && lh.Status.Robustness != "healthy" {
							report.Findings = append(report.Findings, "Longhorn "+vol.PV+" 状态为 "+lh.Status.Robustness)
						}
					}
				}
			}
		}
		report.Volumes = append(report.Volumes, vol)
	}
	if ctx.Err() != nil { return report, ctx.Err() }
	return report, nil
}
