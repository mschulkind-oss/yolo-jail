package cli

// configtarget_test.go pins the one resolved config target and its disclosure
// (docs/reference/config-target-resolution.md#the-config-target and
// docs/reference/config-target-resolution.md#the-disclosure).
//
// # Why these tests drive the VERB and read its printed line
//
// A test that resolves a target and asserts its fields PASSES WITH THE RESOLUTION DELETED
// FROM EVERY VERB. That is the callee-pinned-while-the-call-site-is-unpinned shape AGENTS.md
// names five shipped instances of. So the contract these tests are written to is the
// CALL-SITE one: deleting a target resolution/disclosure call has to fail something, rather than
// a helper being asserted to return the right string. The six surface verbs and the retained
// config drift/dump verbs below run from two different directories and their printed lines are read.
//
// MUTATION-CHECKED: the surface dispatch test exercises all six verbs through the registry; these
// cases pin resolution at both namespaces' writers rather than asserting only a helper result.

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
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// configAndSurfaceVerbs are the commands whose output discloses a resolved config target.
// Six are surface composition verbs; drift and dump remain under config.
var configAndSurfaceVerbs = [][]string{
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
func TestConfigAndSurfaceVerbsDiscloseTheirTarget(t *testing.T) {
	for _, argv := range configAndSurfaceVerbs {
		verb := argv[0]
		t.Run(verb+"/in a workspace", func(t *testing.T) {
			scratchHostHome(t)
			t.Setenv("YOLO_VERSION", "")
			ws, _ := withWorkspaceCwd(t)
			// reset asks the container runtime whether this workspace's jail runs; the answer
			// is the test's, not the machine's podman's.
			withJailLiveness(t, "", true)

			var out, errw bytes.Buffer
			runTargetVerbForTest(argv, &out, &errw)

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
			runTargetVerbForTest(argv, &out, &errw)

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
			"(docs/reference/config-target-resolution.md [OQ-CR5]): compression is allowed, "+
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

// --- the marker ([OQ-CR2](docs/reference/config-target-resolution.md#oq-cr2)) ----------------

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
				writeFile(t, config.WorkspaceConfigBootPath(dir, "podman-self"), `{}`)
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
			// config.ResolveWorkspaceConfigPath's .jsonc→.json fallback: the marker set has to be
			// whatever config.LoadWorkspaceConfig would READ, or a directory yolo will
			// happily launch in fails to resolve.
			name: "the .json spelling of it",
			seed: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "yolo-jail.json"), `{}`)
			},
			marks: true,
		},
		{
			// The readable-config workspace file (docs/reference/config/project-config.md#the-sections): the
			// format-aware loader reads it first, so a TOML-only workspace is one yolo launches in.
			name: "the TOML workspace config",
			seed: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "yolo-jail.toml"), "")
			},
			marks: true,
		},
		{
			name: "the TOML machine-local override alone",
			seed: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "yolo-jail.local.toml"), "")
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
	if rc := surfaceRunW([]string{"ls"}, &out, &errw); rc != 0 {
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
	surfaceRunW([]string{"ls"}, &out, &errw)
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
	if rc := surfaceRunW([]string{"diff", "claude"}, &out, &errw); rc != 0 {
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
	rc := surfaceRunW([]string{"diff", "claude"}, &out, &errw)
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
// target is gap 3 of docs/reference/config-target-resolution.md ("Where this does not reach"), unruled.
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
				"(docs/reference/config-target-resolution.md#the-config-target, [P2]); a second "+
				"resolution is "+
				"how one report came to describe two homes. If you need this answer, add a "+
				"method to configtarget.go.", file, name)
		}
	})
}

