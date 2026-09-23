package cli

// applyhostgitpack_test.go is the host notch's handling of a GIT-FETCHED pack: one resolved
// offline from the pack store exactly as a jail launch resolves it (run.PackRoot), and — when
// one cannot be resolved — the all-or-nothing rule: an incomplete pack set is never applied.
//
// The bug these pin: `yolo host apply` resolved packs through a loader that returned nothing
// for every non-embedded, non-local pack, so a pack the maintainer HAD installed was reported
// "not resolvable offline (fetched packs need `yolo pack install`) — skipped" — advice that
// could not help, since the pack was already installed — and the rest of the set was applied
// without it: a half state in a real home.
//
// Every test runs against a t.TempDir() home with XDG_CONFIG_HOME inside it, and clears
// YOLO_PACK_ROOT: this suite runs inside a yolo jail, where that variable names the live
// jail's staged pack tree, and Resolve's staged-tree fallback would otherwise be measuring
// the machine rather than the fixture.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// gitPackHome makes a temp home whose config selects `claude` plus the git pack at src under
// the name "gp". It does NOT install the pack; installGitPack does.
func gitPackHome(t *testing.T, src string, extra string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	selectPacksWith(t, home, `"claude",{"source":"`+src+`","name":"gp"}`, extra)
	return home
}

