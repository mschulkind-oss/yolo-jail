package entrypoint

// adoptionarchive_test.go measures OQ-CO7's one-time adoption archive
// (docs/design/config-ownership-and-promotion.md §6.3.3) AT BOTH NOTCHES, and every test here
// drives a REAL adoption path rather than the writer:
//
//	host   RenderHostPack(..., render.OwnershipOwn, ...) — what `yolo host apply --assert` calls
//	jail   ConfigurePackSurfaces(e, packs)              — the boot loop, verbatim
//
// ⚠ THAT IS THE POINT, not a stylistic preference. This repo has five times shipped a test that
// pins a CALLEE while its CALL SITE is unpinned, and an archive writer is exactly the shape that
// invites it: a function that takes bytes and a path, trivially testable in isolation, and
// worthless if adoption never calls it. Delete the archiveAdoption call from
// persistStatefulSurface and every test below must go red. If you add one that would not, add
// the one that would instead.
//
// Every test renders into a t.TempDir() home and (at the jail) a t.TempDir() workspace.
// ⚠ Never point one at a real home: these are --assert-equivalent renders that write files.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostConfigArchive is where a host `own` adoption must land the copy: the state dir of the
// home being rendered, under the archive root `yolo host apply` already uses, in a `config`
// bucket beside the kind buckets, keyed by SURFACE.
//
// Spelled out literally rather than through render.Target, so this file states the layout
// instead of agreeing with whatever the code computes. A change to either path is then a
// visible decision here.
func hostConfigArchive(home, agent, name, basename string) string {
	return filepath.Join(home, ".local", "share", "yolo-jail", "archive", "config",
		agent+"-"+name, basename)
}

// jailConfigArchive is the jail's twin: the workspace's own .yolo/ tree, which is the anchor
// the capture sidecars already use and the one both backends name directly.
func jailConfigArchive(workspace, agent, name, basename string) string {
	return filepath.Join(workspace, ".yolo", "archive", "config", agent+"-"+name, basename)
}

