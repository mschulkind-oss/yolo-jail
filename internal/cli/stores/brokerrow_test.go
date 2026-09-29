package stores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The brokers' directory is listed as self-bounded by its writer (the audit log rotates),
// never as a store nothing reclaims and never as one a prune sweeps
// (docs/design/boundary-broker.md §8).
func TestBrokerDirIsListedAsSelfBounded(t *testing.T) {
	o, state := testOptions(t)
	dir := filepath.Join(state, "broker")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var row *Store
	for _, s := range stateStores(o, nil) {
		if s.Key == "state.broker" {
			s := s
			row = &s
		}
	}
	if row == nil {
		t.Fatal("no state.broker row")
	}
	if row.Verdict != VerdictYolo || !strings.Contains(row.Reclaimer.Detail, "rotates at 8 MiB") ||
		row.Reclaimer.Func != "" {
		t.Fatalf("broker row %+v", row)
	}
}