// selectPacksWith is selectPacks plus extra top-level config keys (a raw JSON fragment,
// leading comma included, or "").
func selectPacksWith(t *testing.T, home, list, extra string) {
	t.Helper()
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[`+list+`]`+extra+`}`)
	stubDeclaredBins(t)
}

// installGitPack fetches the configured git packs into the temp home's store, through the
// real `yolo pack install`.
func installGitPack(t *testing.T) {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack install rc=%d\n%s%s", rc, out.String(), errw.String())
	}
}

// gitPackRepoWith builds a real git repo holding exactly files (repo-relative path -> body),
// committed on `main` — gitPackRepo's shape for a test that needs its own manifest. A body
// starting "symlink:" makes the path a symlink to the rest of the string instead.
func gitPackRepoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if target, ok := strings.CutPrefix(body, "symlink:"); ok {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeFile(t, path, body)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "pack"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// CleanGitEnv for gitPackRepo's reason: hook-exported git state would redirect this
		// helper onto the committer's index.
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_AUTHOR_NAME=t",
			"GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// neverFetchedGitSource is a git address whose mirror is not in any store: nothing ever
// fetched it, and nothing in these tests will. No git binary is needed to be refused over it.
const neverFetchedGitSource = "git+file:///nonexistent/yolo-test/never-fetched.git?ref=main"

// hookHome is gitPackHome with the launch hook opted in and the environment neutralized as
// gateFixture neutralizes it. The hook's own lock file is taken and released once first: the
// lock is the hook's, written on every gated launch, and is not a render.
func hookHome(t *testing.T, src string) string {
	t.Helper()
	home := gitPackHome(t, src, `,"host_apply_on_launch":true`)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(acceptConfigChangesEnv, "")
	setGateTTY(t, false)
	if lock := tryHostApplyLock(home); lock != nil {
		lock.Close()
	}
	return home
}

// hashTree fingerprints every path under root — its relative name, its mode, and a regular
// file's bytes or a symlink's target — so "nothing was written" is asserted over the whole
// home rather than over the few paths a test happened to think of.
func hashTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		line := rel + " " + info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			line += " -> " + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			line += " " + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatalf("hashing %s: %v", root, err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// THE REPRODUCTION. A git pack that `yolo pack install` has put in the store is resolvable
// offline — the launch resolves it every time — so host apply must render its content rather
// than skip it with advice to run the command the user already ran.
func TestApplyHostRendersAnInstalledGitPack(t *testing.T) {
	repo := gitPackRepo(t)
	home := gitPackHome(t, "git+file://"+repo+"//tools/agent-pack?ref=main", "")
	installGitPack(t)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "not resolvable") || strings.Contains(report, "cannot be resolved") {
		t.Errorf("an INSTALLED git pack was reported unresolvable:\n%s", report)
	}
	skill := filepath.Join(home, ".claude", "skills", "gitskill", "SKILL.md")
	data, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("the git pack's skill did not reach %s: %v\nreport:\n%s", skill, err, report)
	}
	if !strings.Contains(string(data), "from git") {
		t.Errorf("%s does not carry the git pack's skill:\n%s", skill, data)
	}
}

// NO HALF STATES. One configured pack the store does not have, and --assert refuses the WHOLE
// apply: non-zero, nothing written anywhere in the home, and the report names the pack, why it
// is unresolvable, and the command that fixes it.
func TestApplyHostAssertRefusesAnIncompletePackSet(t *testing.T) {
	home := gitPackHome(t, neverFetchedGitSource, "")
	before := hashTree(t, home)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Fatalf("--assert with an unresolvable pack must refuse (non-zero rc)\n%s", report)
	}
	if after := hashTree(t, home); after != before {
		t.Errorf("a refused apply wrote into the home — the `claude` pack resolves fine, so a "+
			"partial apply is exactly the half state this refusal exists to prevent\n%s", report)
	}
	for _, want := range []string{"gp", "never been fetched", "yolo pack install", "Nothing was written"} {
		if !strings.Contains(report, want) {
			t.Errorf("the refusal must contain %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "skipped") {
		t.Errorf("an unresolvable pack is refused, never `skipped`:\n%s", report)
	}
}

// The control for the refusal above: the SAME home minus the unresolvable pack does write. If
// it did not, "nothing was written" would be passing for a reason that has nothing to do with
// the refusal.
func TestApplyHostWritesWhenEveryPackResolves(t *testing.T) {
	home := gitPackHome(t, neverFetchedGitSource, "")
	selectPacksWith(t, home, `"claude"`, "")
	before := hashTree(t, home)
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("fixture control: rc=%d\n%s", rc, report)
	}
	if hashTree(t, home) == before {
		t.Fatal("fixture control: a complete set wrote nothing, so the refusal test proves nothing")
	}
}

// THE DRY RUN SAYS THE SAME THING, prominently, and its verdict says an --assert would REFUSE
// — never "would complete", which is what it said while the pack was silently skipped. Exit 0:
// the dry run is information (OQ-RO5).
func TestApplyHostDryRunSaysAnAssertWouldRefuse(t *testing.T) {
	gitPackHome(t, neverFetchedGitSource, "")

	var out, errw bytes.Buffer
	rc := applyHost(&out, &errw, true, false, nil)
	colored := out.String() + errw.String()
	_, plain := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("the dry run is information and exits 0 (OQ-RO5); rc=%d\n%s", rc, plain)
	}
	if !strings.Contains(plain, "An --assert would REFUSE") {
		t.Errorf("the verdict must say an --assert would refuse:\n%s", plain)
	}
	if strings.Contains(plain, "would complete") {
		t.Errorf("the verdict claims an --assert would complete over an incomplete pack set:\n%s", plain)
	}
	for _, want := range []string{"gp", "never been fetched", "yolo pack install"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the dry run must contain %q:\n%s", want, plain)
		}
	}
	// PROMINENT, not dim: the line naming the pack is red, and no dim-styled line names it.
	var named bool
	for _, line := range strings.Split(colored, "\n") {
		if !strings.Contains(line, "gp") || !strings.Contains(line, "never been fetched") {
			continue
		}
		named = true
		if strings.Contains(line, "\x1b[2m") && !strings.Contains(line, "\x1b[31m") &&
			!strings.Contains(line, "\x1b[1;31m") {
			t.Errorf("the unresolvable pack is reported dim, the way the skip used to be:\n%q", line)
		}
	}
	if !named {
		t.Errorf("no line names the unresolvable pack and its reason:\n%s", colored)
	}
}

// THE MACHINE DOCUMENT CARRIES IT TOO: outcome `refused`, and the pack with its reason.
func TestApplyHostJSONNamesTheUnresolvablePack(t *testing.T) {
	gitPackHome(t, neverFetchedGitSource, "")
	var out, errw bytes.Buffer
	if rc := applyHostFormatted(&out, &errw, false, false, nil, "json"); rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	var doc struct {
		Outcome    string `json:"outcome"`
		Unresolved []struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		} `json:"unresolved_packs"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if doc.Outcome != outcomeRefused {
		t.Errorf("outcome = %q, want %q", doc.Outcome, outcomeRefused)
	}
	if len(doc.Unresolved) != 1 || doc.Unresolved[0].Name != "gp" ||
		!strings.Contains(doc.Unresolved[0].Reason, "never been fetched") {
		t.Errorf("unresolved_packs = %+v, want gp with its reason", doc.Unresolved)
	}
}

// THE LAUNCH HOOK, SAME RULE. With the opt-in on and a pack the store does not have, the hook
// renders NOTHING — even though the resolvable part of the set is stale (a fresh home) — and
// says so loudly, naming the pack and the remedy. It still launches: the observe pass found a
// configuration problem, and the home holds whatever the last complete apply left, which is a
// consistent state rather than a half one.
func TestHostApplyGateRefusesToRenderAnIncompletePackSet(t *testing.T) {
	home := hookHome(t, neverFetchedGitSource)
	before := hashTree(t, home)

	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "claude") {
		t.Fatalf("the hook must still launch over an unresolvable pack:\n%s", errw.String())
	}
	if after := hashTree(t, home); after != before {
		t.Errorf("the hook rendered an incomplete pack set into the home:\n%s", errw.String())
	}
	for _, want := range []string{"gp", "never been fetched", "yolo pack install", "did not render"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the hook's refusal must contain %q:\n%s", want, errw.String())
		}
	}
}