// TestWorkspaceRootIsOnlyTheSealedVerbs pins the exemption above from the other side, so the
// bare-cwd walk cannot quietly spread back into the config verbs while Blocker 3 is open.
//
// ALL FOUR SPELLINGS, AND A DIFFERENT ALLOWLIST FOR EACH. The bare-cwd answer has four
// names, not one: `workspaceRoot()`; `sealedWorkspaceStore()`, which is
// `render.Jail(paths.Home(), workspaceRoot(), nil)` — the same answer one call deeper;
// `sealedConfigTarget()`, the configTarget that store's files are opened through; and
// `overlayKeyCount()`, which reads that store. This test checked only the first, so a config
// verb reaching for any of the others spread the bare cwd with nothing failing — the
// callee-pinned/call-site-unpinned shape one file over, in the fence meant to catch it.
//
// Each list is that identifier's real callers plus the file that DECLARES it (a declaration is
// an identifier too), and they are deliberately not unioned: `workspaceRoot` has no business
// in configls.go, `sealedWorkspaceStore` none outside configtarget.go, and `overlayKeyCount`
// none in configtarget.go. A union of the three files would permit every pair and stop being a
// statement about any of them. `overlayKeyCountAt` is a DIFFERENT identifier and is
// unguarded on purpose: it takes the sidecar path, so it is what every config verb calls
// through the resolved target.
func TestWorkspaceRootIsOnlyTheSealedVerbs(t *testing.T) {
	// configtarget.go declares the walk, its store and the target over it, and
	// sealedConfigTarget is sealedWorkspaceStore's one caller; apply.go is the verb the
	// exemption is FOR (applySealed calls workspaceRoot, sealedConfigTarget and
	// overlayKeyCount); configls.go declares overlayKeyCount — `apply --sealed`'s key counter,
	// beside the listing it also serves.
	allowed := map[string]map[string]bool{
		"workspaceRoot":        {"configtarget.go": true, "apply.go": true},
		"sealedWorkspaceStore": {"configtarget.go": true},
		"sealedConfigTarget":   {"configtarget.go": true, "apply.go": true},
		"overlayKeyCount":      {"configls.go": true, "apply.go": true},
	}
	forEachIdentInPackageSource(t, func(file, name string) {
		files, guarded := allowed[name]
		if !guarded || files[file] {
			return
		}
		t.Errorf("%s names %s(). It is `yolo apply --sealed`'s bare-cwd walk, "+
			"not the config target: a config verb reads the resolved target, and mixing "+
			"the two is the predicate pair "+
			"docs/reference/config-target-resolution.md "+
			"removed. Read the sidecar through the resolved target instead "+
			"(configTarget.overlayPath + overlayKeyCountAt).", file, name)
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

// At the JAIL notch the resolved store is the one beside the home a launch of the selected setup
// binds (named-setups-plan.md SUP-D35): the setup's own, `.yolo/setups/<name>/prism`, and the
// legacy `.yolo/prism` while SUP-D21 keeps the legacy home bound. The jail's boot writes the same
// store through render.Target.WithHomeState, so a verb and the boot cannot read two baselines.
// Red if the target goes back to one store per project, which reverted one setup's edits when
// another setup booted (6a-ii review M2).
func TestTheJailStoreIsWhereThePrismTwinsPutIt(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_SETUP", "podman-self")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tgt := jailConfigTarget(ws, "the cwd")
	want := filepath.Join(paths.SetupStateDir(ws, "podman-self"), "prism")
	if got := tgt.sidecarDir(); got != want {
		t.Errorf("the resolved jail store = %q, want the setup's prism %q", got, want)
	}
	if got, w := tgt.overlayPath("claude", "settings"),
		filepath.Join(want, "claude-settings.overlay.json"); got != w {
		t.Errorf("overlayPath = %q, want %q", got, w)
	}
	if got, w := tgt.storeFile(tgt.overlayPath("claude", "settings")).rootDir(), want; got != w {
		t.Errorf("the rooted store file opens %q, want %q", got, w)
	}
	if got := sealedWorkspaceStoreAt(t, ws).SidecarDir(); got != want {
		t.Errorf("apply --sealed reads %q, want the setup's prism %q", got, want)
	}

	// The jail's own render, told the home its launch bound, writes where the verb reads.
	boot := render.Jail(paths.Home(), ws, nil).WithHomeState("setups/podman-self/home")
	if got := boot.SidecarDir(); got != want {
		t.Errorf("the boot's store = %q, want the verb's %q", got, want)
	}

	// While the legacy home is still bound (SUP-D21), its store is the legacy one.
	if err := os.MkdirAll(paths.LegacyHomeState(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(paths.WorkspaceStateDir(ws), "prism")
	if got := jailConfigTarget(ws, "the cwd").sidecarDir(); got != legacy {
		t.Errorf("with the legacy home bound, the store = %q, want %q", got, legacy)
	}
	if got := render.Jail(paths.Home(), ws, nil).WithHomeState("home").SidecarDir(); got != legacy {
		t.Errorf("the boot's store with the legacy home bound = %q, want %q", got, legacy)
	}
}

// sealedWorkspaceStoreAt is sealedWorkspaceStore from ws as the cwd.
func sealedWorkspaceStoreAt(t *testing.T, ws string) render.Target {
	t.Helper()
	writeFile(t, filepath.Join(ws, config.WorkspaceConfigName), `{}`)
	t.Chdir(ws)
	return sealedWorkspaceStore()
}

// mkdirAllT is os.MkdirAll with the test's error handling.
func mkdirAllT(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// --- `--at` on the read verbs (design step 5) ----------------------------------------------

// `--at` OVERRIDES THE CWD, AND THE DISCLOSURE SAYS IT DID. The flag is the shipped selector
// (`yolo apply --setup podman-self|guest|host`), and a second spelling for the read verbs would be a
// second vocabulary for one fact ([P4]).
func TestAtOverridesTheCwdAndIsDisclosedAsTheChooser(t *testing.T) {
	home := scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	ws, _ := withWorkspaceCwd(t)

	var out, errw bytes.Buffer
	surfaceRunW([]string{"ls", "--setup", "host-self"}, &out, &errw)
	line := disclosureLine(t, "ls --setup host-self", errw.String())
	if !strings.Contains(line, home) || !strings.Contains(line, "host notch") {
		t.Errorf("`--setup host-self` from inside a workspace disclosed %q, want the real home at the "+
			"host notch", line)
	}
	if !strings.Contains(line, "from --setup host-self") {
		t.Errorf("the disclosure must name WHAT CHOSE the target, and here it was the flag "+
			"rather than the cwd: %q", line)
	}

	// The equals spelling is the same flag, and `--setup podman-self` inside a workspace agrees with
	// what the cwd would have chosen — so the flag is an override, not a second resolution.
	out.Reset()
	errw.Reset()
	surfaceRunW([]string{"ls", "--setup=podman-self"}, &out, &errw)
	line = disclosureLine(t, "ls --setup=podman-self", errw.String())
	if !strings.Contains(line, ws) || !strings.Contains(line, "from --setup podman-self") {
		t.Errorf("`--setup=podman-self` disclosed %q, want the cwd's workspace chosen by the flag", line)
	}
}

// `--setup seatbelt-self` IS REFUSED WITH THE SENTENCE `yolo apply` USES, verbatim apart from the verb.
// render.NotchUnbuilt exists because two packages say it and neither can import the other;
// a third spelling of one notch's status is the drift docs/design/declaration-parity.md
// exists to name (OQ-DP3), so this compares the two commands' real output.
func TestAtGuestIsRefusedWithApplysOwnSentence(t *testing.T) {
	// On Linux, where both verbs refuse: on macOS apply points at the guest launch instead
	// (guestnotch_test.go).
	onPlatform(t, false)
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	withWorkspaceCwd(t)

	var configOut, configErr bytes.Buffer
	if rc := surfaceRunW([]string{"ls", "--setup", "seatbelt-self"}, &configOut, &configErr); rc == 0 {
		t.Fatalf("`config ls --setup seatbelt-self` succeeded; the notch is unbuilt:\n%s", configOut.String())
	}
	var applyOut, applyErr bytes.Buffer
	applyMain([]string{"--setup", "seatbelt-self"}, &applyOut, &applyErr, false, nil)

	// The SENTENCE, one line: what follows it is each verb's own next step.
	configTail := firstLine(afterVerb(configErr.String()+configOut.String(), "config"))
	applyTail := firstLine(afterVerb(applyOut.String()+applyErr.String(), "apply"))
	if configTail == "" || configTail != applyTail {
		t.Errorf("the two verbs refuse `guest` with different sentences.\nconfig: %q\napply:  %q\n"+
			"Both must print render.NotchUnbuilt, parameterised by the verb and nothing else.",
			configTail, applyTail)
	}
}

// afterVerb returns what follows the first occurrence of verb + " at the guest notch" — the
// part of render.NotchUnbuilt's sentence that must be identical across its callers.
func afterVerb(output, verb string) string {
	const marker = " at the guest notch"
	i := strings.Index(output, verb+marker)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(output[i+len(verb):])
}

// `--setup podman-self` WITH NO WORKSPACE REFUSES, NAMING WHAT IS MISSING. An explicit request for a
// thing that does not exist is a different case from an unstated default, which answers about
// the host ([OQ-CR2]); degrading silently to the other notch is F1.
//
// ⚠ AND THE REMEDY IS A `cd`, NEVER A FLAG. internal/cli/run/run.go refuses a launch with
// "there is no --workspace flag: cd into the project you meant", so a second way to name a
// workspace is exactly what this must not invent.
func TestAtJailWithNoWorkspaceRefusesByName(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)

	var out, errw bytes.Buffer
	if rc := surfaceRunW([]string{"diff", "claude", "--setup", "podman-self"}, &out, &errw); rc == 0 {
		t.Fatalf("`--setup podman-self` with no workspace succeeded, so it answered about some other "+
			"home without saying so:\n%s", out.String())
	}
	got := errw.String()
	for _, want := range []string{elsewhere, config.WorkspaceConfigName, "cd into the project"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--workspace") {
		t.Errorf("the refusal offers a --workspace flag, which does not exist and is not "+
			"going to:\n%s", got)
	}
	if strings.Contains(out.String(), "Surfaces:") || strings.Contains(got, "Surfaces:") {
		t.Errorf("a refused resolution still disclosed a target:\n%s%s", out.String(), got)
	}
}

// THE READ VERBS AND `apply` ACCEPT THE SAME SET. Not by a copied list — by the same lookup:
// lookupNamedSetup is what `apply --setup` resolves through and what this resolves through, so
// every built-in resolves (or is refused as its notch, never as an unknown name) and a name
// nothing defines is refused naming what is.
func TestSetupAcceptsExactlyTheDefinedSetups(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	withWorkspaceCwd(t)

	for _, s := range setup.BuiltIns() {
		_, refusal := resolveConfigTarget(s.Name)
		if strings.Contains(refusal, "is not defined") {
			t.Errorf("`--setup %s` is a built-in `yolo apply` accepts, and this rejected it as an "+
				"unknown name: %s", s.Name, refusal)
		}
	}
	if _, refusal := resolveConfigTarget("vm"); !strings.Contains(refusal, `setup "vm" is not defined`) ||
		!strings.Contains(refusal, "yolo setup list") {
		t.Errorf("`--setup vm` names no setup and must be refused naming the defined ones, got %q", refusal)
	}
}

// THE REMOVED `--at` IS REFUSED ON THE READ VERBS, naming `--setup` and the built-in its notch
// meant (atRemovedFor), with the exit code of a usage error.
func TestAtIsRefusedOnTheReadVerbsNamingSetup(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	withWorkspaceCwd(t)
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"dump", "--at", "host"}, &out, &errw); rc != 2 {
		t.Errorf("`config dump --at host` rc = %d, want 2", rc)
	}
	if got := errw.String(); !strings.Contains(got, "`--at` is removed. Use `--setup <name>`") ||
		!strings.Contains(got, "yolo config dump --setup host-self") {
		t.Errorf("`config dump --at host` does not name its replacement:\n%s", got)
	}
}

// `--at` IS REFUSED, NOT IGNORED, ON THE VERBS NO NOTCH SELECTS. A surface that accepts a
// declaration it does not honor is the declaration-parity defect; and `--setup host-self` on promote
// would be worse than inert, since it would resolve a target with no workspace store and
// report nothing to promote for a workspace full of captures.
func TestAtIsRefusedOnTheVerbsNoNotchSelects(t *testing.T) {
	scratchHostHome(t)
	t.Setenv("YOLO_VERSION", "")
	withWorkspaceCwd(t)

	for verb := range notchlessVerbs {
		var out, errw bytes.Buffer
		if _, surface := surfaceColorVerbs[verb]; surface {
			if rc := surfaceRunW([]string{verb, "--setup", "host-self"}, &out, &errw); rc != 2 {
				t.Errorf("`surface %s --setup host-self` = rc %d, want 2", verb, rc)
			}
		} else if rc := configRunW([]string{verb, "--setup", "host-self"}, &out, &errw); rc != 2 {
			t.Errorf("`config %s --setup host-self` = rc %d, want 2", verb, rc)
		}
		if !strings.Contains(errw.String(), "--setup does not apply here") {
			t.Errorf("%s --setup host-self did not say why the flag does not apply:\n%s", verb, errw.String())
		}
	}
}

func runTargetVerbForTest(argv []string, out, errw *bytes.Buffer) int {
	if _, surface := surfaceColorVerbs[argv[0]]; surface {
		return surfaceRunW(argv, out, errw)
	}
	return configRunW(argv, out, errw)
}
