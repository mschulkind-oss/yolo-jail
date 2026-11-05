package brokerstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestPackCallWritesBindExactDigestAndPendingJoin(t *testing.T) {
	legacy := spendOrFileRequest()
	legacyJSON, err := json.Marshal([]any{legacy.Argv, legacy.Repo, legacy.StdinDigest})
	if err != nil {
		t.Fatal(err)
	}
	legacyHash := sha256.Sum256(legacyJSON)
	if CommandDigest(legacy) != hex.EncodeToString(legacyHash[:]) {
		t.Fatal("git/GitHub digest changed")
	}
	request := legacy
	request.CallWrites = []CallWrite{{Cell: "update", Chain: []CallResource{{Type: "task", ID: "a1"}, {Type: "project", ID: "team:alpha"}}}}
	changed := request
	changed.CallWrites = []CallWrite{{Cell: "update", Chain: []CallResource{{Type: "task", ID: "a1"}, {Type: "project", ID: "team:beta"}}}}
	if CommandDigest(request) == CommandDigest(changed) || CommandDigest(request) == CommandDigest(spendOrFileRequest()) {
		t.Fatal("resource chain was not bound into the exact command digest")
	}
	s := &Store{Dir: t.TempDir(), Source: "local-tasks"}
	first, err := s.File(request)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.File(request)
	if err != nil || joined.ID != first.ID {
		t.Fatalf("exact call failed to join pending request: %+v %v", joined, err)
	}
	other, err := s.File(changed)
	if err != nil || other.ID == first.ID || other.Digest == first.Digest {
		t.Fatalf("changed parent joined pending request: %+v %v", other, err)
	}
}
