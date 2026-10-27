package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDiagnosePodLonghornMismatch(t *testing.T) {
	responses := map[string]string{
		"-n uat get pod redis-cluster-5 -o json": `{"spec":{"nodeName":"k8swork6","volumes":[{"persistentVolumeClaim":{"claimName":"redis-data-redis-cluster-5"}}]},"status":{"phase":"Pending","containerStatuses":[{"name":"redis","ready":false,"state":{"waiting":{"reason":"ContainerCreating"}}}]}}`,
		"get volumeattachments -o json": `{"items":[{"spec":{"source":{"persistentVolumeName":"pvc-test"},"nodeName":"node05"},"status":{"attached":true}}]}`,
		"-n uat get pvc redis-data-redis-cluster-5 -o json": `{"spec":{"volumeName":"pvc-test","storageClassName":"longhorn","accessModes":["ReadWriteOnce"]},"status":{"phase":"Bound"}}`,
		"-n longhorn-system get volumes.longhorn.io pvc-test -o json": `{"status":{"state":"attached","robustness":"healthy","currentNodeID":"node05"}}`,
	}
	read := func(args ...string) ([]byte,error) {
		key := strings.Join(args, " ")
		if data, ok := responses[key]; ok { return []byte(data), nil }
		t.Fatalf("unexpected kubectl request: %s", key)
		return nil, errors.New("unexpected request")
	}
	report,err := diagnosePod(context.Background(),read,"uat","redis-cluster-5")
	if err != nil { t.Fatal(err) }
	if report.Node != "k8swork6" || report.Volumes[0].Attachments[0].Node != "node05" ||
		report.Volumes[0].Longhorn.CurrentNode != "node05" {
		t.Fatalf("missing volume node mismatch: %+v",report)
	}
	if len(report.Findings)<2 || len(report.Warnings)!=0 {
		t.Fatalf("unexpected findings/warnings: %+v",report)
	}
	if raw,err:=json.Marshal(report);err!=nil || !json.Valid(raw){t.Fatal("invalid JSON",err)}
}

func TestDiagnosePodForbiddenReadsDoNotCountAsHealthy(t *testing.T) {
	read:=func(args ...string)([]byte,error){
		switch strings.Join(args," ") {
		case "-n uat get pod p -o json":
			return []byte(`{"spec":{"nodeName":"node05","volumes":[{"persistentVolumeClaim":{"claimName":"data"}}]},"status":{"phase":"Running","containerStatuses":[{"name":"redis","ready":true}]}}`),nil
		case "-n uat get pvc data -o json":
			return []byte(`{"spec":{"volumeName":"pvc-test","storageClassName":"longhorn"},"status":{"phase":"Bound"}}`),nil
		default:
			return nil,errors.New("forbidden")
		}
	}
	report,err:=diagnosePod(context.Background(),read,"uat","p")
	if err!=nil {t.Fatal(err)}
	if len(report.Warnings)!=2 {t.Fatalf("missing RBAC warnings: %+v",report)}
}

func TestDiagnosePodNoPVCDoesNotReadAttachments(t *testing.T) {
	read:=func(args ...string)([]byte,error){
		if strings.Join(args," ") != "-n default get pod p -o json" {
			t.Fatalf("unexpected extra read: %v",args)
		}
		return []byte(`{"spec":{"nodeName":"n"},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":true}]}}`),nil
	}
	report,err:=diagnosePod(context.Background(),read,"default","p")
	if err!=nil || len(report.Volumes)!=0 || len(report.Warnings)!=0 || len(report.Findings)!=0{
		t.Fatalf("unexpected: %v %+v",err,report)
	}
}
