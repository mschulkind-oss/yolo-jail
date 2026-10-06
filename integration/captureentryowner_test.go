package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// THE CAPTURE STORE'S CLEANUP TAKES ONLY THIS TEST'S ENTRIES. The store a capture test writes into
// is the machine's own (capture_test.go says why), so an entry that appeared while the test ran may
// be a concurrent capture's or another run's. An entry is this test's when its receipt names the
// test's bin; one whose receipt is not written yet names nobody, and stays. Runs under -short: it
// needs no container.
func TestCaptureCleanupTakesOnlyThisTestsEntries(t *testing.T) {
	store := t.TempDir()
	entry := func(key string, receipt ...string) {
		t.Helper()
		dir := filepath.Join(store, "entries", key)
		if err := os.MkdirAll(filepath.Join(dir, "tree"), 0o755); err != nil {
			t.Fatal(err)
		}
		if len(receipt) == 0 {
			return
		}
		var body string
		for _, line := range receipt {
			body += line + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "receipts.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const bin = "capture-owner-fixture"
	entry("aaaa", `{"kind":"capture","act":"record","bin":"`+bin+`"}`)
	before := captureEntryNames(t, store)

	entry("bbbb", `{"kind":"capture","act":"record","bin":"another-runs-bin"}`)
	entry("cccc", `{"kind":"build","act":"record","bin":"another-runs-bin"}`,
		`{"kind":"build","act":"record","bin":"`+bin+`"}`)
	entry("dddd")
	entry("eeee", `not json`, `{"kind":"capture","act":"record","bin":"`+bin+`-longer"}`)

	if got, want := newCaptureEntries(t, store, before, bin), []string{"cccc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("newCaptureEntries = %v, want %v: the entries whose receipt names %s and that "+
			"appeared since before", got, want, bin)
	}
	removeNewCaptureEntries(t, store, before, bin)
	for key, kept := range map[string]bool{"aaaa": true, "bbbb": true, "cccc": false, "dddd": true, "eeee": true} {
		_, err := os.Stat(filepath.Join(store, "entries", key))
		if kept && err != nil {
			t.Errorf("the cleanup removed entry %s, which is not this test's: %v", key, err)
		}
		if !kept && !os.IsNotExist(err) {
			t.Errorf("the cleanup left entry %s, which this test added (%v)", key, err)
		}
	}
}
