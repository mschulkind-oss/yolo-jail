package cli

// configtarget_test.go pins the one resolved config target and its disclosure
// (docs/design/config-target-resolution.md §3, §3.1).
//
// # Why these tests drive the VERB and read its printed line
//
// A test that resolves a target and asserts its fields PASSES WITH THE RESOLUTION DELETED
// FROM EVERY VERB. That is the callee-pinned-while-the-call-site-is-unpinned shape AGENTS.md
// names five shipped instances of, and this design's [§8](docs/design/config-target-resolution.md#8-what-i-would-build-in-order)
// step 1 states its own test contract against it: *"its test is that deleting the resolution
// call site fails something, not that a helper returns the right string"*. So the resolution
// and the disclosure landed together — the disclosure being the observable — and the tests
// below run `yolo config <verb>` from two different directories and read what it printed.
//
// MUTATION-CHECKED: deleting the `fmt.Fprintln(errw, t.disclosure())` from configRunW fails
// TestEveryConfigVerbDisclosesItsTarget for all eight verbs; deleting the
// `resolveConfigTarget()` call cannot compile, because the verbs take the target as a
// parameter — which is what makes [P2](docs/design/config-target-resolution.md#1-the-verdict-and-the-principles-it-rests-on)
// structural rather than a convention.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// configVerbs is every `yolo config` subcommand that resolves a target, i.e. every one. The
// disclosure is UNCONDITIONAL ([OQ-CR5](docs/design/config-target-resolution.md#oq-cr5)), so
// the list is the whole verb surface rather than the ones where it seemed interesting.
var configVerbs = [][]string{
	{"ls"},
	{"render", "claude"},
	{"diff", "claude"},
	{"reset", "claude"},
	{"capture", "claude"},
	{"promote", "claude", "--plan"},
	{"drift"},
	{"dump"},
}

// scratchHostHome points $HOME at a temp dir with no yolo config, so the host target resolves
// a real-shaped home nobody's tests share.
func scratchHostHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// TestEveryConfigVerbDisclosesItsTarget is step 1's contract, in the only form that measures
// it: two cwds, and each verb's PRINTED line naming a different subject and what chose it.
//
// Delete the disclosure from configRunW and all sixteen subtests fail. Nothing here asserts
// that a helper returns a string.
func TestEveryConfigVerbDisclosesItsTarget(t *testing.T) {
	for _, argv := range configVerbs {
		verb := argv[0]
		t.Run(verb+"/in a workspace", func(t *testing.T) {
			scratchHostHome(t)
			t.Setenv("YOLO_VERSION", "")
			ws, _ := withWorkspaceCwd(t)

			var out, errw bytes.Buffer
			configRunW(argv, &out, &errw)

			line := disclosureLine(t, verb, errw.String())
			if !strings.Contains(line, ws) {
				t.Errorf("`yolo config %s` in %s disclosed %q, which does not name the "+
					"workspace it is about", verb, ws, line)
			}
			for _, want := range []string{"jail notch", "from the cwd"} {
				if !strings.Contains(line, want) {
					t.Errorf("`yolo config %s` disclosed %q, missing %q", verb, line, want)
				}
			}
		})
		t.Run(verb+"/outside every workspace", func(t *testing.T) {
			home := scratchHostHome(t)
			t.Setenv("YOLO_VERSION", "")
			// A directory with no marker anywhere above it. It must NOT be under the home:
			// the walk stops at the home either way, but a sibling of it keeps this test
			// about the marker rather than about the breach stop.
			elsewhere, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(elsewhere)

			var out, errw bytes.Buffer
			configRunW(argv, &out, &errw)

			line := disclosureLine(t, verb, errw.String())
			if !strings.Contains(line, home) {
				t.Errorf("`yolo config %s` outside a workspace disclosed %q, which does not "+
					"name the home it fell back to", verb, line)
			}
			if strings.Contains(line, elsewhere) {
				t.Errorf("`yolo config %s` disclosed the CWD as its subject (%q) — a directory "+
					"that resolves no workspace is not a workspace whose store happens to be "+
					"absent, which is the confident empty answer this design removes", verb, line)
			}
			for _, want := range []string{"host notch", "resolving no workspace", "host_management:"} {
				if !strings.Contains(line, want) {
					t.Errorf("`yolo config %s` disclosed %q, missing %q", verb, line, want)
				}
			}
		})
	}
}

