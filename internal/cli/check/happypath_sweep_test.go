package check

// happypath_sweep_test.go pins the sweep half of the next-step work: the findings that passed an
// empty note, or one with no step in it, each name one now (docs/reference/happy-path-principle.md
// coins "next step" and "dead end"). The structural guard keeps a new empty note from landing;
// the behavioral tests read individual notes through their sections.

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// notSwept is the files of this package another change owns: their findings get their next
// steps there, and this guard does not read them until it lands.
var notSwept = map[string]bool{
	"section_nix_probe.go": true,
	"sections_nix.go":      true,
	"sections_macos.go":    true,
	"section_autogc.go":    true,
}

// No r.fail or r.warn in this package passes a literal empty note: a [FAIL] or [WARN] with no
// note reports a problem and stops, the dead end the principle forbids. A finding that truly has
// no step the user can take names who can act instead (rung 4).
func TestNoFindingIsWrittenWithAnEmptyNote(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || notSwept[name] {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "fail" && sel.Sel.Name != "warn") {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != "r" {
				return true
			}
			if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `""` {
				t.Errorf("%s: r.%s with an empty note — name the next step, or who can act",
					fset.Position(call.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

// No [FAIL] or [WARN] in the fixture reports leaves its reader with nothing to do: every one
// carries a note, in the JSON document a script or an agent reads as much as on a terminal.
func TestEveryGradedFindingInTheFixturesHasANote(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  func(*Options)
	}{
		{"no runtime, no repo root", func(*Options) {}},
		{"macOS, no runtime", func(o *Options) { o.IsMacOS, o.Machine = true, "arm64" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			tc.mod(&opts)
			opts.Format = outfmt.JSON
			Check(opts)
			var doc Report
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatalf("decoding the report: %v\n%s", err, out.String())
			}
			graded := 0
			for _, f := range doc.Findings {
				if f.Status != "fail" && f.Status != "warn" {
					continue
				}
				graded++
				if strings.TrimSpace(f.Note) == "" {
					t.Errorf("[%s] %s (%s) has no note", f.Status, f.Message, f.Section)
				}
			}
			if graded == 0 {
				t.Fatalf("the fixture graded nothing, so it proves nothing:\n%s", out.String())
			}
		})
	}
}

// The sections whose findings the sweep gave a note, each driven into the branches that used to
// end at the problem: every [FAIL] and [WARN] they record names a next step now.
func TestSweptSectionsNameANextStep(t *testing.T) {
	gpu := func(vendor string) *jsonx.OrderedMap {
		m, err := jsonx.Decode([]byte(`{"gpu": {"enabled": true, "vendor": "` + vendor + `"}, "kvm": true}`))
		if err != nil {
			t.Fatal(err)
		}
		return m.(*jsonx.OrderedMap)
	}
	loophole, err := jsonx.Decode([]byte(`{"loopholes": {"svc": {"command": ["no-such-daemon"]},
		"abs": {"command": ["/no/such/daemon"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	silent := func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	empty := func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }
	for _, tc := range []struct {
		name string
		mod  func(*Options)
		run  func(*Options, *reporter)
	}{
		{"NVIDIA on a Mac", func(o *Options) { o.IsMacOS = true },
			func(o *Options, r *reporter) { o.sectionGPUNvidia(r, gpu("nvidia")) }},
		{"NVIDIA, nvidia-smi does not run", func(o *Options) {
			o.LookPath = func(n string) (string, bool) { return "/usr/bin/" + n, n == "nvidia-smi" }
		}, func(o *Options, r *reporter) { o.sectionGPUNvidia(r, gpu("nvidia")) }},
		{"NVIDIA, no GPU", func(o *Options) {
			o.LookPath = func(n string) (string, bool) { return "/usr/bin/" + n, n == "nvidia-smi" }
			o.Exec = empty
		}, func(o *Options, r *reporter) { o.sectionGPUNvidia(r, gpu("nvidia")) }},
		{"ROCm on a Mac", func(o *Options) { o.IsMacOS = true },
			func(o *Options, r *reporter) { o.sectionGPUAmd(r, gpu("amd")) }},
		{"ROCm, no driver or nodes", func(*Options) {},
			func(o *Options, r *reporter) { o.sectionGPUAmd(r, gpu("amd")) }},
		{"ROCm, rocminfo does not run", func(o *Options) {
			o.LookPath = func(n string) (string, bool) { return "/usr/bin/" + n, n == "rocminfo" }
		}, func(o *Options, r *reporter) { o.sectionGPUAmd(r, gpu("amd")) }},
		{"ROCm, nodes present but not stat-able", func(o *Options) {
			o.PathExists = func(p string) bool { return p == "/dev/kfd" }
		}, func(o *Options, r *reporter) { o.sectionGPUAmd(r, gpu("amd")) }},
		{"KVM, not stat-able", func(o *Options) { o.PathExists = func(p string) bool { return p == "/dev/kvm" } },
			func(o *Options, r *reporter) { o.sectionKVM(r, gpu("nvidia")) }},
		{"inline loopholes, missing commands", func(*Options) {},
			func(o *Options, r *reporter) { o.sectionInlineLoopholes(r, loophole.(*jsonx.OrderedMap)) }},
		{"macOS podman, machine probe does not run", func(o *Options) {
			o.IsMacOS = true
			o.LookPath = func(n string) (string, bool) { return "/usr/bin/" + n, n == "podman" }
		}, func(o *Options, r *reporter) { o.sectionMacOSPlatform(r, nil) }},
		{"macOS podman, no machine", func(o *Options) {
			o.IsMacOS = true
			o.LookPath = func(n string) (string, bool) { return "/usr/bin/" + n, n == "podman" }
			o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 125} }
		}, func(o *Options, r *reporter) { o.sectionMacOSPlatform(r, nil) }},
		{"macOS Apple Container, probe does not run", func(o *Options) {
			o.IsMacOS = true
			o.LookPath = func(n string) (string, bool) { return "/usr/bin/" + n, n == "container" }
		}, func(o *Options, r *reporter) { o.sectionMacOSPlatform(r, nil) }},
		{"running jails, ps does not run", func(o *Options) { o.Exec = silent },
			func(o *Options, r *reporter) { o.sectionRunningJails(r, "podman") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			tc.mod(&opts)
			r := newReporter(&out, false)
			tc.run(&opts, r)
			graded := 0
			for _, f := range r.findings {
				if f.Status != "fail" && f.Status != "warn" {
					continue
				}
				graded++
				if strings.TrimSpace(f.Note) == "" {
					t.Errorf("[%s] %s has no note:\n%s", f.Status, f.Message, out.String())
				}
			}
			if graded == 0 {
				t.Fatalf("the fixture graded nothing, so it proves nothing:\n%s", out.String())
			}
		})
	}
}

// Check() creates every Global Storage directory before the section looks (EnsureGlobalStorage),
// so a missing one is a creation that failed — and the note used to promise "Will be created on
// first run", a run that fails the same way. It says why it is missing now, and what to change.
func TestMissingStorageDirSaysWhyCreationFailed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A file where ~/.local must be a directory: every MkdirAll under it fails, root or not.
	if err := os.WriteFile(filepath.Join(home, ".local"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.SkipEnsureStorage = false
	opts.PathExists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	opts.Stderr = &bytes.Buffer{}
	Check(opts)
	got := stripANSI(out.String())
	_, after, ok := strings.Cut(got, "[WARN] Home directory missing: ")
	if !ok {
		t.Fatalf("no missing-directory finding:\n%s", got)
	}
	note, _, _ := strings.Cut(after, "[WARN]")
	if strings.Contains(note, "Will be created on first run") {
		t.Errorf("the note promises a first run will create what this run could not:\n%s", got)
	}
	for _, want := range []string{"yolo could not create it: ", "not a directory", "then: yolo check"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q:\n%s", want, note)
		}
	}
}

// A probe that does not answer names the command to run by hand, at each call site.
func TestUnansweredProbesNameTheCommandToRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	silent := func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }

	var img bytes.Buffer
	(&Options{Exec: silent, Getenv: func(string) string { return "" }, IsMacOS: true}).
		sectionContainerImage(newReporter(&img, false), "podman", "hint", "")
	if !strings.Contains(img.String(), "Run `podman images ") {
		t.Errorf("the image probe's note names no command:\n%s", img.String())
	}

	var jails bytes.Buffer
	(&Options{Exec: silent}).sectionRunningJails(newReporter(&jails, false), "podman")
	if !strings.Contains(jails.String(), "Run `podman ps` yourself") {
		t.Errorf("the running-jails probe's note names no command:\n%s", jails.String())
	}

	var rt bytes.Buffer
	opts := baseOptions(t, &rt)
	opts.IsMacOS = true // the one-shot probes; Linux podman goes through the readiness gate
	opts.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	opts.sectionContainerRuntime(newReporter(&rt, false))
	if !strings.Contains(rt.String(), "[FAIL] podman found but not working: exec failed") ||
		!strings.Contains(rt.String(), "Run `podman --version` yourself") {
		t.Errorf("a runtime that will not start names no command:\n%s", rt.String())
	}
}

// A config finding in Merged Configuration names where to fix it and the re-check.
func TestMergedConfigFindingNamesWhereToFixIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	if err := os.WriteFile(filepath.Join(opts.Workspace, "yolo-jail.jsonc"), []byte(`{"no_such_key": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	Check(opts)
	got := stripANSI(out.String())
	_, after, ok := strings.Cut(got, "no_such_key")
	if !ok {
		t.Fatalf("no finding for the unknown key:\n%s", got)
	}
	note, _, _ := strings.Cut(after, "[")
	if !strings.Contains(note, "yolo config-ref") || !strings.Contains(note, "then: yolo check") {
		t.Errorf("the config finding names no fix:\n%s", got)
	}
}

// The lockfile finding's step depends on why LoadLock refused: a newer yolo's file wants a newer
// yolo, and a corrupt one is written again.
func TestLockfileFindingNamesTheStepForItsCause(t *testing.T) {
	dir := t.TempDir()
	newer := filepath.Join(dir, "newer.json")
	if err := os.WriteFile(newer, []byte(`{"schema": 999, "packs": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lockfileNote(newer, os.ErrInvalid); !strings.Contains(got, "yolo update") {
		t.Errorf("a newer yolo's lockfile is told %q", got)
	}
	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lockfileNote(corrupt, os.ErrInvalid); !strings.Contains(got, "rm "+shquote.Quote(corrupt)+" && yolo pack install") {
		t.Errorf("a corrupt lockfile is told %q", got)
	}
}

// The issue tracker a yolo-bug note names is this module's repository.
func TestIssuesURLIsThisModulesRepo(t *testing.T) {
	gomod, err := os.ReadFile(filepath.Join("..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(gomod), "\n")
	module := strings.TrimSpace(strings.TrimPrefix(first, "module"))
	if want := "https://" + module + "/issues"; issuesURL != want {
		t.Errorf("issuesURL = %q, want %q (go.mod's module path)", issuesURL, want)
	}
}

// A self-check whose program would not start used to be told "If the program it runs is
// missing, `yolo pack install` fetches a loophole's programs" — but a program the pack declares
// under "binaries" and has not fetched never reaches the run (the gate says `yolo pack install`
// itself), so this one is a program pack install does not fetch. Its step is the command itself.
func TestSelfCheckThatWillNotStartNamesItsCommand(t *testing.T) {
	isolatedModuleDir(t)
	mod := selfCheckModule(t, t.TempDir(), "acme-gone", []string{"/no/such/acme-doctor", "--self-check"})
	recordPackModule(t, mod, true)

	r, out := runCheckLoopholes(t, t.TempDir())
	var note string
	for _, f := range r.findings {
		if f.Status == "warn" && strings.Contains(f.Message, "acme-gone: self-check could not run") {
			note = f.Note
		}
	}
	if note == "" {
		t.Fatalf("no could-not-run finding:\n%s", out)
	}
	if strings.Contains(note, "yolo pack install") {
		t.Errorf("a program pack install does not fetch is sent to pack install:\n%s", note)
	}
	if !strings.Contains(note, "Run `/no/such/acme-doctor --self-check` yourself") || !strings.Contains(note, "then: yolo check") {
		t.Errorf("the note does not name the self-check to run:\n%s", note)
	}
}

// A self-check withheld because nothing vouched for its module says it is a yolo bug to report,
// and now says where.
func TestWithheldSelfCheckNamesTheIssueTracker(t *testing.T) {
	isolatedModuleDir(t)
	mod := selfCheckModule(t, t.TempDir(), "acme-withheld", []string{"/bin/true"})
	recordPackModule(t, mod, false)

	_, out := runCheckLoopholes(t, t.TempDir())
	if !strings.Contains(out, "please report it") || !strings.Contains(out, issuesURL) {
		t.Errorf("the yolo-bug note names no issue tracker:\n%s", out)
	}
	if strings.Contains(out, "yolo pack install") {
		t.Errorf("a withheld self-check is sent to pack install:\n%s", out)
	}
}

// The lockfile note through its call site: a corrupt lockfile in the user config directory is
// told how to write it again, by the Packs section itself.
func TestCorruptLockfileFindingThroughThePacksSection(t *testing.T) {
	packsFixture(t, `{"packs": ["claude"]}`)
	lock := packsrc.LockPath(paths.UserConfigPath())
	if err := os.WriteFile(lock, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	var note string
	for _, f := range r.findings {
		if f.Status == "fail" && strings.HasPrefix(f.Message, "Lockfile: ") {
			note = f.Note
		}
	}
	if !strings.Contains(note, "rm "+shquote.Quote(lock)+" && yolo pack install") {
		t.Errorf("the lockfile finding's note is %q:\n%s", note, out.String())
	}
}
