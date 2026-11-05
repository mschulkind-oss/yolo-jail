package brokerstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHTTPApprovalRecordBindsHashesAndCannotArchiveOriginalBody(t *testing.T) {
	q := Request{Argv: []string{"http"}, Repo: "fixture", Set: "fixture", Jail: "launch", Start: "start", Workspace: "/fixture",
		HTTPOriginalDigest: strings.Repeat("a", 64), HTTPCanonicalDigest: strings.Repeat("b", 64), HTTPDeclarationHash: strings.Repeat("c", 64),
		StdinDigest: strings.Repeat("a", 64), CallWrites: []CallWrite{{Cell: "rename", Chain: []CallResource{{Type: "task", ID: "a1"}, {Type: "project", ID: "alpha"}}}}}
	q.Policy = Policy{Window: 15 * time.Minute, MaxWindow: 15 * time.Minute, Uses: 1}
	if err := checkFiling(q); err != nil {
		t.Fatal(err)
	}
	original := CommandDigest(q)
	q.HTTPCanonicalDigest = strings.Repeat("d", 64)
	if next := CommandDigest(q); next == original {
		t.Fatal("canonical HTTP bytes are not bound to approval digest")
	}
	q.HTTPCanonicalDigest = strings.Repeat("b", 64)
	q.CallWrites[0].Chain[1].ID = "beta"
	if next := CommandDigest(q); next == original {
		t.Fatal("native parent is not bound to approval digest")
	}
	q.OriginalArgv = []string{"private-body-sentinel"}
	if err := checkFiling(q); err == nil {
		t.Fatal("HTTP approval accepted replayable original bytes")
	}
	q.OriginalArgv = nil
	payload, err := json.Marshal(q)
	if err != nil || strings.Contains(string(payload), "private-body-sentinel") || strings.Contains(string(payload), "body_base64") {
		t.Fatalf("HTTP approval record contains original body: err=%v", err)
	}
}