// disclosureLine finds the one `Surfaces: …` line a verb printed, failing if there is none or
// more than one — "one line, before the report" is part of the ruling, and two lines naming
// two homes is the defect (F1) rather than a louder disclosure.
func disclosureLine(t *testing.T, verb, stderr string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "Surfaces: ") {
			found = append(found, line)
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("`yolo config %s` printed no disclosure line. It is UNCONDITIONAL "+
			"(docs/design/config-target-resolution.md [OQ-CR5]): compression is allowed, "+
			"suppression is not, and a reader who has to know the rules to notice the line's "+
			"absence has not been disclosed anything.\nstderr:\n%s", verb, stderr)
	default:
		t.Fatalf("`yolo config %s` printed %d disclosure lines. One report describing two "+
			"homes is the defect this design removes, not a fuller disclosure:\n%s",
			verb, len(found), strings.Join(found, "\n"))
	}
	return ""
}

// THE DISCLOSURE IS ON STDERR, and `config dump`'s stdout is the reason. The design's
// transcripts show the line inside a `$ yolo config diff` session, where a terminal conflates
// the two streams; routing it to stdout would make this design break every machine consumer
// of `dump` and of `promote --format json`, which is the failure report-tiers.md's
// "exit 2, stdout empty" rule for the JSON postures exists to prevent. Routing is not
// suppression — the line is emitted on every invocation either way.
func TestTheDisclosureNeverLandsOnAMachineReadableStdout(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	withWorkspaceCwd(t)

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"dump"}, &out, &errw); rc != 0 {
		t.Fatalf("config dump rc=%d: %s", rc, errw.String())
	}
	if strings.Contains(out.String(), "Surfaces:") {
		t.Errorf("the disclosure landed on `config dump`'s stdout, which is the canonical "+
			"config JSON the startup config-change diff validates against:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "Surfaces:") {
		t.Errorf("the disclosure is missing from stderr too, so it was suppressed rather than "+
			"routed:\n%s", errw.String())
	}
}

// --- the marker ([OQ-CR2](docs/design/config-target-resolution.md#oq-cr2)) ----------------

// TestTheMarkerIsAnArtifactNotADirectory is the ruling's load-bearing half. A bare `.yolo/`
// matches `/home/agent` in EVERY jail — it is the anchor for the generated bin/block and
// bin/launch dirs — so `cd ~ && yolo config diff claude` described a workspace at
// /home/agent that has never existed, with the real captures sitting elsewhere.
func TestTheMarkerIsAnArtifactNotADirectory(t *testing.T) {
	cases := []struct {
		name  string
		seed  func(t *testing.T, dir string)
		marks bool
	}{
		{
			name:  "a bare .yolo directory",
			seed:  func(t *testing.T, dir string) { mkdirAllT(t, filepath.Join(dir, ".yolo")) },
			marks: false,
		},
		{
			name: "the generated-script anchor a jail always has",
			seed: func(t *testing.T, dir string) {
				mkdirAllT(t, filepath.Join(dir, ".yolo", "home", "yolo-bin", "block"))
			},
			marks: false,
		},
		{
			name: "a launch artifact",
			seed: func(t *testing.T, dir string) {
				writeFile(t, config.WorkspaceConfigBootPath(dir), `{}`)
			},
			marks: true,
		},
		{
			// The case the config half of the marker exists for: a freshly cloned repo is a
			// workspace BEFORE its first launch. It is the directory the user is about to
			// launch in, and the one they will run `config ls` in to see what they will get.
			name: "a committed workspace config, never launched",
			seed: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, config.WorkspaceConfigName), `{"packs":["claude"]}`)
			},
			marks: true,
		},
		{
			// resolveWorkspaceConfigPath's .jsonc→.json fallback: the marker set has to be
			// whatever config.LoadWorkspaceConfig would READ, or a directory yolo will
			// happily launch in fails to resolve.
			name: "the .json spelling of it",
			seed: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "yolo-jail.json"), `{}`)
			},
			marks: true,
		},
		{
			name: "the machine-local override alone",
			seed: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, config.WorkspaceLocalConfigName), `{}`)
			},
			marks: true,
		},
		{
			// A DIRECTORY with a config's name is not a config. The whole reason the marker is
			// an artifact is that a directory's mere existence is what went wrong the first time.
			name:  "a directory named like a config",
			seed:  func(t *testing.T, dir string) { mkdirAllT(t, filepath.Join(dir, config.WorkspaceConfigName)) },
			marks: false,
		},
		{
			name:  "nothing at all",
			seed:  func(t *testing.T, dir string) {},
			marks: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scratchHostHome(t)
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tc.seed(t, dir)
			if got := workspaceMarked(dir); got != tc.marks {
				t.Errorf("workspaceMarked(%s) = %v, want %v", tc.name, got, tc.marks)
			}
		})
	}
}

