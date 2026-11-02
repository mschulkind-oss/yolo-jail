package loopholegrant

import (
	"os"
	"path/filepath"
	"testing"
)

// The block carries the manifest's `access` line and each valued kind's noun, so a daemon can
// render its refusal from the file instead of spelling its own; a file an older launch wrote,
// with neither, still reads, and its daemon keeps its own constants.
func TestTheBlockCarriesAccessAndValueNounAndAnOlderFileStillReads(t *testing.T) {
	dir := resolvedTempDir(t)
	b := Block{Pack: "host-processes", Workspace: "/w", Container: "c", LaunchID: "l", PID: 1,
		Access: "see host processes",
		Kinds: map[string]Kind{"view": {Standing: Pending, Values: []string{"postgres"},
			Text: "see these host processes", ValueNoun: "process names"}}}
	path := writeGrantFile(t, dir, "new.json", nil, b)
	_, got, err := Read(path)
	if err != nil || got == nil {
		t.Fatalf("Read: %v %v", got, err)
	}
	if got.Access != "see host processes" || got.Kinds["view"].ValueNoun != "process names" {
		t.Errorf("block lost access or value noun: %+v", got)
	}

	older := filepath.Join(dir, "older.json")
	if err := os.WriteFile(older, []byte(`{"grants":{"pack":"journal","workspace":"/w","container":"c",`+
		`"launch_id":"l","pid":1,"kinds":{"read":{"standing":"granted"}}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, got, err = Read(older)
	if err != nil || got == nil {
		t.Fatalf("an older grant file no longer reads: %v %v", got, err)
	}
	if got.Access != "" || got.Kinds["read"].ValueNoun != "" {
		t.Errorf("absent access and noun read as %+v, want empty for the daemon's own constants", got)
	}
}