// THE COPY IS OF THE FILE AS YOLO FOUND IT, measured on a home yolo has NEVER applied to: a
// hand-written file, in the user's own formatting, adopted by a first `own` apply.
//
// The fixture is chosen so the render VISIBLY CHANGES the file — the seed is compact and yolo
// re-emits canonically — because that is the only way this can tell a copy taken BEFORE the
// write from one taken after. The assert -> own transition below cannot: adoption there is
// byte-identical by construction (§11's zero-bytes criterion), so both orderings archive the
// same bytes and the ordering bug would pass. Two fixtures, one for each claim.
func TestOwnAdoptionArchivesTheFileAsYoloFoundIt(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".acme", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Compact, unsorted, no trailing newline: a file a human or an agent wrote, not one a
	// composing encoder emitted.
	const seed = `{"permissions":{"ask":["Bash(rm:*)"]},"apiKeyHelper":"/usr/local/bin/acme-key.sh"}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, false, nil)
	if err != nil {
		t.Fatalf("`own` apply: %v", err)
	}
	if len(results) != 1 || results[0].Action != "rendered" {
		t.Fatalf("the owned render did not render: %+v", results)
	}
	rendered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) == seed {
		t.Fatalf("the render left the seed byte-identical, so this fixture cannot tell a copy "+
			"taken before the write from one taken after — pick a seed the canonical re-emit "+
			"changes:\n%s", rendered)
	}

	want := hostConfigArchive(home, "acme", "settings", "settings.json")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("no adoption archive at %s: %v\n\nOQ-CO7 rules ONE ARCHIVE AT ADOPTION: the "+
			"pre-existing file copied once before the first render that composes the whole "+
			"file out of what it holds. If the archiveAdoption call has been removed from "+
			"persistStatefulSurface, adoption now has no net at all.", want, err)
	}
	if string(got) != seed {
		t.Errorf("the archive does not hold the file as yolo FOUND it.\narchived:\n%s\nthe "+
			"file before the render:\n%s\n\nA copy taken after the render is yolo's own "+
			"output under the name of a backup — the archive must be written BEFORE the "+
			"surface write, from the bytes composeStatefulSurface read.", got, seed)
	}
	// And the render REPORTS it. An archive the user cannot find is a deletion from where
	// they stand, and at the host notch e.Stderr is nil by design — so this field is the only
	// channel `yolo host apply` has for saying where the copy went.
	if results[0].Archived != want {
		t.Errorf("HostRenderResult.Archived = %q, want %q — the copy was made and not "+
			"reported, so nothing tells the user it exists", results[0].Archived, want)
	}
}

// THE CASE THE WHOLE RULING IS ABOUT: a home already applying under `host_management: assert`
// is switched to `own`. That is not a first apply and the loss it can take is not a named table
// entry, so `confirmHostLosses` — which reads EntryLosses and fires only on FirstApply — says
// "nothing would be lost" and prompts for nothing. The archive is what stands in for the prompt
// it cannot give, and this pins that it is written on exactly that transition.
//
// assertBaselineBytes is the state of that home before the switch, pinned by
// hostassertbaseline_test.go, so the comparison is against a stated expectation.
func TestOwnAdoptionArchivesOnTheAssertToOwnSwitch(t *testing.T) {
	home, path := assertBaselineHome(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != assertBaselineBytes {
		t.Fatalf("the assert baseline moved; fix TestHostAssertLeavesTheAdoptionBaseline "+
			"first:\n%s", before)
	}

	results, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, false, nil)
	if err != nil {
		t.Fatalf("`own` apply: %v", err)
	}
	want := hostConfigArchive(home, "acme", "settings", "settings.json")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("no adoption archive at %s: %v\n\nThis is the transition OQ-CO7 exists for "+
			"— `assert` -> `own` on a home yolo has already applied to, which the one-way-door "+
			"prompt is structurally blind to. Without the archive the deep-merged leaf has no "+
			"net at all.", want, err)
	}
	if string(got) != assertBaselineBytes {
		t.Errorf("the archive holds:\n%s\nwant the assert baseline:\n%s", got, assertBaselineBytes)
	}
	if len(results) == 1 && results[0].Archived != want {
		t.Errorf("HostRenderResult.Archived = %q, want %q", results[0].Archived, want)
	}
}

// ONE ARCHIVE, AND WHAT MAKES IT IDEMPOTENT. A second adoption of the same surface is reachable
// (`yolo config reset`, a deleted or corrupt last_render, a restored workspace), and the
// decisive reason not to re-archive is not tidiness: by then the file on disk is YOLO'S OWN
// OUTPUT, so a second copy would overwrite the user's original with the very thing the original
// exists to be compared against. The net would perform the deletion it exists to prevent.
//
// Driven by destroying the capture store, which is exactly what reset does to it — so this is
// the reachable path, not a contrived one.
func TestOwnAdoptionArchivesOnlyTheFirstTime(t *testing.T) {
	home, path := assertBaselineHome(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pack := adoptionBaselinePack(t)
	if _, err := RenderHostPack(pack, home, render.OwnershipOwn, false, nil); err != nil {
		t.Fatalf("first `own` apply: %v", err)
	}

	// Make the file DIFFERENT, so a second archive would be visibly the wrong bytes rather
	// than accidentally equal to the first.
	if err := os.WriteFile(path, []byte(`{"theme":"system","scribbled":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Destroy the baseline: no trusted last_render means the next render ADOPTS again.
	if err := os.RemoveAll(render.Host(home, nil, render.OwnershipOwn).SidecarDir()); err != nil {
		t.Fatal(err)
	}
	results, err := RenderHostPack(pack, home, render.OwnershipOwn, false, nil)
	if err != nil {
		t.Fatalf("second `own` apply: %v", err)
	}

	archive := hostConfigArchive(home, "acme", "settings", "settings.json")
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("the archive is gone after a second adoption: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("the second adoption overwrote the archive.\nnow:\n%s\nthe original it "+
			"replaced:\n%s\n\nBy the second adoption the file on disk is yolo's own output, "+
			"so re-archiving destroys the one copy of what the user had. Idempotency is the "+
			"archive's own existence — see archiveAdoption.", got, original)
	}
	if len(results) == 1 && results[0].Archived != "" {
		t.Errorf("the second adoption reported Archived=%q — it wrote nothing, and saying it "+
			"did would point the user at a copy that is not of their file", results[0].Archived)
	}
	// Exactly one copy in the bucket, so "one archive" is measured rather than inferred from
	// the one path this test happens to name.
	entries, err := os.ReadDir(filepath.Dir(archive))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the config bucket holds %d copies of one surface (%v), want 1 — this is an "+
			"archive, not a history; the later-regression case is what git on the pack is for",
			len(entries), names)
	}
}