// TestNoWorkspaceIsAnAnswerAndNotARefusal: outside a workspace every verb still answers, about
// the host, and says so. The leaning was to REFUSE with a remedy; the ruling amended it,
// because outside a workspace the only home there is to describe is the host's and standing
// there is how the user says so. What is removed is the CONFIDENT EMPTY ANSWER, and naming
// the host target removes it exactly as well as an error does.
func TestNoWorkspaceIsAnAnswerAndNotARefusal(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"ls"}, &out, &errw); rc != 0 {
		t.Fatalf("`config ls` outside a workspace = rc %d, want an ANSWER: %s", rc, errw.String())
	}
	if out.Len() == 0 {
		t.Error("`config ls` outside a workspace printed nothing on stdout")
	}
}

// TestInnermostWorkspaceWinsAndIsDisclosedByPath: a workspace inside a workspace is unchanged
// behaviour — the innermost wins — and the change is that the report names which
// (§4.1's row: *"unchanged, and disclosed by path"*).
func TestInnermostWorkspaceWinsAndIsDisclosedByPath(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	outer, _ := withWorkspaceCwd(t)
	inner := filepath.Join(outer, "vendor", "nested")
	writeFile(t, filepath.Join(inner, config.WorkspaceConfigName), `{}`)
	t.Chdir(inner)

	var out, errw bytes.Buffer
	configRunW([]string{"ls"}, &out, &errw)
	line := disclosureLine(t, "ls", errw.String())
	if !strings.Contains(line, inner) {
		t.Errorf("the innermost workspace must win and be named: disclosed %q, want %q",
			line, inner)
	}
}

// --- the three answers a store can give ([P3]) --------------------------------------------

// A STORE THAT WAS NEVER WRITTEN IS NOT A STORE WITH NO EDITS. "No captured in-jail edits" is
// a measurement, and printing it for a directory nothing has ever created states a negative
// with the confidence of a real one — the failure this design exists to remove, at rc 0.
func TestDiffDistinguishesNeverRenderedFromNoEdits(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	ws, store := withWorkspaceCwd(t)
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"diff", "claude"}, &out, &errw); rc != 0 {
		t.Fatalf("diff with no store = rc %d: %s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "never rendered here") {
		t.Errorf("an ABSENT store must say so, not report an absence of edits:\n%s", got)
	}
	if strings.Contains(got, "No captured in-jail edits") {
		t.Errorf("diff stated a measured negative for a store nothing has written:\n%s", got)
	}
	// AND IT MUST NOT CREATE THE STORE IT FOUND ABSENT (§4.4: the workspace store keeps
	// exactly two writers, and no read verb is one of them).
	if _, err := os.Stat(store); err == nil {
		t.Errorf("a READ verb created the capture store at %s (workspace %s)", store, ws)
	}
}

// AN UNREADABLE STORE IS THE THIRD STATE, and it currently reads as empty: os.ReadDir's error
// became nil and the verb printed the same negative it prints for a store it really read.
func TestDiffRefusesOnAnUnreadableStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so there is no unreadable store to make")
	}
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	_, store := withWorkspaceCwd(t)
	if err := os.Chmod(store, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o755) })

	var out, errw bytes.Buffer
	rc := configRunW([]string{"diff", "claude"}, &out, &errw)
	if rc == 0 {
		t.Fatalf("an unreadable store exited 0, so the answer reads as measured:\n%s%s",
			out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), store) {
		t.Errorf("the refusal does not name the store it could not read:\n%s", errw.String())
	}
}

