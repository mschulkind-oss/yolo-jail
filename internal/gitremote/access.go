package gitremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const maxStatusBytes = 1 << 20
const statusSessionTimeout = 3 * time.Second

// ForwardAccess reads live standing or submits a Git-selected, untrusted push candidate
// to the host verifier. It never performs a push or treats its observed old IDs as authority.
func ForwardAccess(op string, args []string, env Env) ([]byte, int) {
	if op != "status" && op != "evaluate" && op != "ask" {
		fmt.Fprintln(env.Stderr, "yolo access: unsupported git operation; use `yolo access git` or `yolo access -- git push <remote> <source>:<destination>`")
		return nil, exitUsage
	}
	endpoint := env.Getenv(EndpointEnv)
	if endpoint == "" {
		fmt.Fprintln(env.Stderr, "yolo access: no git-broker endpoint serves this jail; ask the human to run `yolo check` on the host, then relaunch")
		return nil, exitUnavailable
	}
	started := time.Now()
	deadline := started.Add(statusSessionTimeout)
	if op != "status" {
		limit := 30 * time.Second
		if env.accessTimeout > 0 && env.accessTimeout < limit {
			limit = env.accessTimeout
		}
		deadline = started.Add(limit)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	body := []byte(`{"access":{"op":"status","version":1}}`)
	if op != "status" {
		proposal, err := prepareAccessProposal(ctx, args)
		if err != nil {
			fmt.Fprintf(env.Stderr, "yolo access: %v\n", err)
			return nil, exitUsage
		}
		body, err = json.Marshal(struct {
			Access struct {
				Op       string                `json:"op"`
				Version  int                   `json:"version"`
				Proposal gitproto.PushProposal `json:"proposal"`
			} `json:"access"`
		}{Access: struct {
			Op       string                `json:"op"`
			Version  int                   `json:"version"`
			Proposal gitproto.PushProposal `json:"proposal"`
		}{op, 1, proposal}})
		if err != nil {
			return nil, exitUnavailable
		}
	}
	conn, err := svcendpoint.DialUntil(endpoint, deadline)
	if err != nil {
		fmt.Fprintf(env.Stderr, "yolo access: cannot reach git-broker: %v; run `yolo check` on the host, then relaunch\n", err)
		return nil, exitUnavailable
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		fmt.Fprintf(env.Stderr, "yolo access: cannot bound git-broker session: %v; relaunch the jail\n", err)
		return nil, exitUnavailable
	}
	// Legacy decoders see no top-level service/URL, even for push proposals.
	if err := frameproto.WriteRequest(conn, body); err != nil {
		fmt.Fprintf(env.Stderr, "yolo access: git-broker stopped answering: %v; relaunch the jail\n", err)
		return nil, exitUnavailable
	}
	var output []byte
	responseBytes := 0
	for {
		remaining := maxStatusBytes - responseBytes
		if remaining < 0 {
			remaining = 0
		}
		frame, err := frameproto.ReadFrameBounded(conn, uint32(remaining))
		if err != nil {
			if errors.Is(err, frameproto.ErrFrameTooLarge) {
				fmt.Fprintln(env.Stderr, "yolo access: git-broker status is too large; run `yolo check` on the host")
			} else {
				fmt.Fprintln(env.Stderr, "yolo access: git-broker stopped answering; relaunch the jail")
			}
			return nil, exitUnavailable
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			responseBytes += len(frame.Payload)
			output = append(output, frame.Payload...)
		case frameproto.StreamStderr:
			responseBytes += len(frame.Payload)
			_, _ = env.Stderr.Write(frame.Payload)
		case frameproto.StreamExit:
			code, err := frameproto.ExitCode(frame.Payload)
			if err != nil || code != 0 || (op == "status" && !validGitStatus(output)) || (op != "status" && !validGitPrediction(output)) {
				kind := "prediction"
				if op == "status" {
					kind = "status"
				}
				fmt.Fprintf(env.Stderr, "yolo access: git-broker returned an unsupported %s; relaunch the jail\n", kind)
				return nil, exitUnavailable
			}
			return output, 0
		default:
			fmt.Fprintln(env.Stderr, "yolo access: git-broker returned an unknown frame; relaunch the jail")
			return nil, exitUnavailable
		}
	}
}

// These pointers distinguish required wire fields from missing or null JSON values. The
// host broker remains outside this package, so status validation follows its public schema
// rather than importing the host-side implementation.
type gitStatusScopeGrant struct {
	Kind *string `json:"kind"`
	Repo *string `json:"repo"`
	Name *string `json:"name"`
}
type gitStatusRequest struct {
	ID      *string         `json:"id"`
	State   *string         `json:"state"`
	Answer  json.RawMessage `json:"answer"`
	Used    json.RawMessage `json:"used"`
	Filed   *time.Time      `json:"filed"`
	Expires *time.Time      `json:"expires"`
}
type gitStatusGrant struct {
	ID       *string    `json:"id"`
	Kind     *string    `json:"kind"`
	Set      *string    `json:"set"`
	Repo     *string    `json:"repo"`
	Resource *string    `json:"resource"`
	Expires  *time.Time `json:"expires"`
	Uses     *int       `json:"uses"`
}
type gitStatusWorking struct {
	ID       *string    `json:"id"`
	Repo     *string    `json:"repo"`
	Kind     *string    `json:"kind"`
	Resource *string    `json:"resource"`
	By       *string    `json:"by"`
	Expires  *time.Time `json:"expires"`
}
type gitStatusLimits struct {
	Pending       *int       `json:"pending"`
	PendingCap    *int       `json:"pending_cap"`
	CooldownUntil *time.Time `json:"cooldown_until"`
}
type gitStatusResponse struct {
	Schema       *string                `json:"schema"`
	Source       *string                `json:"source"`
	State        *string                `json:"state"`
	Read         *string                `json:"read"`
	Next         *string                `json:"next"`
	Repositories *[]string              `json:"repositories"`
	ScopeGrants  *[]gitStatusScopeGrant `json:"scope_grants"`
	Requests     *[]gitStatusRequest    `json:"requests"`
	Grants       *[]gitStatusGrant      `json:"grants"`
	WorkingSet   *[]gitStatusWorking    `json:"working_set"`
	Limits       *gitStatusLimits       `json:"limits"`
}

func validGitStatus(body []byte) bool {
	var status gitStatusResponse
	if json.Unmarshal(body, &status) != nil ||
		status.Schema == nil || *status.Schema != "yolo.access/1" ||
		status.Source == nil || *status.Source != "git" ||
		status.State == nil || status.Read == nil || status.Next == nil ||
		status.Repositories == nil || status.ScopeGrants == nil || status.Requests == nil ||
		status.Grants == nil || status.WorkingSet == nil || status.Limits == nil {
		return false
	}
	switch *status.State {
	case "running":
		if *status.Read != "granted" && *status.Read != "partial" && *status.Read != "not-granted" {
			return false
		}
	case "unreachable":
		if *status.Read != "" && *status.Read != "granted" && *status.Read != "partial" && *status.Read != "not-granted" {
			return false
		}
	default:
		return false
	}
	for _, repo := range *status.Repositories {
		if repo == "" {
			return false
		}
	}
	for _, grant := range *status.ScopeGrants {
		if grant.Kind == nil || *grant.Kind == "" || grant.Repo == nil || *grant.Repo == "" {
			return false
		}
	}
	for _, request := range *status.Requests {
		if request.ID == nil || *request.ID == "" || request.State == nil ||
			!validGitRequestState(*request.State) ||
			request.Filed == nil || request.Filed.IsZero() || request.Expires == nil || request.Expires.IsZero() ||
			!validOptionalString(request.Answer) || !validOptionalBool(request.Used) {
			return false
		}
	}
	for _, grant := range *status.Grants {
		if grant.ID == nil || *grant.ID == "" || grant.Kind == nil ||
			(*grant.Kind != "allow-once" && *grant.Kind != "timed") ||
			grant.Set == nil || *grant.Set == "" || grant.Repo == nil || *grant.Repo == "" ||
			grant.Resource == nil || grant.Expires == nil || grant.Expires.IsZero() ||
			grant.Uses == nil || *grant.Uses < 0 {
			return false
		}
	}
	for _, entry := range *status.WorkingSet {
		if entry.ID == nil || *entry.ID == "" || entry.Repo == nil || *entry.Repo == "" ||
			entry.Kind == nil || *entry.Kind == "" || entry.Resource == nil || *entry.Resource == "" ||
			entry.By == nil || *entry.By == "" {
			return false
		}
	}
	return status.Limits.Pending != nil && *status.Limits.Pending >= 0 &&
		status.Limits.PendingCap != nil && *status.Limits.PendingCap > 0
}

func validOptionalString(raw json.RawMessage) bool {
	if raw == nil {
		return true
	}
	value := strings.TrimSpace(string(raw))
	return len(value) > 0 && value[0] == '"'
}

func validOptionalBool(raw json.RawMessage) bool {
	if raw == nil {
		return true
	}
	switch strings.TrimSpace(string(raw)) {
	case "true", "false":
		return true
	default:
		return false
	}
}

func validGitRequestState(state string) bool {
	switch state {
	case "pending", "allowed", "denied", "expired", "refused":
		return true
	default:
		return false
	}
}