// A FAILED ARCHIVE REFUSES THE ADOPTION, and the file is untouched.
//
// The alternative — warn and adopt anyway — is a net that can silently not exist, which is
// worth less than one that says so: it takes the one-way door WITHOUT the thing that makes it
// survivable and reports a successful render. Refusing is also what the surrounding code
// already does with comparable failures (every other write in this path returns its error) and
// what the host dispatch already knows how to present (a per-surface refusal, the remaining
// surfaces still rendered).
//
// The obstruction is a FILE where the archive root's parent directory must be, which makes
// MkdirAll fail as ENOTDIR — chosen over chmod deliberately, because this suite runs as root in
// the jail and a mode bit would not stop it.
func TestOwnAdoptionRefusesWhenTheArchiveCannotBeWritten(t *testing.T) {
	home, path := assertBaselineHome(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(home, ".local", "share", "yolo-jail", "archive")
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, false, nil)
	if err != nil {
		t.Fatalf("an archive failure must be a per-surface refusal, not a pack-level error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want one surface result, got %+v", results)
	}
	if got := results[0].Action; !strings.HasPrefix(got, "refused:") {
		t.Fatalf("the surface reported %q; want a refusal. Adopting with no archive is the "+
			"silent-net outcome OQ-CO7 exists to prevent", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the refused surface was written anyway:\n%s\n\n\"the file is untouched\" is "+
			"only true if the archive is attempted BEFORE the surface write", after)
	}
	// And nothing was persisted, so the NEXT apply still sees a first migration and can retry
	// the whole adoption from the same pre-existing file. A refusal that left a last_render
	// behind would make the loss permanent on the very next render.
	if _, err := os.Stat(render.Host(home, nil, render.OwnershipOwn).SidecarDir()); err == nil {
		t.Error("the refused render left a capture store — the next apply would then take the " +
			"steady-state branch and adopt nothing, so the refusal would have cost the user " +
			"their file one render later")
	}
}

// OBSERVE ARCHIVES NOTHING. A dry run makes no copy, which is what keeps HostRenderResult
// honest: it reports a path only when one exists. It is also what lets confirmHostLosses run a
// full observe pass for free — a preview that littered an archive would be a side effect of
// asking a question.
func TestOwnObserveArchivesNothing(t *testing.T) {
	home, _ := assertBaselineHome(t)
	results, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, true, nil)
	if err != nil {
		t.Fatalf("observe under `own`: %v", err)
	}
	if len(results) == 1 && results[0].Archived != "" {
		t.Errorf("observe reported Archived=%q", results[0].Archived)
	}
	root := filepath.Join(home, ".local", "share", "yolo-jail", "archive")
	if _, err := os.Stat(root); err == nil {
		t.Errorf("the observe posture created %s — a dry run must leave no trace", root)
	}
}

// THE MECHANISM IS THE GATE, not the notch. `assert` renders the same surface into the same
// home through `rmw`, which asserts individual keys and adopts no file at all — so there is no
// one-way door and nothing to net. An archive appearing here would mean the copy had been
// hoisted above the mechanism switch, where it would fire on every apply forever: per-apply
// snapshots, which the ruling explicitly is not.
func TestAssertNotchArchivesNothing(t *testing.T) {
	home, _ := assertBaselineHome(t) // this already ran one --assert apply
	if _, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipAssert, false, nil); err != nil {
		t.Fatalf("second --assert apply: %v", err)
	}
	root := filepath.Join(home, ".local", "share", "yolo-jail", "archive")
	if _, err := os.Stat(root); err == nil {
		t.Errorf("an `assert` apply wrote an adoption archive at %s — rmw adopts nothing, so "+
			"this is a snapshot of every apply rather than a net for adoption", root)
	}
}

