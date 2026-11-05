package gitremote

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Prediction is the host's reply, never a client-side policy evaluation.
type Prediction struct {
	Schema            string `json:"schema"`
	Source            string `json:"source"`
	SourceStatus      string `json:"source_status"`
	SourceError       string `json:"source_error"`
	CommandDigest     string `json:"command_digest"`
	CallUnavailable   string `json:"call_unavailable"`
	ScopeSource       string `json:"scope_source"`
	ScopeVerified     *bool  `json:"scope_verified"`
	EligibilityStatus string `json:"eligibility_status"`
	CoverageStatus    string `json:"coverage_status"`
	Call              struct {
		Source    string   `json:"source"`
		Canonical []string `json:"canonical"`
		Digest    string   `json:"digest"`
	} `json:"call"`
	Classified []struct {
		Ref       string `json:"ref"`
		Old       string `json:"old"`
		New       string `json:"new"`
		CoveredBy string `json:"covered_by"`
	} `json:"classified"`
	Verdict   string `json:"verdict"`
	Rule      string `json:"rule"`
	CoveredBy string `json:"covered_by"`
	Next      []struct {
		Who    string `json:"who"`
		Where  string `json:"where"`
		Run    string `json:"run"`
		Detail string `json:"detail"`
	} `json:"next"`
	RequestID     string     `json:"request_id"`
	ReliesOnUntil *time.Time `json:"relies_on_until"`
	InputsDigest  string     `json:"inputs_digest"`
}

func validGitPrediction(body []byte) bool {
	var p Prediction
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || p.Schema != "yolo.access.prediction/1" || p.Source != "git" || p.Rule == "" || p.InputsDigest == "" {
		return false
	}
	if p.Verdict != "runs" && p.Verdict != "asks" && p.Verdict != "refused" {
		return false
	}
	if p.SourceStatus != "not-read" && p.SourceStatus != "unavailable" && p.SourceStatus != "verified" {
		return false
	}
	if p.SourceStatus == "unavailable" && p.SourceError == "" {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields["next"]) == 0 || bytes.Equal(bytes.TrimSpace(fields["next"]), []byte("null")) {
		return false
	}
	if raw, ok := fields["call"]; ok {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || p.Call.Source != "git" || p.Call.Digest == "" || len(p.Call.Canonical) == 0 || p.CallUnavailable != "" {
			return false
		}
	} else if p.CallUnavailable == "" || p.Verdict != "refused" || p.SourceStatus == "verified" {
		return false
	}
	if raw, ok := fields["scope_verified"]; ok && (bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || p.ScopeVerified == nil) {
		return false
	}
	if p.ScopeVerified != nil && *p.ScopeVerified && p.ScopeSource == "" {
		return false
	}
	if p.Verdict != "refused" && (p.SourceStatus != "verified" || p.ScopeVerified == nil || !*p.ScopeVerified || p.Call.Digest == "" || len(p.Classified) == 0 || p.CoverageStatus == "" || p.EligibilityStatus == "" || p.CallUnavailable != "") {
		return false
	}
	if p.CommandDigest != "" && (p.Call.Digest == "" || p.CommandDigest != p.Call.Digest) {
		return false
	}
	if len(p.Classified) != 0 {
		if p.SourceStatus != "verified" || p.Call.Canonical[0] != "git-receive-pack" || len(p.Call.Canonical) < len(p.Classified)+1 {
			return false
		}
		seen := make(map[string]bool, len(p.Classified))
		for i, ref := range p.Classified {
			if ref.Ref == "" || seen[ref.Ref] || !objectID(ref.Old) || !objectID(ref.New) || len(ref.Old) != len(ref.New) || p.Call.Canonical[i+1] != ref.Old+" "+ref.New+" "+ref.Ref || p.Verdict == "runs" && ref.CoveredBy == "" {
				return false
			}
			seen[ref.Ref] = true
		}
		for _, opt := range p.Call.Canonical[len(p.Classified)+1:] {
			if !strings.HasPrefix(opt, "push-option=") {
				return false
			}
		}
	}
	if p.Verdict == "runs" && p.CoverageStatus != "covered" || p.Verdict == "asks" && p.CoverageStatus != "needs-approval" || p.SourceStatus != "verified" && (len(p.Classified) != 0 || p.CoverageStatus != "" || p.ReliesOnUntil != nil || p.CommandDigest != "") {
		return false
	}
	if p.EligibilityStatus == "exact-grant" && p.SourceStatus != "verified" {
		return false
	}
	if raw, ok := fields["classified"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	if p.SourceStatus == "verified" && p.Verdict != "refused" && len(p.Classified) == 0 {
		return false
	}
	if p.RequestID != "" && (p.Verdict != "asks" || !validPredictionRequestID(p.RequestID)) {
		return false
	}
	if p.ScopeSource != "" && p.ScopeSource != "launch" && p.ScopeSource != "live-host" {
		return false
	}
	if p.EligibilityStatus != "" && p.EligibilityStatus != "unavailable" && p.EligibilityStatus != "not-covered" && p.EligibilityStatus != "exact-grant" {
		return false
	}
	if p.CoverageStatus != "" && p.CoverageStatus != "covered" && p.CoverageStatus != "needs-approval" {
		return false
	}
	if p.ReliesOnUntil != nil && p.ReliesOnUntil.IsZero() {
		return false
	}
	for _, n := range p.Next {
		if (n.Who != "agent" && n.Who != "human") || (n.Where != "jail" && n.Where != "host") || (n.Run == "" && n.Detail == "") || (n.Run != "" && n.Detail != "") || (n.Run != "" && !validPredictionNextCommand(n.Run, n.Who, n.Where)) {
			return false
		}
	}
	return true
}

func validPredictionRequestID(id string) bool {
	if !strings.HasPrefix(id, "r-") || len(id) < 3 {
		return false
	}
	for _, c := range id[2:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validPredictionNextCommand(run, who, where string) bool {
	parts := strings.Fields(run)
	if len(parts) != 3 || parts[0] != "yolo" || !validPredictionRequestID(parts[2]) {
		return false
	}
	return (parts[1] == "approve" && who == "human" && where == "host") || (parts[1] == "wait" && who == "agent" && where == "jail")
}
