package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// namedVerbFixture is a host workspace whose user config selects the shipped git and github
// packs, with [allow] held by a config.d fragment rather than config.toml, so a test can tell
// "the file that already holds [allow]" from the default user file.
type namedVerbFixture struct {
	workspace, userConfig, fragment, project string
}

func newNamedVerbFixture(t *testing.T, project, fragment string) namedVerbFixture {
	t.Helper()
	ws := testconfig.Workspace(t, project)
	user := testconfig.User(t, "packs = [\"git\", \"github\"]\n")
	fragmentPath := filepath.Join(filepath.Dir(user.Path), "config.d", "work.toml")
	if err := os.MkdirAll(filepath.Dir(fragmentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragmentPath, []byte(fragment), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	// The broker set a post-write `yolo check` reports comes from loopholes.PackModules, whose
	// lazy resolver memoizes its first answer for the process: whichever earlier test first
	// asked, under its own HOME, would otherwise fix the pack set this fixture's git and github
	// packs are read from. Cleared on the way in and the way out, as enableHome does.
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	withConfigEditCWD(t, ws.Root)
	return namedVerbFixture{workspace: ws.Root, userConfig: user.Path, fragment: fragmentPath, project: ws.Path}
}

// gateRecord is what reached the approval gate: the workspace it was run for and the
// capability list the effective config held AT THAT MOMENT, read through the same loader the
// gate reads. It records the gate's input, not a mocked prompt's output.
type gateRecord struct {
	calls     int
	workspace string
	always    []any
}

func recordAllowGate(t *testing.T, interactive bool) *gateRecord {
	t.Helper()
	rec := &gateRecord{}
	oldGate, oldInteractive := allowApprovalGate, allowInteractive
	t.Cleanup(func() { allowApprovalGate, allowInteractive = oldGate, oldInteractive })
	allowInteractive = func() bool { return interactive }
	allowApprovalGate = func(workspace string, out, errw io.Writer) int {
		rec.calls++
		rec.workspace = workspace
		cfg, err := config.LoadReadableConfig(workspace, true, func(string) {})
		if err != nil {
			t.Errorf("the gate's config read failed: %v", err)
			return 1
		}
		if got, ok := configValueAt(cfg, "allow", "git", "always"); ok {
			rec.always, _ = got.([]any)
		}
		return 0
	}
	return rec
}

func TestAllowWritesIntoTheFileHoldingAllowAndOffersApproval(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.github]\nalways = [\"read\"]\n")
	userBefore := readFile(t, fx.userConfig)
	rec := recordAllowGate(t, true)

	rc, out, errs := captureStd(t, func() int { return registry["allow"]([]string{"allow", "git", "fetch"}) })
	if rc != 0 {
		t.Fatalf("yolo allow git fetch rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := readFile(t, fx.fragment)
	if !strings.Contains(got, "[allow.git]") || !strings.Contains(got, "fetch") || !strings.HasPrefix(got, "[allow.github]\nalways = [\"read\"]\n") {
		t.Fatalf("config.d/work.toml (the file holding [allow]) was not spliced:\n%s", got)
	}
	if after := readFile(t, fx.userConfig); after != userBefore {
		t.Fatalf("config.toml changed although it does not hold [allow]:\n%s", after)
	}
	if !strings.Contains(out, fx.fragment) {
		t.Fatalf("the written file is not named: %q", out)
	}
	if rec.calls != 1 || rec.workspace != fx.workspace {
		t.Fatalf("approval gate calls=%d workspace=%q, want 1 call for %q; stdout=%q", rec.calls, rec.workspace, fx.workspace, out)
	}
	if !slices.Contains(rec.always, any("fetch")) {
		t.Fatalf("the gate ran before the write was visible: allow.git.always=%v", rec.always)
	}

	// Without a terminal the verb never runs a gate nobody can answer; it names the command.
	rec2 := recordAllowGate(t, false)
	rc, out, errs = captureStd(t, func() int { return registry["allow"]([]string{"allow", "git", "on_request", "push"}) })
	if rc != 0 || rec2.calls != 0 || !strings.Contains(out, "yolo config apply") {
		t.Fatalf("non-interactive allow rc=%d gate calls=%d stdout=%q stderr=%q", rc, rec2.calls, out, errs)
	}
	if got := readFile(t, fx.fragment); !strings.Contains(got, "on_request") || !strings.Contains(got, "push") {
		t.Fatalf("on_request was not written:\n%s", got)
	}
}

func TestAllowUnknownCapabilityListsDeclared(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	rec := recordAllowGate(t, true)
	rc, out, errs := captureStd(t, func() int { return registry["allow"]([]string{"allow", "git", "fetchh"}) })
	if rc != 1 {
		t.Fatalf("rc=%d, want 1; stdout=%q stderr=%q", rc, out, errs)
	}
	for _, want := range []string{"fetchh", "fetch", "new_branches", "push", "push_tags", "yolo allow git"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("refusal does not name %q: %q", want, errs)
		}
	}
	if got := readFile(t, fx.fragment); got != "" || rec.calls != 0 {
		t.Fatalf("a refused allow wrote %q or ran the gate (%d)", got, rec.calls)
	}

	rc, _, errs = captureStd(t, func() int { return registry["allow"]([]string{"allow", "gitt", "fetch"}) })
	if rc != 1 || !strings.Contains(errs, "git, github") || !strings.Contains(errs, "yolo pack add") {
		t.Fatalf("unknown source rc=%d stderr=%q", rc, errs)
	}
}

func TestAllowInJailPrintsHostRun(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	t.Setenv("YOLO_VERSION", "test")
	t.Setenv("YOLO_HOST_DIR", "/home/someone/code/proj")
	rec := recordAllowGate(t, true)
	rc, out, errs := captureStd(t, func() int { return registry["allow"]([]string{"allow", "git", "fetch"}) })
	if rc != 1 {
		t.Fatalf("rc=%d, want 1; stdout=%q stderr=%q", rc, out, errs)
	}
	want := "yolo host-run -C /home/someone/code/proj --name allow-git-fetch -- yolo allow git fetch"
	if !strings.Contains(errs, want) || !strings.Contains(errs, "on the host:  yolo allow git fetch") {
		t.Fatalf("in-jail refusal does not print %q: %q", want, errs)
	}
	if got := readFile(t, fx.fragment); got != "" || rec.calls != 0 {
		t.Fatalf("an in-jail allow wrote %q or ran the gate (%d)", got, rec.calls)
	}
}

// loopholeGrantPacks is the shipped set replaced by two grant-declaring fixture packs, since no
// shipped pack declares [[grants]] until the flips: host-processes with the valued `view` (process
// names), and journal with `read` requestable by default and `read_all` not, as the design's
// §4.3 gives them.
var loopholeGrantPacks = fstest.MapFS{
	"host-processes/pack.toml": {Data: []byte("name = \"host-processes\"\n\n[[loophole]]\nfrom = \"loopholes/host-processes\"\n")},
	"host-processes/loopholes/host-processes/loophole.toml": {Data: []byte(`name = "host-processes"
description = "a grant-declaring fixture"
version = 1
transport = "loopback-tls"
access = "see host processes"

[host_daemon]
cmd = ["yolo", "internal", "daemon", "host-processes", "--socket", "{socket}"]
publishes = "socket"

[[grants]]
kind = "view"
text = "see these host processes"
valued = true
value_noun = "process names"
`)},
	"journal/pack.toml": {Data: []byte("name = \"journal\"\n\n[[loophole]]\nfrom = \"loopholes/journal\"\n")},
	"journal/loopholes/journal/loophole.toml": {Data: []byte(`name = "journal"
description = "a grant-declaring fixture"
version = 1
transport = "loopback-tls"
access = "read the host's journal"

[host_daemon]
cmd = ["yolo", "internal", "daemon", "journal", "--socket", "{socket}"]
publishes = "socket"

[[grants]]
kind = "read"
text = "read your user journal"
requestable_by_default = true

[[grants]]
kind = "read_all"
text = "read the whole host journal"
`)},
}

// newLoopholeGrantVerbFixture is a host workspace whose TOML user config selects the fixture
// grant-declaring packs, with userExtra appended to it and project as the workspace file.
func newLoopholeGrantVerbFixture(t *testing.T, project, userExtra string) namedVerbFixture {
	t.Helper()
	shipped := packload.EmbeddedFS()
	packload.ReleaseEmbedded()
	t.Cleanup(func() {
		packload.ReleaseEmbedded()
		packload.SetEmbeddedFS(shipped)
	})
	packload.SetEmbeddedFS(loopholeGrantPacks)
	ws := testconfig.Workspace(t, project)
	user := testconfig.User(t, "packs = [\"host-processes\", \"journal\"]\n"+userExtra)
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	withConfigEditCWD(t, ws.Root)
	return namedVerbFixture{workspace: ws.Root, userConfig: user.Path, project: ws.Path}
}

// `yolo allow <pack> on_request <kind>` writes the loophole ceiling the grant reader takes
// (docs/reference/loophole-grants.md §4.2), validated against the pack's [[grants]], and
// offers the approval as it does for a broker.
func TestAllowWritesALoopholeKind(t *testing.T) {
	fx := newLoopholeGrantVerbFixture(t, "", "")
	rec := recordAllowGate(t, false)
	rc, out, errs := captureStd(t, func() int {
		return registry["allow"]([]string{"allow", "host-processes", "on_request", "view"})
	})
	if rc != 0 {
		t.Fatalf("yolo allow host-processes on_request view rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := readFile(t, fx.userConfig)
	if !strings.Contains(got, "[allow.host-processes]") || !strings.Contains(got, `on_request = ["view"]`) {
		t.Fatalf("the ceiling was not written:\n%s\nstdout=%q stderr=%q", got, out, errs)
	}
	if rec.calls != 0 || !strings.Contains(out, "yolo config apply") {
		t.Fatalf("non-interactive allow ran the gate (%d) or did not name the approval: %q", rec.calls, out)
	}

	// An unvalued kind defaults to always, as a broker capability does.
	rc, out, errs = captureStd(t, func() int { return registry["allow"]([]string{"allow", "journal", "read"}) })
	if rc != 0 {
		t.Fatalf("yolo allow journal read rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.userConfig); !strings.Contains(got, "[allow.journal]") || !strings.Contains(got, `always = ["read"]`) {
		t.Fatalf("allow.journal.always was not written:\n%s", got)
	}

	before := readFile(t, fx.userConfig)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		// A valued kind under always: its value is the project's (§5's wording), so the verb
		// names the on_request command instead, and writes nothing.
		{[]string{"allow", "host-processes", "view"}, []string{"carries a value only a project can give", "yolo allow host-processes on_request view"}},
		{[]string{"allow", "host-processes", "always", "view"}, []string{"carries a value only a project can give", "yolo allow host-processes on_request view"}},
		// An undeclared kind lists the declared ones.
		{[]string{"allow", "journal", "write"}, []string{`"write"`, "read, read_all", "yolo allow journal always read"}},
	} {
		rc, out, errs := captureStd(t, func() int { return registry["allow"](tc.args) })
		if rc != 1 {
			t.Fatalf("%v rc=%d, want 1; stdout=%q stderr=%q", tc.args, rc, out, errs)
		}
		for _, want := range tc.want {
			if !strings.Contains(errs, want) {
				t.Fatalf("%v refusal lacks %q: %q", tc.args, want, errs)
			}
		}
	}
	if readFile(t, fx.userConfig) != before {
		t.Fatal("a refused allow wrote the user config")
	}
}

// journalKindStanding is the standing config.ResolveLoopholeGrants gives the fixture journal's
// kind, reading the configs the fixture wrote.
func journalKindStanding(t *testing.T, workspace, kind string) config.LoopholeKindStanding {
	t.Helper()
	_, packSettings, err := allowSelectedBrokers()
	if err != nil {
		t.Fatal(err)
	}
	decl, ok := findGrantDeclaringPack("journal", packSettings)
	if !ok {
		t.Fatal("the fixture journal pack declares no grants")
	}
	cfg, err := config.LoadReadableConfig(workspace, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := config.ResolveLoopholeGrants(config.LoopholeGrantInput{User: cfg, Workspace: cfg,
		Declarations: []config.LoopholeGrantDeclaration{decl}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Packs {
		for _, k := range p.Kinds {
			if k.Grant.Kind == kind {
				return k.Standing
			}
		}
	}
	t.Fatalf("the plan has no journal kind %q", kind)
	return ""
}

// The first `yolo allow <pack> on_request <kind>` creates the list, and a written list replaces
// the requestable-by-default ceiling, so the verb seeds the list with those kinds: asking to add
// read_all must not withdraw read.
func TestAllowFirstOnRequestListKeepsRequestableByDefaultKinds(t *testing.T) {
	fx := newLoopholeGrantVerbFixture(t, "", "")
	recordAllowGate(t, false)
	if got := journalKindStanding(t, fx.workspace, "read"); got != config.LoopholeKindRequestable {
		t.Fatalf("before the write, read stands %q, want requestable by default", got)
	}
	rc, out, errs := captureStd(t, func() int {
		return registry["allow"]([]string{"allow", "journal", "on_request", "read_all"})
	})
	if rc != 0 {
		t.Fatalf("yolo allow journal on_request read_all rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.userConfig); !strings.Contains(got, `on_request = ["read", "read_all"]`) {
		t.Fatalf("the first on_request list was not seeded with read:\n%s", got)
	}
	if !strings.Contains(out, "read (requestable by default)") {
		t.Fatalf("the output does not say read was kept: %q", out)
	}
	for _, kind := range []string{"read", "read_all"} {
		if got := journalKindStanding(t, fx.workspace, kind); got != config.LoopholeKindRequestable {
			t.Fatalf("after the write, %s stands %q, want requestable", kind, got)
		}
	}
}

// Appending to a list the user already wrote adds only the kind asked for: a requestable-by-default
// kind the user left out of it stays out.
func TestAllowAppendToOnRequestListDoesNotReseedDefaults(t *testing.T) {
	fx := newLoopholeGrantVerbFixture(t, "", "\n[allow.journal]\non_request = []\n")
	recordAllowGate(t, false)
	rc, out, errs := captureStd(t, func() int {
		return registry["allow"]([]string{"allow", "journal", "on_request", "read_all"})
	})
	if rc != 0 {
		t.Fatalf("yolo allow journal on_request read_all rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.userConfig); !strings.Contains(got, `on_request = ["read_all"]`) {
		t.Fatalf("appending re-added a default the user removed:\n%s", got)
	}
	if strings.Contains(out, "requestable by default") {
		t.Fatalf("an append claimed to seed defaults: %q", out)
	}
	if got := journalKindStanding(t, fx.workspace, "read"); got != config.LoopholeKindNotAllowed {
		t.Fatalf("read stands %q, want not allowed: the user's list leaves it out", got)
	}
}
