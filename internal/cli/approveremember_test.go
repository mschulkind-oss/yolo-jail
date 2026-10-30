package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// filePushRequest files a pending git push request for workspace in the git store the broker
// files into, under a live scope file and lease, so `yolo approve` revalidates it as on a host.
// needs is the refs the push needs approval for.
func filePushRequest(t *testing.T, workspace string, needs ...string) (*brokerstore.Store, brokerstore.Request) {
	t.Helper()
	const remote = "git@fixture.test:o/r.git"
	sf := brokerscope.File{Source: gitbroker.Source, LaunchID: "remember-git", PID: os.Getpid(), Container: "remember-git-jail",
		Workspace: workspace, Form: brokerscope.FormRemoteURL, Repos: []string{remote},
		Grants: []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: "*"}}}
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	const start = "0123456789abcdef0123456789abcdef"
	_, lease, err := brokercore.BindLease(gitbroker.Source, path, sf, start)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { brokercore.CloseLease(gitbroker.Source, lease) })
	const old, updated = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	argv := []string{"git-receive-pack"}
	for _, ref := range needs {
		argv = append(argv, old+" "+updated+" "+ref)
	}
	empty := sha256.Sum256(nil)
	s := gitbroker.ApprovalStore()
	q, err := s.File(brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: start, Workspace: workspace,
		LaunchID: sf.LaunchID, Argv: argv, Repo: "fixture.test/o/r", Set: "push", StdinDigest: hex.EncodeToString(empty[:]),
		Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 15 * time.Minute, Uses: 1}, NeedsApproval: needs})
	if err != nil {
		t.Fatal(err)
	}
	return s, q
}

func requestState(t *testing.T, s *brokerstore.Store, id string) string {
	t.Helper()
	rs, _, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range rs {
		if q.ID == id {
			return q.State
		}
	}
	t.Fatalf("no request %s", id)
	return ""
}

// --remember, through the registered verb: the push is allowed once, and each ref it needed
// approval for is written into the REQUEST'S workspace (not the cwd's) by the request verb's
// own path, a branch under push and a tag under push_tags, with the command equivalent named.
func TestApproveRememberWritesTheRequestAndAllowsOnce(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\", \"push_tags\"]\n")
	withConfigEditCWD(t, t.TempDir())
	s, q := filePushRequest(t, fx.workspace, "refs/heads/main", "refs/tags/v1")

	rc, out, errs := captureStd(t, func() int { return registry["approve"]([]string{"approve", q.ID, "--remember"}) })
	t.Logf("--remember:\n%s%s", out, errs)
	if rc != 0 {
		t.Fatalf("--remember: rc %d\nstdout=%s\nstderr=%s", rc, out, errs)
	}
	got := readFile(t, fx.project)
	for _, want := range []string{"[requests.git]", `push = ["main"]`, `push_tags = ["v1"]`} {
		if !strings.Contains(got, want) {
			t.Fatalf("the project file lacks %q:\n%s\nstdout=%s\nstderr=%s", want, got, out, errs)
		}
	}
	for _, want := range []string{"YOLO Jail Check", "Allowed " + q.ID + ": one exact command, 1 use(s)",
		"Remembered main in " + fx.project, "`yolo request git push main`", "`yolo request git push_tags v1`",
		"run `yolo config apply` in " + fx.workspace} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s\nstderr=%s", want, out, errs)
		}
	}
	if strings.Contains(out, "runs without it") {
		t.Fatalf("a request within the ceiling was reported as skipped:\n%s", out)
	}
	if st := requestState(t, s, q.ID); st == "pending" {
		t.Fatalf("the request is still pending after --remember")
	}
	_, gs, err := s.List()
	if err != nil || len(gs) != 1 || gs[0].Digest != q.Digest || gs[0].Uses != 1 {
		t.Fatalf("Allow once grant: %+v %v", gs, err)
	}
}

// A request outside the user's ceiling surfaces the request verb's own report, which names
// `yolo allow`; the human's answer to this push still stands.
func TestApproveRememberOutsideTheCeilingNamesYoloAllow(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.git]\nalways = [\"fetch\"]\n")
	_, q := filePushRequest(t, fx.workspace, "refs/heads/main")
	rc, out, errs := captureStd(t, func() int { return registry["approve"]([]string{"approve", q.ID, "--remember"}) })
	if rc != 0 {
		t.Fatalf("--remember: rc %d\nstdout=%s\nstderr=%s", rc, out, errs)
	}
	for _, want := range []string{"runs without it", "yolo allow git on_request push", "Remembered main"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s\nstderr=%s", want, out, errs)
		}
	}
}