// The hook's CALL SITE, through hostMain: the refusal reaches a wrapped launch, and the launch
// proceeds to the PATH lookup (rc 127 for a binary that does not exist).
func TestHostExecReportsAnIncompletePackSetAndLaunches(t *testing.T) {
	home := hookHome(t, neverFetchedGitSource)
	before := hashTree(t, home)

	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil)
	report := out.String() + errw.String()
	if rc != 127 {
		t.Fatalf("rc = %d, want 127 (past the hook, to the PATH lookup)\n%s", rc, report)
	}
	if !strings.Contains(report, "did not render") || !strings.Contains(report, "gp") {
		t.Errorf("the wrapped launch did not report the refused render:\n%s", report)
	}
	if hashTree(t, home) != before {
		t.Errorf("the wrapped launch rendered an incomplete pack set:\n%s", report)
	}
}

// `yolo check-deps` PROBES A GIT PACK'S DEPS — it resolves through the same loader.
func TestCheckDepsProbesAnInstalledGitPack(t *testing.T) {
	repo := gitPackRepoWith(t, map[string]string{
		"tools/agent-pack/pack.json": `{"name":"gp","contributes":[
		  {"kind":"requires","bin":"yolo-test-git-pack-dep","install_hints":{"brew":"x","apt":"x"}}]}`,
	})
	gitPackHome(t, "git+file://"+repo+"//tools/agent-pack?ref=main", "")
	installGitPack(t)

	var out, errw bytes.Buffer
	checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
	if !strings.Contains(out.String(), "yolo-test-git-pack-dep") {
		t.Errorf("check-deps did not probe the git pack's declared dependency:\n%s%s",
			out.String(), errw.String())
	}
}

// A capture over an unresolvable pack names it WITH the resolver's reason, rather than
// telling the user to run a command they may already have run.
func TestCaptureTargetNamesWhyAPackIsUnresolvable(t *testing.T) {
	gitPackHome(t, neverFetchedGitSource, "")
	_, err := resolveCaptureTarget("no-such-bin")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "gp") || !strings.Contains(err.Error(), "never been fetched") {
		t.Errorf("the capture error must name the pack and why: %v", err)
	}
}

// `yolo config promote` resolves the pack fold through the same loader, so an INSTALLED git
// pack is in the fold — its config-overlay can outrank a promotion — and an absent one is
// named with its reason in the report rather than as "fetched packs need `yolo pack install`".
func TestPromoteFoldResolvesAnInstalledGitPack(t *testing.T) {
	repo := gitPackRepo(t)
	gitPackHome(t, "git+file://"+repo+"//tools/agent-pack?ref=main", "")
	installGitPack(t)

	fold, unresolved := loadPromoteFold()
	if len(unresolved) != 0 {
		t.Fatalf("an installed git pack came back unresolved: %+v", unresolved)
	}
	if _, ok := fold.order["gp"]; !ok {
		t.Errorf("the installed git pack is not in promote's fold: %v", fold.order)
	}
}

func TestPromoteReportNamesWhyAPackIsUnresolvable(t *testing.T) {
	gitPackHome(t, neverFetchedGitSource, "")
	_, unresolved := loadPromoteFold()
	if len(unresolved) != 1 || unresolved[0].Name != "gp" || !unresolved[0].NeedsInstall {
		t.Fatalf("unresolved = %+v, want gp needing `yolo pack install`", unresolved)
	}
	var out bytes.Buffer
	writePromoteReport(richtext.Printer{W: &out}, promotePlan{Unresolved: unresolved})
	if !strings.Contains(out.String(), "gp") || !strings.Contains(out.String(), "never been fetched") {
		t.Errorf("the promote report must name the pack and why:\n%s", out.String())
	}
}

// A FETCHED TREE IS HELD TO THE LAUNCH'S STAGING RULE. The host notch reads a pack in place, so
// without the check a third-party repo's escaping symlink would copy a host file into a skill the
// user's agents read. The launch refuses that pack (packstage's NO-ESCAPE rule); host apply makes
// it unresolvable, so an --assert refuses and writes nothing.
func TestApplyHostRefusesAFetchedPackWithAnEscapingSymlink(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	writeFile(t, secret, "TOP-SECRET-HOST-FILE\n")
	repo := gitPackRepoWith(t, map[string]string{
		"tools/agent-pack/skills/leak/SKILL.md": "symlink:" + secret,
	})
	home := gitPackHome(t, "git+file://"+repo+"//tools/agent-pack?ref=main", "")
	installGitPack(t)
	before := hashTree(t, home)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Fatalf("a fetched pack with an escaping symlink must make the apply refuse\n%s", report)
	}
	if !strings.Contains(report, "symlink pointing outside the pack") {
		t.Errorf("the refusal must carry the staging rule's reason:\n%s", report)
	}
	if hashTree(t, home) != before {
		t.Errorf("the refused apply wrote into the home:\n%s", report)
	}
}
