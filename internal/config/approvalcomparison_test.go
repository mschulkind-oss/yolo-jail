package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApprovalRecordComparisonReadOnlyCases(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName),
		[]byte("[install]\nnix = [\"jq\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	missing, err := CompareApprovalRecord(workspace)
	if err != nil || missing.HasRecord || missing.Current == "" || missing.RecordPath != ApprovalSnapshotPath(workspace) {
		t.Fatalf("missing-record comparison = %+v, err=%v", missing, err)
	}
	if _, err := os.Stat(filepath.Dir(missing.RecordPath)); !os.IsNotExist(err) {
		t.Fatalf("comparison created approval directory (stat error %v)", err)
	}

	if err := os.MkdirAll(filepath.Dir(missing.RecordPath), 0o755); err != nil {
		t.Fatal(err)
	}
	malformed := "not JSON\n"
	if err := os.WriteFile(missing.RecordPath, []byte(malformed), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := CompareApprovalRecord(workspace)
	if err != nil || !got.HasRecord || !got.Malformed || got.Equal || len(got.DiffLines) == 0 {
		t.Fatalf("malformed-record comparison = %+v, err=%v", got, err)
	}
	if stored, err := os.ReadFile(missing.RecordPath); err != nil || string(stored) != malformed {
		t.Fatalf("comparison repaired malformed record: bytes=%q err=%v", stored, err)
	}

	if err := os.WriteFile(missing.RecordPath, []byte(got.Current+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	equal, err := CompareApprovalRecord(workspace)
	if err != nil || !equal.HasRecord || !equal.Equal || equal.Malformed || len(equal.DiffLines) != 0 {
		t.Fatalf("equal-record comparison = %+v, err=%v", equal, err)
	}
}
