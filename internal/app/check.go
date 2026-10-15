package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type s3CheckResult struct {
	SchemaVersion int    `json:"schema_version"`
	Provider      string `json:"provider,omitempty"`
	Bucket        string `json:"bucket"`
	Reachable     bool   `json:"service_reachable"`
	BucketAccess  string `json:"bucket_access"`
	ObjectAccess  string `json:"object_access,omitempty"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	ElapsedMS     int64  `json:"elapsed_ms"`
	WriteAccess   string `json:"write_access"`
}

func (s storage) check(ctx context.Context, out io.Writer) (int, error) {
	start := time.Now()
	result := s3CheckResult{SchemaVersion: 1, Provider: s.provider, Bucket: s.bucket, BucketAccess: "not-checked", WriteAccess: "not-tested"}
	client, err := s.client(ctx)
	if err == nil {
		_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
		if err == nil {
			result.Reachable = true
			result.BucketAccess = "ok"
			result.HTTPStatus = 200
		} else {
			result.BucketAccess = "failed"
		}
	}
	if err == nil && s.key != "" {
		_, err = client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key)})
		if err == nil {
			result.ObjectAccess = "ok"
		} else {
			result.ObjectAccess = "failed"
		}
	}
	if err != nil {
		status := "connection-or-credentials-error"
		var response *smithyhttp.ResponseError
		if errors.As(err, &response) {
			result.Reachable = true
			result.HTTPStatus = response.HTTPStatusCode()
			switch result.HTTPStatus {
			case 403:
				status = "access-denied"
			case 404:
				status = "not-found-or-hidden"
			default:
				status = "http-error"
			}
		}
		if ctx.Err() != nil {
			status = "timeout-or-cancelled"
		}
		if result.ObjectAccess == "failed" {
			result.ObjectAccess = status
		} else {
			result.BucketAccess = status
		}
	}
	result.ElapsedMS = time.Since(start).Milliseconds()
	if encodeErr := writeCheck(out, result); encodeErr != nil {
		return 1, encodeErr
	}
	if err != nil {
		if ctx.Err() != nil {
			return 4, ctx.Err()
		}
		return 1, errors.New("S3 check failed; inspect JSON status (HEAD does not test write permission)")
	}
	return 0, nil
}

type etcdMember struct {
	ID         uint64   `json:"ID"`
	Name       string   `json:"name"`
	PeerURLs   []string `json:"peerURLs"`
	ClientURLs []string `json:"clientURLs"`
	IsLearner  bool     `json:"isLearner"`
}
type etcdNodeResult struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	PeerURLs   []string `json:"peer_urls"`
	ClientURLs []string `json:"client_urls"`
	Learner    bool     `json:"learner"`
	Healthy    bool     `json:"healthy"`
	Endpoint   string   `json:"checked_endpoint,omitempty"`
	Leader     bool     `json:"leader"`
	Version    string   `json:"version,omitempty"`
	DBSize     int64    `json:"db_size_bytes"`
	Revision   int64    `json:"revision"`
	RaftTerm   uint64   `json:"raft_term"`
	Error      string   `json:"error,omitempty"`
}
type etcdCheckResult struct {
	SchemaVersion  int              `json:"schema_version"`
	Members        int              `json:"member_count"`
	Voters         int              `json:"voter_count"`
	Learners       int              `json:"learner_count"`
	HealthyMembers int              `json:"healthy_members"`
	HealthyVoters  int              `json:"healthy_voters"`
	QuorumRequired int              `json:"quorum_required"`
	QuorumObserved bool             `json:"quorum_observed"`
	Healthy        bool             `json:"healthy"`
	Nodes          []etcdNodeResult `json:"nodes"`
	Error          string           `json:"error,omitempty"`
}
type endpointStatus struct {
	Status struct {
		Header struct {
			ClusterID uint64 `json:"cluster_id"`
			MemberID  uint64 `json:"member_id"`
			Revision  int64  `json:"revision"`
		} `json:"header"`
		Version  string   `json:"version"`
		DBSize   int64    `json:"dbSize"`
		Leader   uint64   `json:"leader"`
		RaftTerm uint64   `json:"raftTerm"`
		Errors   []string `json:"errors"`
	} `json:"Status"`
}

func etcdJSON(ctx context.Context, binary string, base, args []string, target any) error {
	all := append(append([]string{}, base...), "--write-out=json", "--hex=false")
	all = append(all, args...)
	// Do not copy stderr into structured results: it may contain connection details.
	data, err := exec.CommandContext(ctx, binary, all...).Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errors.New("etcdctl command failed (check connectivity, TLS and permissions)")
	}
	if err = json.Unmarshal(data, target); err != nil {
		return errors.New("invalid etcdctl JSON response")
	}
	return nil
}
func etcdArgs(endpoint, ca, cert, key string) []string {
	args := []string{}
	for _, v := range [][2]string{{"--endpoints", endpoint}, {"--cacert", ca}, {"--cert", cert}, {"--key", key}} {
		if v[1] != "" {
			args = append(args, v[0], v[1])
		}
	}
	return args
}
func checkEtcd(ctx context.Context, binary, endpoint, ca, cert, key string, probeTimeout time.Duration, out io.Writer) (int, error) {
	var members struct {
		Header struct {
			ClusterID uint64 `json:"cluster_id"`
		} `json:"header"`
		Members []etcdMember `json:"members"`
	}
	result := etcdCheckResult{SchemaVersion: 1, Nodes: []etcdNodeResult{}}
	memberCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	err := etcdJSON(memberCtx, binary, etcdArgs(endpoint, ca, cert, key), []string{"member", "list"}, &members)
	cancel()
	if err != nil {
		result.Error = err.Error()
	} else if len(members.Members) == 0 || members.Header.ClusterID == 0 {
		result.Error = "member list returned no members or cluster ID"
	}
	if result.Error != "" {
		if e := writeCheck(out, result); e != nil {
			return 1, e
		}
		if ctx.Err() != nil {
			return 4, ctx.Err()
		}
		return 1, errors.New(result.Error)
	}
	for _, member := range members.Members {
		node := etcdNodeResult{ID: strconv.FormatUint(member.ID, 16), Name: member.Name, PeerURLs: member.PeerURLs, ClientURLs: member.ClientURLs, Learner: member.IsLearner}
		result.Members++
		if member.IsLearner {
			result.Learners++
		} else {
			result.Voters++
		}
		nodeCtx, stop := context.WithTimeout(ctx, probeTimeout)
		for _, url := range member.ClientURLs {
			node.Endpoint = url
			base := etcdArgs(url, ca, cert, key)
			var statuses []endpointStatus
			err = etcdJSON(nodeCtx, binary, base, []string{"endpoint", "status"}, &statuses)
			if err != nil {
				node.Error = err.Error()
				continue
			}
			if len(statuses) != 1 {
				node.Error = "expected one endpoint status"
				continue
			}
			status := statuses[0].Status
			if status.Header.MemberID != member.ID || status.Header.ClusterID != members.Header.ClusterID {
				node.Error = "endpoint belongs to a different member or cluster"
				continue
			}
			node.Version = status.Version
			node.DBSize = status.DBSize
			node.Revision = status.Header.Revision
			node.RaftTerm = status.RaftTerm
			node.Leader = status.Leader == member.ID
			if status.Leader == 0 || len(status.Errors) > 0 {
				node.Error = "endpoint reports no leader or status errors"
				continue
			}
			var health []struct {
				Health bool `json:"health"`
			}
			if err = etcdJSON(nodeCtx, binary, base, []string{"endpoint", "health"}, &health); err != nil {
				node.Error = err.Error()
				continue
			}
			if len(health) != 1 || !health[0].Health {
				node.Error = "endpoint health probe failed"
				continue
			}
			node.Healthy = true
			node.Error = ""
			break
		}
		stop()
		if len(member.ClientURLs) == 0 {
			node.Error = "member has no advertised client URL (possibly unstarted)"
		}
		if node.Healthy {
			result.HealthyMembers++
			if !member.IsLearner {
				result.HealthyVoters++
			}
		}
		result.Nodes = append(result.Nodes, node)
	}
	result.QuorumRequired = result.Voters/2 + 1
	result.QuorumObserved = result.Voters > 0 && result.HealthyVoters >= result.QuorumRequired
	result.Healthy = result.QuorumObserved && result.HealthyMembers == result.Members
	if err = writeCheck(out, result); err != nil {
		return 1, err
	}
	if ctx.Err() != nil {
		return 4, ctx.Err()
	}
	if !result.Healthy {
		return 1, fmt.Errorf("etcd check: %d/%d members healthy; observed voting quorum=%v", result.HealthyMembers, result.Members, result.QuorumObserved)
	}
	return 0, nil
}

func writeCheck(out io.Writer, result any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