// A workspace still on the legacy JSONC format refuses --remember with the exact conversion
// step, writes nothing and decides nothing.
func TestApproveRememberRefusesALegacyJSONCWorkspace(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
	if err := os.Remove(fx.project); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(fx.workspace, "yolo-jail.jsonc")
	const body = "{\n  // a legacy project\n}\n"
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, q := filePushRequest(t, fx.workspace, "refs/heads/main")
	rc, out, errs := captureStd(t, func() int { return registry["approve"]([]string{"approve", q.ID, "--remember"}) })
	want := "yolo approve: " + legacy + " is still in the deprecated JSONC format, and remembering a grant needs the TOML format. " +
		"Convert it first: `yolo config migrate` (preview with `yolo config migrate --dry-run`), then run this command again. Nothing was written.\n"
	if rc != approveExitPolicy || errs != want || out != "" {
		t.Fatalf("rc %d, want %d\nstderr=%q\nwant  =%q\nstdout=%q", rc, approveExitPolicy, errs, want, out)
	}
	if got := readFile(t, legacy); got != body {
		t.Fatalf("the legacy file changed:\n%s", got)
	}
	if _, err := os.Stat(fx.project); !os.IsNotExist(err) {
		t.Fatalf("a TOML file was written beside the legacy one: %v", err)
	}
	if st := requestState(t, s, q.ID); st != "pending" {
		t.Fatalf("the refused --remember decided the request: %s", st)
	}
}

// --remember is one answer among Deny, --for and --add, and needs a request.
func TestApproveRememberExcludesOtherAnswers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	for _, args := range [][]string{
		{"approve", "r-x", "--remember", "--deny"},
		{"approve", "r-x", "--remember", "--for", "15m"},
		{"approve", "r-x", "--remember", "--add"},
		{"approve", "--remember"},
		{"approve", "r-x", "--remember=yes"},
	} {
		rc, _, errs := captureStd(t, func() int { return registry["approve"](args) })
		if rc != approveExitUsage {
			t.Fatalf("%v: rc %d, want %d: %s", args, rc, approveExitUsage, errs)
		}
	}
}

// A branch declared in a TOML project's config: the --revoke refusal names the exact
// `yolo config unset` that removes it, and that command, run as printed, does.
func TestApproveRevokeOfAConfigBranchNamesTheUnsetVerb(t *testing.T) {
	fx := newNamedVerbFixture(t, "[requests.git]\npush = [\"feat\", \"main\"]\n", "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
	f := brokerscope.File{Source: gitbroker.Source, LaunchID: "cfgunset", Workspace: fx.workspace, Container: "c", PID: os.Getpid(),
		Form: brokerscope.FormRemoteURL, Repos: []string{"git@github.com:o/r.git"}, ConfigFile: "yolo-jail.jsonc",
		Grants: []brokerscope.Grant{{Kind: brokerscope.KindBranch, Repo: "*", Branch: "main"}}}
	path, err := brokerscope.Write(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := brokerscope.BindBroker(path, f, "start", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	listing := captureStdout(t, func() {
		if rc := registry["approve"]([]string{"approve"}); rc != 0 {
			t.Fatalf("list: %d", rc)
		}
	})
	var id string
	for _, l := range strings.Split(listing, "\n") {
		if strings.Contains(l, "refs/heads/main") && strings.Contains(l, "from config") {
			id = strings.Fields(l)[0]
			// The launch records the legacy name; the listing names the file the project holds.
			if !strings.Contains(l, fx.project) {
				t.Fatalf("the listing does not name the TOML file %s: %q", fx.project, l)
			}
		}
	}
	if id == "" {
		t.Fatalf("no config branch listed:\n%s", listing)
	}
	var rc int
	refusal := captureStderr(t, func() { rc = registry["approve"]([]string{"approve", "--revoke", id}) })
	t.Logf("revoke:\n%s", refusal)
	const unset = "yolo config unset 'requests.git.push[1]' --project"
	if rc != approveExitPolicy || !strings.Contains(refusal, unset) || !strings.Contains(refusal, "yolo config apply") ||
		strings.Contains(refusal, "brokered.git.grants") {
		t.Fatalf("revoke %s: rc %d\n%s", id, rc, refusal)
	}
	var out, errw bytes.Buffer
	if rc := runConfigEditWith([]string{"unset", "requests.git.push[1]", "--project"}, &out, &errw, fx.workspace); rc != 0 {
		t.Fatalf("the named verb refused: rc %d\n%s%s", rc, out.String(), errw.String())
	}
	if got := readFile(t, fx.project); strings.Contains(got, "main") || !strings.Contains(got, "feat") {
		t.Fatalf("the named verb did not remove main alone:\n%s", got)
	}
}
