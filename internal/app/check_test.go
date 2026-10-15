package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fakeEtcdCheck() {
	scenario := os.Getenv("OPSKIT_FAKE_CHECK")
	args := strings.Join(os.Args[1:], " ")
	if !strings.Contains(args, "--write-out=json") || !strings.Contains(args, "--hex=false") {
		os.Exit(10)
	}
	if strings.Contains(args, "member list") {
		if scenario == "member-fail" {
			os.Exit(2)
		}
		if scenario == "malformed" {
			fmt.Print("not-json")
			return
		}
		if scenario == "timeout" {
			time.Sleep(time.Second)
			return
		}
		data := `{"header":{"cluster_id":99},"members":[{"ID":9007199254740993,"name":"node1","clientURLs":["https://node1:2379"],"peerURLs":["https://node1:2380"]},{"ID":2,"name":"node2","clientURLs":["https://node2:2379"]},{"ID":3,"name":"node3","clientURLs":["https://node3:2379"]},{"ID":4,"name":"learner","isLearner":true,"clientURLs":[]}]}`
		if scenario == "healthy" {
			data = strings.Replace(data, `"clientURLs":[]`, `"clientURLs":["https://learner:2379"]`, 1)
		}
		fmt.Print(data)
		return
	}
	id := uint64(9007199254740993)
	if strings.Contains(args, "https://node2:2379") {
		id = 2
	}
	if strings.Contains(args, "https://node3:2379") {
		id = 3
	}
	if strings.Contains(args, "https://learner:2379") {
		id = 4
	}
	if scenario == "degraded" && id == 3 {
		os.Exit(2)
	}
	if strings.Contains(args, "endpoint status") {
		if scenario == "wrong-member" {
			id = 7
		}
		fmt.Printf(`[{"Endpoint":"test","Status":{"header":{"cluster_id":99,"member_id":%d,"revision":42},"version":"3.6.0","dbSize":1024,"leader":9007199254740993,"raftTerm":3}}]`, id)
		return
	}
	if strings.Contains(args, "endpoint health") {
		fmt.Print(`[{"endpoint":"test","health":true}]`)
		return
	}
	os.Exit(11)
}
func TestEtcdCheckMembersAndQuorum(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	for _, scenario := range []string{"healthy", "ok", "degraded", "wrong-member", "member-fail", "malformed", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("OPSKIT_FAKE_CHECK", scenario)
			var output bytes.Buffer
			probe := 3 * time.Second
			if scenario == "timeout" {
				probe = 50 * time.Millisecond
			}
			code, err := checkEtcd(context.Background(), exe, "https://seed:2379", "ca", "cert", "key", probe, &output)
			var result etcdCheckResult
			if e := json.Unmarshal(output.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
			if scenario == "healthy" {
				if code != 0 || err != nil || !result.Healthy || result.HealthyMembers != 4 {
					t.Fatalf("healthy cluster failed: %d %v %+v", code, err, result)
				}
			} else if code == 0 || err == nil {
				t.Fatal("unhealthy or undiscovered members reported healthy")
			}
			if scenario == "member-fail" || scenario == "malformed" || scenario == "timeout" {
				if result.Error == "" || result.Healthy {
					t.Fatal("discovery failure hidden")
				}
				return
			}
			if result.Members != 4 || result.Voters != 3 || result.Learners != 1 || result.QuorumRequired != 2 {
				t.Fatalf("wrong counts: %+v", result)
			}
			expected := 3
			if scenario == "degraded" {
				expected = 2
			}
			if scenario == "wrong-member" {
				expected = 0
			}
			if result.HealthyVoters != expected || result.QuorumObserved != (expected >= 2) {
				t.Fatalf("wrong quorum: %+v", result)
			}
			if result.Nodes[0].ID != "20000000000001" {
				t.Fatal("member ID lost precision")
			}
			if scenario == "ok" && (!result.Nodes[0].Leader || result.Nodes[0].Revision != 42 || result.Nodes[0].DBSize != 1024) {
				t.Fatal("status fields missing")
			}
		})
	}
}
func TestS3CheckHTTPStatuses(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	for _, status := range []int{200, 403, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "HEAD" {
					t.Error("S3 check must only HEAD")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			s := storage{bucket: "test", endpoint: server.URL, region: "us-east-1", pathStyle: true}
			var out bytes.Buffer
			code, err := s.check(context.Background(), &out)
			var result s3CheckResult
			if e := json.Unmarshal(out.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
			if !result.Reachable || result.HTTPStatus != status || result.WriteAccess != "not-tested" {
				t.Fatalf("%+v", result)
			}
			if status == 200 {
				if code != 0 || err != nil || result.BucketAccess != "ok" {
					t.Fatal(code, err)
				}
			} else if code == 0 || err == nil {
				t.Fatal("HTTP error reported as successful")
			}
			if calls != 1 {
				t.Fatal("unexpected requests", calls)
			}
		})
	}
}
func TestS3CheckObjectFailureAndTimeout(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing") {
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	s := storage{bucket: "test", key: "missing", endpoint: server.URL, region: "us-east-1", pathStyle: true}
	var out bytes.Buffer
	code, err := s.check(context.Background(), &out)
	var result s3CheckResult
	json.Unmarshal(out.Bytes(), &result)
	if code != 1 || err == nil || result.BucketAccess != "ok" || result.ObjectAccess != "not-found-or-hidden" {
		t.Fatalf("%d %v %+v", code, err, result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out.Reset()
	code, err = s.check(ctx, &out)
	if code != 4 || err == nil {
		t.Fatalf("%d %v", code, err)
	}
}

func TestRunChecksReuseConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPSKIT_FAKE_CHECK", "healthy")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	// An unfinished S3 configuration must not block etcd health checks.
	body := "schema_version: 1\ns3:\n  provider: minio\n  minio:\n    bucket: pending\n    access_key: unfinished\n"
	if err = os.WriteFile("opskit.yaml", []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := Run(context.Background(), []string{"etcd", "check", "--etcdctl", exe}, &out, &out, "test")
	if code != 0 || err != nil {
		t.Fatalf("%d %v %s", code, err, out.String())
	}
	// S3 check uses the selected provider and requires no etcdctl.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Error(r.Method)
		}
	}))
	defer server.Close()
	body = "schema_version: 1\netcd:\n  etcdctl: does-not-exist\ns3:\n  provider: minio\n  minio:\n    bucket: test\n    endpoint: " + server.URL + "\n    access_key: test\n    secret_key: secret\n"
	if err = os.WriteFile("opskit.yaml", []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	code, err = Run(context.Background(), []string{"s3", "check"}, &out, &out, "test")
	if code != 0 || err != nil {
		t.Fatalf("%d %v %s", code, err, out.String())
	}
}
