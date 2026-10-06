package prune

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/miseuse"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// misePruneOpts is baseOpts with a tool store holding one version no jail has used and one a jail
// uses, judged at a clock two windows ahead (the fixtures' directories are made now).
func misePruneOpts(t *testing.T) (Options, *miseFixture) {
	t.Helper()
	o, gs := baseOpts(t)
	f := &miseFixture{t: t, store: filepath.Join(gs, "mise"), now: time.Now().Add(2 * miseuse.Window)}
	f.install("node/20.1.0")
	f.install("node/22.5.0")
	f.since(f.now.Add(-miseuse.Window - 24*time.Hour))
	f.record("/home/u/code/a", f.now.Add(-time.Hour), "node/22.5.0")
	o.Now = func() time.Time { return f.now }
	// The host's prune, stated: the default reads YOLO_VERSION, which a jail running these tests
	// sets.
	o.InJail = func() bool { return false }
	// Stated too: the default is "" on a Mac, whose store is in the container VM.
	o.MiseStore = func() string { return f.store }
	return o, f
}

// TestPruneListsAndRemovesUnusedToolVersions is `yolo prune`'s half of OQ-DF4: the dry run names
// the version no jail used and keeps it; --apply removes it, and only it. It fails if Run stops
// calling the section.
func TestPruneListsAndRemovesUnusedToolVersions(t *testing.T) {
	o, f := misePruneOpts(t)
	var buf bytes.Buffer
	o.Out = &buf
	if rc := Run(o); rc != 0 {
		t.Fatalf("dry run rc=%d:\n%s", rc, buf.String())
	}
	for _, want := range []string{
		"Unused tool versions  (the shared mise store; no jail has used them for 30 d)",
		"  would remove: 1000 B across 1 version(s)",
	} {
		if !hasLine(&buf, want) {
			t.Errorf("missing line %q in:\n%s", want, buf.String())
		}
	}
	if !strings.Contains(buf.String(), "    • node 20.1.0  1000 B") {
		t.Errorf("the dry run does not name the version:\n%s", buf.String())
	}
	if !f.present("node/20.1.0") {
		t.Fatal("a dry run removed a version")
	}

	buf.Reset()
	o.Apply = true
	if rc := Run(o); rc != 0 {
		t.Fatalf("apply rc=%d:\n%s", rc, buf.String())
	}
	if !hasLine(&buf, "  removed: 1000 B across 1 version(s)") {
		t.Errorf("apply did not report the removal:\n%s", buf.String())
	}
	if f.present("node/20.1.0") || !f.present("node/22.5.0") {
		t.Fatalf("after --apply: 20.1.0 present=%v, 22.5.0 present=%v; want only the used one kept",
			f.present("node/20.1.0"), f.present("node/22.5.0"))
	}
}

// TestPruneFailsWhenTheToolRecordsCannotAnswer: a decline is an error, as for every other sweep
// (OQ-LS2), and it names the next step. A wait is not: it says when judging can start, and the
// command still succeeds.
func TestPruneFailsWhenTheToolRecordsCannotAnswer(t *testing.T) {
	o, f := misePruneOpts(t)
	ws := t.TempDir()
	o.Exec = stubExec(map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"): runtime.FromWorkspace(ws) + " running\n",
	}, nil)
	var buf bytes.Buffer
	o.Out, o.Apply = &buf, true
	if rc := Run(o); rc == 0 {
		t.Fatalf("a running jail with no record did not fail the command:\n%s", buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "FAILED — the running jail") || !strings.Contains(out, "to fix:") {
		t.Errorf("the decline does not say what failed and what to do:\n%s", out)
	}
	if !f.present("node/20.1.0") {
		t.Fatal("a declined sweep removed a version")
	}

	o, f = misePruneOpts(t)
	f.since(f.now.Add(-24 * time.Hour))
	buf.Reset()
	o.Out, o.Apply = &buf, true
	if rc := Run(o); rc != 0 {
		t.Fatalf("waiting for the record to cover a window failed the command:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "  not yet — jails have recorded") || !f.present("node/20.1.0") {
		t.Errorf("the wait is not said, or a version went anyway:\n%s", buf.String())
	}
}

// TestPruneLeavesTheToolStoreToTheHost: a jail sees the store but not the host's running jails.
func TestPruneLeavesTheToolStoreToTheHost(t *testing.T) {
	o, f := misePruneOpts(t)
	o.InJail = func() bool { return true }
	var buf bytes.Buffer
	o.Out, o.Apply = &buf, true
	_ = Run(o)
	if !strings.Contains(buf.String(), "skipped — this jail's tools come from the host's store") {
		t.Errorf("in-jail prune does not say it leaves the store to the host:\n%s", buf.String())
	}
	if !f.present("node/20.1.0") {
		t.Fatal("an in-jail prune removed a version from the shared store")
	}

	// A Mac's jails keep the store in the container VM: MiseStore's default there is "".
	o, f = misePruneOpts(t)
	o.MiseStore = func() string { return "" }
	buf.Reset()
	o.Out, o.Apply = &buf, true
	if rc := Run(o); rc != 0 {
		t.Fatalf("a store out of reach failed the command:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "skipped — on a Mac the jails' tool store") || !f.present("node/20.1.0") {
		t.Errorf("a store yolo cannot reach is not said to be skipped:\n%s", buf.String())
	}
}

// TestPruneJSONCarriesTheToolVersions: the JSON report has the category, in both modes.
func TestPruneJSONCarriesTheToolVersions(t *testing.T) {
	o, _ := misePruneOpts(t)
	var buf bytes.Buffer
	o.Out, o.Format = &buf, outfmt.JSON
	_ = Run(o)
	var rep Report
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	for _, c := range rep.Categories {
		if c.Name == "mise_versions" {
			if c.Count != 1 || c.Bytes != 1000 || c.Unit != "versions" {
				t.Errorf("mise_versions = %+v, want 1 version of 1000 B", c)
			}
			return
		}
	}
	t.Fatalf("no mise_versions category in %+v", rep.Categories)
}
