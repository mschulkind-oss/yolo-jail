package cli

// hostsupersession_test.go pins docs/design/reference-mismatch-diagnostics.md §7 step 4 at the
// HOST NOTCH: a selected pack's `supersedes` claim that matches no capability any loophole of
// the pack set serves refuses `yolo host --` and `yolo host env`, through the gate a jail launch
// refuses on (run.UnmatchedSupersessionRefusal), in the same words. One code path per concern,
// the notch as an input (docs/plans/notch-convergence.md NC-D1): at the host a claim retires a
// loophole's doorway, so a mistyped one leaves that doorway open exactly as it leaves a jail's
// loophole running, and `yolo check`'s [FAIL] row says "The launch refuses this." of both.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// supersedingHostPack writes a local pack named name superseding capability and returns the
// user config selecting it beside the shipped claude pack, whose broker serves
// claude-oauth-refresh.
func supersedingHostPack(t *testing.T, name, capability string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(
		`{"name":"`+name+`","description":"d","supersedes":[`+
			`{"capability":"`+capability+`","because":"Bedrock overrides the OAuth path"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	return `{"packs": ["claude", {"source":"file://` + dir + `","name":"` + name + `"}]}`
}

// hostTypoPack names the refusing test's pack, and so its sentence, which no other test in this
// package says: loopholes' warnf says each distinct line once per process, so a sentence some
// earlier test had already warned would be silent here, and the "once" count below would pass
// with the gate misplaced.
const hostTypoPack = "claude-bedrock-host-typo"

const hostTypoSentence = "pack '" + hostTypoPack + "' supersedes capability 'claude-oauth-refersh', " +
	"which NO loophole on this machine serves"

// TestHostLaunchRefusesAnUnmatchedSupersession: `yolo host --` refuses before the exec, and
// `yolo host env` refuses printing nothing to eval, each with the sentence once, the
// did-you-mean and the remedy. Under `-p bedrock`, so the launch asks for aws-auth's doorway:
// that plan discovers loopholes, whose discovery warns the same sentence, and a gate placed
// after it would print the finding twice. Discovery warns on the PROCESS's stderr
// (loopholes.warnf), not on the writer hostMain is handed, so the count reads both.
func TestHostLaunchRefusesAnUnmatchedSupersession(t *testing.T) {
	cfg := supersedingHostPack(t, hostTypoPack, "claude-oauth-refersh")
	var rc int
	var env map[string]string
	var errs string
	discovery := captureStderr(t, func() {
		rc, env, errs = hostGateRun(t, cfg, nil, []string{"-p", "bedrock"}, "claude")
	})
	if n := strings.Count(errs+discovery, hostTypoSentence); n != 1 {
		t.Errorf("yolo host --: the sentence appears %d times across the launch's stderr and the "+
			"process's, want once (the refusal only, never first as discovery's warning):\n%s\n%s",
			n, errs, discovery)
	}
	if rc == 0 || env != nil {
		t.Fatalf("yolo host -- claude with a supersession that retires nothing must refuse "+
			"before the exec: rc=%d\n%s", rc, errs)
	}

	hostGateHome(t, cfg, nil)
	var out, envErr bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude"}, &out, &envErr, false, nil); rc == 0 || out.Len() != 0 {
		t.Fatalf("yolo host env with a supersession that retires nothing must refuse, printing "+
			"nothing to eval: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), envErr.String())
	}

	for verb, got := range map[string]string{"yolo host --": errs, "yolo host env": envErr.String()} {
		for _, want := range []string{
			"a selected pack's `supersedes` names a capability no loophole of this launch serves",
			hostTypoSentence,
			"did you mean 'claude-oauth-refresh'?",
			loopholes.UnmatchedSupersessionRemedy,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: the refusal is missing %q:\n%s", verb, want, got)
			}
		}
		if n := strings.Count(got, hostTypoSentence); n != 1 {
			t.Errorf("%s: the sentence appears %d times in the refusal, want once:\n%s", verb, n, got)
		}
		if strings.Contains(got, "Refusing to launch: a selected pack") {
			t.Errorf("%s: the jail's header leaked into the host's own refusal prefix:\n%s", verb, got)
		}
	}
}

// TestHostLaunchAcceptsAMatchedSupersession: the same pack with the capability spelled right
// reaches the exec. A gate that refused a correct claim would refuse every host launch that
// supersedes.
func TestHostLaunchAcceptsAMatchedSupersession(t *testing.T) {
	rc, env, errs := hostGateRun(t, supersedingHostPack(t, "claude-bedrock", "claude-oauth-refresh"), nil, nil, "claude")
	if rc != 0 || env == nil {
		t.Fatalf("a matched supersession must launch: rc=%d\n%s", rc, errs)
	}
	if strings.Contains(errs, "supersedes capability") {
		t.Errorf("a matched claim was reported:\n%s", errs)
	}
}

// TestHostLaunchRefusalNamesProvenSkew is RM-D2 at the host notch: when the running yolo is
// provably older than the source tree it resolves, the refusal says so and names
// `just install`, as the jail's and `yolo check`'s do. The packs a host yolo ships are the ones
// compiled into it at this notch too.
func TestHostLaunchRefusalNamesProvenSkew(t *testing.T) {
	root, installed, head := hostSkewRepo(t)
	cfg := supersedingHostPack(t, "claude-bedrock", "claude-oauth-refersh")
	rc, env, errs := hostGateRun(t, cfg, map[string]string{"YOLO_REPO_ROOT": root}, nil, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("the launch must refuse: rc=%d\n%s", rc, errs)
	}
	for _, want := range []string{
		"This yolo is older than the source tree it builds from",
		"yolo: " + installed[:8],
		"tree: " + head[:8],
		"(cd " + root + " && just install)",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("the host refusal is missing %q:\n%s", want, errs)
		}
	}
}

// hostSkewRepo builds a checkout reporoot.Resolve accepts (a go.mod) whose HEAD has moved past
// the commit this binary is stamped with, through internal/ (a path version.SourceSkew
// compares), and returns its root and both full commits. The stamp is restored at cleanup.
func hostSkewRepo(t *testing.T) (root, installed, head string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		full := append([]string{"-c", "user.email=test@example.com", "-c", "user.name=test"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = root
		// The machine's git configuration is not the fixture's (HermeticGitEnv).
		cmd.Env = testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ()))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("go.mod", "module example.com/x\n")
	write("internal/x/x.go", "package x // installed\n")
	git("add", "-A")
	git("commit", "-q", "-m", "installed")
	installed = git("rev-parse", "HEAD")
	write("internal/x/x.go", "package x // moved on\n")
	git("add", "-A")
	git("commit", "-q", "-m", "moved on")
	head = git("rev-parse", "HEAD")

	orig := version.GitCommit
	t.Cleanup(func() { version.GitCommit = orig })
	version.GitCommit = installed
	return root, installed, head
}
