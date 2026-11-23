package entrypoint

// prelaunchrefreshstart_test.go runs the pre-launch refresh's PER-JAIL-START due rule
// (docs/design/launcher-update-per-jail-start.md, LU-D8): the refresh is due when watched content
// is unseen, or when this jail start has no <bin>.stamp.<id>. No wall clock decides anything.
// Same fake program and probe as prelaunchrefresh_test.go: no network, no agent.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestARefreshIsDueOncePerJailStartAndOnChangedContentOnly is the rule itself: due on a new start,
// due on content nobody refreshed with, never otherwise, and never because an old-style
// <bin>.stamp exists.
func TestARefreshIsDueOncePerJailStartAndOnChangedContentOnly(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{}`)
	// The pre-start-marker stamp: ignored, and never deleted.
	old := filepath.Join(filepath.Dir(p.stampPath()), "tool.stamp")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p.run(t, "")
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Fatalf("one jail start with unchanged content refreshed %d times, want 1", n)
	}
	if _, err := os.Stat(p.stampPath()); err != nil {
		t.Errorf("the refresh left no stamp for this start: %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("an old-style stamp was deleted: %v", err)
	}
	p.newStart(t)
	p.run(t, "")
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("a new jail start refreshed %d times in total, want 2", n)
	}
}

// With no jail-start record nothing is due by the stamp (the boot is the record's only writer), but
// changed content still refreshes at once, and the launcher writes no record of its own.
func TestARefreshWithNoStartRecordRunsOnlyForChangedContent(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{}`)
	p.run(t, "") // this content is now seen
	if err := os.Remove(filepath.Join(p.home, filepath.FromSlash(JailStartRel))); err != nil {
		t.Fatal(err)
	}
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Fatalf("a launch with no start record and no changed content refreshed again (ran %d)", n)
	}
	p.setWatched(t, `{"packages":["git:a"]}`)
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("changed content must still refresh with no record (ran %d)", n)
	}
	if _, err := os.Stat(filepath.Join(p.home, filepath.FromSlash(JailStartRel))); err == nil {
		t.Error("the launcher wrote the jail-start record; the boot is its only writer")
	}
}

// An offline start: a failed refresh is not retried within the start however many launches, the
// next start retries it, and the could-not-check line names its next step.
func TestAFailedRefreshWaitsForTheNextStartAndNamesTheNextStep(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{"packages":["git:a"]}`)
	_, stderr := p.run(t, "", "FAKE_REFRESH_RC=1", registryDownEnv)
	for _, want := range []string{"asks again the next time this jail starts", "yolo pack update"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the could-not-check line lacks its next step %q:\n%s", want, stderr)
		}
	}
	for i := 0; i < 2; i++ {
		p.run(t, "", "FAKE_REFRESH_RC=1", registryDownEnv)
	}
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Errorf("a failed refresh was retried inside its start (ran %d)", n)
	}
	p.newStart(t)
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("the next start did not retry the failed refresh (ran %d)", n)
	}
}