// archiveJailPack is a one-surface pack for the JAIL notch, declaring no mode — i.e. `stateful`
// (manifest.Surface.ResolvedMode's default), the mechanism whose first render ADOPTS. Same
// shape as adoptionBaselinePack, kept separate so a change to the host fixture cannot silently
// change what the jail tests render.
func archiveJailPack(t *testing.T) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":     "~/.acme/settings.json",
		"defaults": map[string]any{"theme": "system"},
		"managed":  map[string]any{"permissions": map[string]any{"defaultMode": "default"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// jailAdoptionHome seeds a jail home with a pre-existing agent-written file and returns the
// Env the BOOT LOOP will be given, plus the surface path.
func jailAdoptionHome(t *testing.T, seed string) (e *Env, path string) {
	t.Helper()
	e = &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	withCtxRoot(t, t.TempDir(), "acme")
	path = filepath.Join(e.Home, ".acme", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if seed != "" {
		if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return e, path
}

// THE JAIL NOTCH, through the boot loop. The rule is uniform and the NET splits by the
// primitive available: the host apply can prompt, a boot cannot — in-jail `yolo apply` is a
// report, not a provision — so at this notch the copy is the ONLY net there is. That is why
// OQ-CO7 is owed here too, and why building only the host half would have left the notch that
// needs it most with nothing.
func TestJailFirstMigrationArchivesThePreExistingFile(t *testing.T) {
	const seed = `{"apiKeyHelper":"/usr/local/bin/acme-key.sh","permissions":{"ask":["Bash(rm:*)"]}}` + "\n"
	e, path := jailAdoptionHome(t, seed)

	ConfigurePackSurfaces(e, []*packload.Pack{archiveJailPack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}
	if rendered, err := os.ReadFile(path); err != nil || string(rendered) == seed {
		t.Fatalf("the boot loop did not render the surface (err=%v) — nothing was adopted, so "+
			"this test is not measuring adoption", err)
	}

	want := jailConfigArchive(e.Workspace, "acme", "settings", "settings.json")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("no adoption archive at %s: %v\n\nThe jail's half of OQ-CO7 goes into the "+
			"workspace's own .yolo/ tree — the same anchor the capture sidecars use, which is "+
			"the one per-workspace directory both backends name directly.", want, err)
	}
	if string(got) != seed {
		t.Errorf("the archive holds %q, want the file as yolo found it (%q)", got, seed)
	}
}

// THE DEGENERATE INPUT THAT MATTERS MOST: a pre-existing file yolo CANNOT PARSE.
//
// At the host notch `own` refuses such a surface outright (OQ-CO9). At the jail it does not:
// ComposeStateful treats an undecodable current file as "skip capture", so the render replaces
// it WHOLESALE with the pure render. That is a total loss, it is silent, and the adopted
// residue is empty — so the archive is the only thing that records the file ever existed. A
// gate keyed on the file PARSING rather than on bytes being present would miss exactly this.
func TestJailAdoptionArchivesAnUnparseableFile(t *testing.T) {
	const garbage = "{this is not json at all\n"
	e, path := jailAdoptionHome(t, garbage)

	ConfigurePackSurfaces(e, []*packload.Pack{archiveJailPack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) == garbage {
		t.Skip("the jail no longer replaces an unparseable stateful surface; the loss this " +
			"test nets does not exist here any more")
	}

	got, err := os.ReadFile(jailConfigArchive(e.Workspace, "acme", "settings", "settings.json"))
	if err != nil {
		t.Fatalf("an unparseable file was replaced wholesale with no archive: %v\n\nThis is "+
			"the most destructive adoption available at this notch — the residue is empty, so "+
			"nothing else records what the file held. The archive gate must key on BYTES "+
			"BEING PRESENT, never on the file parsing.", err)
	}
	if string(got) != garbage {
		t.Errorf("the archive holds %q, want the unparseable original %q", got, garbage)
	}
}

// AN ABSENT FILE IS NOT AN ADOPTION. This is the overwhelmingly common case — every surface of
// every fresh jail home — and it is what keeps the archive from existing at all for most
// surfaces. Nothing was there, so nothing can be lost.
func TestJailAdoptionSkipsAnAbsentFile(t *testing.T) {
	e, _ := jailAdoptionHome(t, "")
	ConfigurePackSurfaces(e, []*packload.Pack{archiveJailPack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}
	root := filepath.Join(e.Workspace, ".yolo", "archive")
	if _, err := os.Stat(root); err == nil {
		t.Errorf("a first render with no pre-existing file wrote an archive at %s — every "+
			"fresh jail home would carry one copy of nothing per surface", root)
	}
}

// AN EMPTY FILE IS THE SAME ANSWER, for the same reason: restoring "absent" and restoring
// "zero bytes" leave the user in the same place, so an archive of zero bytes is not a net — it
// is a directory entry saying yolo ran.
func TestJailAdoptionSkipsAnEmptyFile(t *testing.T) {
	e, _ := jailAdoptionHome(t, "")
	path := filepath.Join(e.Home, ".acme", "settings.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ConfigurePackSurfaces(e, []*packload.Pack{archiveJailPack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}
	root := filepath.Join(e.Workspace, ".yolo", "archive")
	if _, err := os.Stat(root); err == nil {
		t.Errorf("an empty pre-existing file produced an archive at %s", root)
	}
}

// THE JAIL'S SECOND BOOT ARCHIVES NOTHING — steady state has a trusted baseline, captures a
// diff against it, and adopts nothing. Without this, "one archive" would be a claim about the
// host path alone while every boot of a jail added another copy to a tree the human reads.
func TestJailSteadyStateBootArchivesNothing(t *testing.T) {
	const seed = `{"apiKeyHelper":"/usr/local/bin/acme-key.sh"}` + "\n"
	e, _ := jailAdoptionHome(t, seed)
	pack := archiveJailPack(t)
	ConfigurePackSurfaces(e, []*packload.Pack{pack})
	ConfigurePackSurfaces(e, []*packload.Pack{pack})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}
	dir := filepath.Dir(jailConfigArchive(e.Workspace, "acme", "settings", "settings.json"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the archive bucket: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("two boots left %d copies in %s, want 1 — an archive per boot is the "+
			"per-apply snapshot the ruling refuses", len(entries), dir)
	}
}

// A SECRET IN AN `rmw` SURFACE NEVER REACHES THE ARCHIVE, measured on the shipped pack the
// exposure is about.
//
// Two things at once, and they are the same fact seen from two sides. The MECHANISM GATE:
// copilot/config is read-modify-write precisely so the agent's OAuth state is never composed,
// and rmw adopts nothing, so it is archived never — a copy here would mean the archive had
// been hoisted above the mechanism switch. The PRIVACY property: the jail's archive lives in
// <workspace>/.yolo/, which crosses into a container and plausibly into git, so it inherits
// exactly the exposure the capture overlay beside it already has — and the surface that would
// have carried a credential into it is the one B2 already moved out of composition.
func TestCopilotRMWSecretIsNeverArchived(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	dir := filepath.Join(e.Home, ".copilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"copilot_tokens":{"gh":"SECRET"},"logged_in_users":["ada"],"theme":"dark"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePackByName(e, "copilot"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(e.Workspace, ".yolo", "archive")
	var found []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // an absent root is the expected answer
		}
		data, rerr := os.ReadFile(p)
		if rerr == nil && strings.Contains(string(data), "SECRET") {
			found = append(found, p)
		}
		return nil
	})
	if len(found) > 0 {
		t.Errorf("copilot's OAuth token reached the adoption archive at %v — copilot/config is "+
			"rmw so it adopts nothing, and the archive lives in the workspace tree a user may "+
			"commit", found)
	}
}

// A TARGET WITH NOWHERE TO PUT AN ARCHIVE says so rather than returning a relative path, which
// is what makes archiveAdoption's refusal reachable as a refusal instead of as a stray
// directory in whatever cwd the process happens to have. Guest is the notch waiting to reach
// this writer, so it is the one worth pinning.
func TestTargetsWithNoArchiveDirSayNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target render.Target
	}{
		{"unset", render.Target{}},
		{"host with no home", render.Host("", nil, render.OwnershipOwn)},
	} {
		if got := tc.target.ArchiveDir(); got != "" {
			t.Errorf("%s: ArchiveDir() = %q, want \"\"", tc.name, got)
		}
		if got := tc.target.ArchivePath("acme", "settings", "settings.json"); got != "" {
			t.Errorf("%s: ArchivePath() = %q, want \"\"", tc.name, got)
		}
	}
	// And a real jail target refuses a surface with no basename rather than archiving the
	// bucket directory itself.
	jail := render.Jail(t.TempDir(), t.TempDir(), nil)
	if got := jail.ArchivePath("acme", "settings", ""); got != "" {
		t.Errorf("ArchivePath with no basename = %q, want \"\"", got)
	}
}
