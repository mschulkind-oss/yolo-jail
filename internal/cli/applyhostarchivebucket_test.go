package cli

// applyhostarchivebucket_test.go pins WHERE a host render files the copy it moves aside (V3).
//
// The archive is what makes `apply --host` non-destructive against a real $HOME: yolo never
// deletes a delivered path, it MOVES it and prints where. That promise has two halves, and only
// one of them used to hold. The copy survived — but every kind shared the literal
// `archive/skills`, so a replaced `files` copy (a script the pack owns, pi's models.json, a
// theme) was filed under a directory named for skills. Nothing was lost and nothing was
// findable, which is the same outcome for a user who goes looking under the state dir and
// concludes their file is gone.
//
// So these tests assert the BUCKET, not merely that something was archived. The distinction is
// the whole fix, and it is invisible to any assertion that only counts files.
//
// Every test uses a t.TempDir() home. The real $HOME is never read or written.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// filesPackJSON is a pack whose ONLY host contribution is a `files` tree, so the bucket a
// replaced copy lands in cannot be inherited from a skills or briefing render in the same run.
const filesPackJSON = `{"name":"filesonly","description":"d","contributes":[
  {"kind":"files","from":"bin","into":".mytool/bin"}]}`

// filesFixture writes the pack, selects it beside `claude`, and returns the home and pack dir.
func filesFixture(t *testing.T) (home, packDir string) {
	t.Helper()
	home = t.TempDir()
	packDir = filepath.Join(t.TempDir(), "filesonly")
	writeFile(t, filepath.Join(packDir, "pack.json"), filesPackJSON)
	writeFile(t, filepath.Join(packDir, "bin", "pick.sh"), "#!/bin/sh\necho v1\n")

	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"filesonly"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home, packDir
}

// THE V3 DEFECT. A `files` copy the render replaces is archived under `archive/files`, not under
// a directory named for a kind that had nothing to do with it.
func TestHostFilesArchivesUnderItsOwnKind(t *testing.T) {
	home, packDir := filesFixture(t)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	// The pack now ships different content, so the next apply must replace its own previous
	// copy — which is the ONE moment the `files` kind archives anything.
	writeFile(t, filepath.Join(packDir, "bin", "pick.sh"), "#!/bin/sh\necho v2\n")

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}

	archived := archivedAll(t, home)
	if len(archived) == 0 {
		t.Fatalf("fixture bug: replacing the pack's own copy must archive it:\n%s", report)
	}
	var wrong []string
	found := false
	for _, rel := range archived {
		switch archiveBucketOf(rel) {
		case "files":
			found = true
		case "skills":
			wrong = append(wrong, rel)
		}
	}
	if len(wrong) > 0 {
		sort.Strings(wrong)
		t.Errorf("a `files` copy was archived under the SKILLS bucket: %v\n"+
			"a bucket that names a different kind is where a user stops looking", wrong)
	}
	if !found {
		t.Errorf("no archived copy landed under the `files` bucket, got %v", archived)
	}
	// The report is the other half of findability: an archive nobody is told about is a
	// deletion from the user's point of view, so the path it PRINTS must be the real one.
	if !strings.Contains(report, filepath.Join("archive", "files")) {
		t.Errorf("the report must name the bucket the copy actually went to:\n%s", report)
	}
}

// The archived bytes are the OLD copy, recoverable. A bucket rename that lost the content would
// satisfy the assertion above and defeat the entire point of archiving instead of deleting.
func TestHostFilesArchiveKeepsTheReplacedContent(t *testing.T) {
	home, packDir := filesFixture(t)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	writeFile(t, filepath.Join(packDir, "bin", "pick.sh"), "#!/bin/sh\necho v2\n")
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("second apply rc=%d\n%s", rc, report)
	}

	root := filepath.Join(home, ".local", "share", "yolo-jail", "archive")
	var bodies []string
	for _, rel := range archivedAll(t, home) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(data))
	}
	recoverable := false
	for _, b := range bodies {
		if strings.Contains(b, "echo v1") {
			recoverable = true
		}
	}
	if !recoverable {
		t.Errorf("the REPLACED copy must be recoverable from the archive, got %v", bodies)
	}
}

// The dropped-pack retire archives skills and files through one path-keyed record, so it gets a
// bucket named for the OPERATION. What must never happen is what V3 fixed: a retired `files`
// path filed under a kind's name that is not its own.
func TestDroppedPackRetireArchivesUnderRetired(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("retire apply rc=%d\n%s", rc, report)
	}

	buckets := map[string]bool{}
	for _, rel := range archivedAll(t, home) {
		buckets[archiveBucketOf(rel)] = true
	}
	if !buckets["retired"] {
		t.Errorf("the confirmed retire must archive under the `retired` bucket, got buckets %v\n%s",
			sortedBuckets(buckets), report)
	}
	if buckets["skills"] {
		t.Errorf("the retire must not file a mixed skills/files set under the SKILLS bucket "+
			"(that is V3), got buckets %v", sortedBuckets(buckets))
	}
}

// A retired BRIEFING lands in the briefing bucket — the same defect, the other pack-set-wide
// kind. Unconfirmed, so this drives the retire with no stdin at all.
func TestRetiredBriefingArchivesUnderBriefing(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	got := archivedBriefings(t, home)
	if len(got) == 0 {
		t.Fatalf("the retired briefing must be archived under the `briefing` bucket, got %v\n%s",
			archivedAll(t, home), report)
	}
	for _, rel := range got {
		if archiveBucketOf(rel) != "briefing" {
			t.Errorf("%s is not in the briefing bucket", rel)
		}
	}
}

func sortedBuckets(set map[string]bool) []string {
	var out []string
	for b := range set {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}