// --- the census ---------------------------------------------------------------------------

// TestTheRetiredPredicatesHaveNoOtherCallers is the collapse's own guard. Each name below was
// a SECOND resolution of something the target now answers, and re-introducing one is how F1
// and F3 come back: two independently resolved predicates, one report describing two homes.
//
// It reads the package's non-test sources AS SYNTAX rather than as text, and that is not
// fussiness: this file and three others discuss the retired names in prose — a doc comment
// recording what a collapse deleted is supposed to name it — so a grep-shaped census fails on
// its own explanation. What is forbidden is a REFERENCE, so the census walks identifiers.
//
// `workspaceRoot` and `sealedWorkspaceStore` are the declared exemptions and they name their
// reason: `yolo apply --sealed` reads the bare cwd, and whether it should take the resolved
// target is Blocker 3 of docs/design/config-target-resolution-plan.md, unruled.
func TestTheRetiredPredicatesHaveNoOtherCallers(t *testing.T) {
	retired := map[string]bool{
		"surfacesAreLocal":    true,
		"prismSidecarDir":     true,
		"prismOverlayPath":    true,
		"prismLastRenderPath": true,
		"prismProvenancePath": true,
		"hostProvenancePath":  true,
		"hostCaptureDir":      true,
		"hostOwnsSurfaces":    true,
		"resetCapturePaths":   true,
		"composedFileExists":  true,
	}
	forEachIdentInPackageSource(t, func(file, name string) {
		if retired[name] {
			t.Errorf("%s references the retired predicate %q. Every store dir, home root and "+
				"provenance path comes off the ONE resolved configTarget "+
				"(docs/design/config-target-resolution.md §3, [P2]); a second resolution is "+
				"how one report came to describe two homes. If you need this answer, add a "+
				"method to configtarget.go.", file, name)
		}
	})
}

// TestWorkspaceRootIsOnlyTheSealedVerbs pins the exemption above from the other side, so the
// bare-cwd walk cannot quietly spread back into the config verbs while Blocker 3 is open.
//
// configls.go is on the list because overlayKeyCount — `apply --sealed`'s key counter — lives
// beside the listing it also serves; configtarget.go because the walk and its exemption are
// defined there; apply.go because it is the verb the exemption is FOR.
func TestWorkspaceRootIsOnlyTheSealedVerbs(t *testing.T) {
	allowed := map[string]bool{"configtarget.go": true, "apply.go": true, "configls.go": true}
	forEachIdentInPackageSource(t, func(file, name string) {
		if name == "workspaceRoot" && !allowed[file] {
			t.Errorf("%s calls workspaceRoot(). It is `yolo apply --sealed`'s bare-cwd walk, "+
				"not the config target: a config verb reads the resolved target, and mixing "+
				"the two is the predicate pair docs/design/config-target-resolution.md "+
				"removed.", file)
		}
	})
}

// forEachIdentInPackageSource calls fn for every identifier in every non-test .go file of this
// package — the census's reader. Comments are not part of the syntax tree, which is exactly
// why the census is built on one.
func forEachIdentInPackageSource(t *testing.T, fn func(file, name string)) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				fn(name, id.Name)
			}
			return true
		})
	}
}

// --- the store, resolved ------------------------------------------------------------------

// At the JAIL notch the resolved store must be byte-identical to the retired hand-joined
// path, which is what makes step 1 a refactor: the Reuse note in the plan says the collapse
// changes behaviour ONLY under `own`, and this is that claim.
func TestTheJailStoreIsWhereThePrismTwinsPutIt(t *testing.T) {
	scratchHostHome(t)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tgt := jailConfigTarget(ws, "the cwd")
	want := filepath.Join(paths.WorkspaceStateDir(ws), "prism")
	if got := tgt.sidecarDir(); got != want {
		t.Errorf("the resolved jail store = %q, want the workspace prism tree %q", got, want)
	}
	if got, w := tgt.overlayPath("claude", "settings"),
		filepath.Join(want, "claude-settings.overlay.json"); got != w {
		t.Errorf("overlayPath = %q, want %q", got, w)
	}
}

// mkdirAllT is os.MkdirAll with the test's error handling.
func mkdirAllT(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
