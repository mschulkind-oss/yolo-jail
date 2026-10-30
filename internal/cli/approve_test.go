package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func approvalFixture(t *testing.T) (*brokerstore.Store, brokerstore.Request) {
	t.Helper()
	s := &brokerstore.Store{Dir: t.TempDir(), Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
	r, err := s.File(brokerstore.Request{Jail: "j", Start: "s", Workspace: "/w\x1b[2J", Argv: []string{"pr", "merge", "1"}, Repo: "o/r", StdinDigest: "stdin", Set: "read-write", Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 25}})
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}
func TestGitHubApproveRefusesSetWideDecisionsButOtherSourcesKeepThem(t *testing.T) {
	newRequest := func(t *testing.T, source string) (*brokerstore.Store, brokerstore.Request) {
		t.Helper()
		s := &brokerstore.Store{Dir: t.TempDir(), Source: source, Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
		q, err := s.File(brokerstore.Request{Jail: "j", Start: "s", Workspace: "/code/app", Argv: []string{"pr", "merge", "1"}, Repo: "o/r", StdinDigest: "stdin", Set: "read-write", Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 25}})
		if err != nil {
			t.Fatal(err)
		}
		return s, q
	}

	gh, request := newRequest(t, ghbroker.Source)
	for _, args := range [][]string{{request.ID, "--for", "15m"}, {request.ID, "--add"}} {
		var out, errs bytes.Buffer
		if rc := approveMain(context.Background(), args, false, gh, &out, &errs); rc != approveExitPolicy || !strings.Contains(errs.String(), "Allow once") || !strings.Contains(errs.String(), "yolo approve "+request.ID) || strings.Contains(errs.String(), `\\n`) || !strings.Contains(errs.String(), "\n") {
			t.Fatalf("GitHub set-wide decision %v: rc %d, out %q, err %q", args, rc, out.String(), errs.String())
		}
	}
	requests, grants, err := gh.List()
	if err != nil || len(requests) != 1 || requests[0].State != "pending" || len(grants) != 0 {
		t.Fatalf("GitHub request changed after refused decisions: requests=%+v grants=%+v err=%v", requests, grants, err)
	}
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), []string{request.ID}, false, gh, &out, &errs); rc != 0 {
		t.Fatalf("GitHub Allow once: rc %d, err %q", rc, errs.String())
	}
	_, grants, err = gh.List()
	if err != nil || len(grants) != 1 || grants[0].Digest != request.Digest || grants[0].Uses != 1 {
		t.Fatalf("GitHub Allow once was not exact: %+v err=%v", grants, err)
	}

	// A source with no Allow-once-only rule (both shipped sources have one now) keeps them.
	git, gitRequest := newRequest(t, "other")
	out.Reset()
	errs.Reset()
	if rc := approveMain(context.Background(), []string{gitRequest.ID, "--for", "15m"}, false, git, &out, &errs); rc != 0 {
		t.Fatalf("other source --for semantics changed: rc %d, err %q", rc, errs.String())
	}
	_, grants, err = git.List()
	if err != nil || len(grants) != 1 || grants[0].Digest != "" {
		t.Fatalf("other source set-wide grant changed: %+v err=%v", grants, err)
	}
}

func TestApproveEmptyListDescribesLiveGitHubRequestPath(t *testing.T) {
	store := &brokerstore.Store{Dir: t.TempDir(), Source: ghbroker.Source}
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), nil, false, store, &out, &errs); rc != 0 {
		t.Fatalf("empty listing: rc %d, err %q", rc, errs.String())
	}
	if !strings.Contains(out.String(), "can file a pending Allow-once request") ||
		strings.Contains(out.String(), "Brokers do not file requests") || strings.Contains(out.String(), "refuse without one") {
		t.Fatalf("empty listing misstates the live GitHub request path: %q", out.String())
	}
}

func TestApproveOperatorPaths(t *testing.T) {
	s, r := approvalFixture(t)
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), nil, false, s, &out, &errs); rc != 0 || !strings.Contains(out.String(), r.ID) || strings.Contains(out.String(), "\x1b") {
		t.Fatalf("list %d %q %q", rc, out.String(), errs.String())
	}
	out.Reset()
	if rc := approveMain(context.Background(), []string{r.ID, "--for", "2h"}, false, s, &out, &errs); rc == 0 {
		t.Fatal("exceeded ceiling")
	}
	if rc := approveMain(context.Background(), []string{r.ID}, false, s, &out, &errs); rc != 0 || !strings.Contains(out.String(), "expires") {
		t.Fatalf("allow %d %q", rc, errs.String())
	}
	_, gs, _ := s.List()
	if len(gs) != 1 {
		t.Fatal(gs)
	}
	if rc := approveMain(context.Background(), []string{"--revoke", gs[0].ID}, false, s, &out, &errs); rc != 0 {
		t.Fatal(errs.String())
	}
	if _, ok, _ := s.Consume(r.Workspace, "j", "read-write", r.Digest, s.Revalidate); ok {
		t.Fatal("revoked grant consumed")
	}
	for _, args := range [][]string{{r.ID, "--deny", "--for", "1m"}, {"--for", "1m"}, {"--revoke", "all", r.ID}, {"--for", "0s", r.ID}, {"--nonsense"}} {
		if rc := approveMain(context.Background(), args, false, s, &out, &errs); rc == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}

// A grant is shown by its holder, the workspace (boundary-broker.md BB-D99): the allow line, the
// listing and --json name it, and --json keeps the filing jail and the jail that used it.
func TestApproveShowsAGrantsWorkspace(t *testing.T) {
	s := &brokerstore.Store{Dir: t.TempDir(), Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
	r, err := s.File(brokerstore.Request{Jail: "j", Start: "s", Workspace: "/code/app", Argv: []string{"pr", "merge", "1"}, Repo: "o/r", StdinDigest: "stdin", Set: "read-write", Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 25}})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), []string{r.ID, "--for", "15m"}, false, s, &out, &errs); rc != 0 ||
		!strings.Contains(out.String(), "every jail of /code/app") || !strings.Contains(out.String(), "across restarts") {
		t.Fatalf("allow %d %q %q", rc, out.String(), errs.String())
	}
	if _, ok, err := s.Consume("/code/app", "another-jail", "read-write", r.Digest, s.Revalidate); err != nil || !ok {
		t.Fatal(ok, err)
	}
	out.Reset()
	if rc := approveMain(context.Background(), nil, false, s, &out, &errs); rc != 0 || !strings.Contains(out.String(), "[/code/app]  set read-write") {
		t.Fatalf("list %d %q", rc, out.String())
	}
	out.Reset()
	if rc := approveMain(context.Background(), []string{"--json"}, false, s, &out, &errs); rc != 0 {
		t.Fatal(errs.String())
	}
	var state brokerstore.StateView
	if err := json.Unmarshal(out.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Grants) != 1 || state.Grants[0].Workspace != "/code/app" || state.Grants[0].Jail != "j" || state.Grants[0].UsedBy != "another-jail" {
		t.Fatalf("--json grants %+v\n%s", state.Grants, out.String())
	}
}

func TestApproveDenyAndJailRefusal(t *testing.T) {
	s, r := approvalFixture(t)
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), []string{r.ID, "--deny"}, false, s, &out, &errs); rc != 0 {
		t.Fatal(errs.String())
	}
	rs, gs, _ := s.List()
	if rs[0].State != "denied" || len(gs) != 0 {
		t.Fatal(rs, gs)
	}
	out.Reset()
	errs.Reset()
	if rc := approveMain(context.Background(), nil, true, s, &out, &errs); rc != 2 || out.Len() != 0 || !strings.Contains(errs.String(), "host-only") {
		t.Fatal(rc, out.String(), errs.String())
	}
}
func TestApproveThroughRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	s := &brokerstore.Store{Dir: paths.BrokerSourceDir("github")}
	r, err := s.File(brokerstore.Request{Jail: "j", Start: "s", Workspace: "/w", Argv: []string{"pr", "merge"}, Repo: "o/r", StdinDigest: "stdin", Set: "read-write", Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Hour, Uses: 2}})
	if err != nil {
		t.Fatal(err)
	}
	handler, ok := registry["approve"]
	if !ok {
		t.Fatal("approve not dispatched")
	}
	if rc := handler([]string{"approve", r.ID, "--deny"}); rc != 0 {
		t.Fatal(rc)
	}
	rs, _, err := s.List()
	if err != nil || rs[0].State != "denied" {
		t.Fatal(rs, err)
	}
}

func TestApproveRegistryUsesCurrentHostAuthorityAndRequiredAudit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	sf := brokerscope.File{Source: "github", LaunchID: "registry", PID: os.Getpid(), Container: "registry-jail", Workspace: "/fixture", Repos: []string{"o/r"}, Grants: []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: "*"}}}
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ghbroker.BindAuthority(path, sf, "registry-start")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	d, policy, err := a.Classify([]string{"pr", "comment", "1", "--body", "hi"}, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	s := &brokerstore.Store{Dir: paths.BrokerSourceDir("github"), Revalidate: a.Revalidate}
	q, err := s.File(brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: "registry-start", Workspace: sf.Workspace, LaunchID: sf.LaunchID, Argv: d.Argv, Repo: "o/r", Set: d.Set, Policy: policy, StdinDigest: "stdin"})
	if err != nil {
		t.Fatal(err)
	}
	if code := registry["approve"]([]string{"approve", q.ID}); code != 0 {
		t.Fatal(code)
	}
	_, gs, err := s.List()
	if err != nil || len(gs) != 1 {
		t.Fatal(gs, err)
	}
	audit, err := os.ReadFile(paths.BrokerAuditLog())
	if err != nil || !bytes.Contains(audit, []byte(`"event":"decision"`)) {
		t.Fatal(string(audit), err)
	}
	// A second filed command cannot be approved when its actual broker lease ended,
	// even if both recorded PIDs are still live (PID reuse cannot grant authority).
	d, policy, err = a.Classify([]string{"pr", "comment", "2", "--body", "hi"}, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	q, err = s.File(brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: "registry-start", Workspace: sf.Workspace, LaunchID: sf.LaunchID, Argv: d.Argv, Repo: "o/r", Set: d.Set, Policy: policy, StdinDigest: "stdin"})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if code := registry["approve"]([]string{"approve", q.ID}); code == 0 {
		t.Fatal("approved dead lifetime")
	}
}

// A request whose on_request failed says so wherever it is listed: the plain listing, with
// the one line, and the JSON, with the line and the output.
func TestAFailedHookIsListedWithItsRequest(t *testing.T) {
	s, r := approvalFixture(t)
	if err := s.NoteHookFailure(r.ID, "hook exited 127: sh: notify-send: not found", "sh: notify-send: not found\n"); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), nil, false, s, &out, &errs); rc != 0 ||
		!strings.Contains(out.String(), "your on_request failed, so you may not have been notified: hook exited 127: sh: notify-send: not found") {
		t.Fatalf("list %d %q %q", rc, out.String(), errs.String())
	}
	out.Reset()
	if rc := approveMain(context.Background(), []string{"--json"}, false, s, &out, &errs); rc != 0 ||
		!strings.Contains(out.String(), `"hook_failure":"hook exited 127: sh: notify-send: not found"`) ||
		!strings.Contains(out.String(), `"hook_output":"sh: notify-send: not found\n"`) {
		t.Fatalf("json %d %q %q", rc, out.String(), errs.String())
	}
}

// --- the working set in `yolo approve` (docs/design/git-broker-plan.md task 9, BB-D86) ---

func workingStore(dir string) *brokerstore.Store {
	return &brokerstore.Store{Dir: dir, Source: "git"}
}

func addEntry(t *testing.T, s *brokerstore.Store, ws, ref string) brokerstore.WorkingEntry {
	t.Helper()
	e, _, err := s.AddWorking(brokerstore.WorkingEntry{Workspace: ws, Repo: "github.com/o/r", Kind: brokerstore.WorkingBranch,
		Resource: ref, By: "create-branch"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestApproveListsWorkingSet(t *testing.T) {
	gh, _ := approvalFixture(t)
	ws := workingStore(t.TempDir())
	e := addEntry(t, ws, "/home/u/code/app", "refs/heads/agent/x")
	stores := []*brokerstore.Store{gh, ws}
	var out, errs bytes.Buffer
	if rc := approveAll(context.Background(), nil, false, stores, &out, &errs); rc != 0 {
		t.Fatalf("list %d %q", rc, errs.String())
	}
	t.Logf("listing:\n%s", out.String())
	var line string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, e.ID) {
			line = l
		}
	}
	for _, want := range []string{"git", "/home/u/code/app", "github.com/o/r", "refs/heads/agent/x", "create-branch"} {
		if !strings.Contains(line, want) {
			t.Fatalf("working-set line %q lacks %q in:\n%s", line, want, out.String())
		}
	}
	if !strings.Contains(out.String(), "w-entry") {
		t.Fatalf("the listing does not say how to revoke an entry:\n%s", out.String())
	}
	out.Reset()
	if rc := approveAll(context.Background(), []string{"--json"}, false, stores, &out, &errs); rc != 0 ||
		!strings.Contains(out.String(), `"working":[{`) || !strings.Contains(out.String(), `"id":"`+e.ID+`"`) ||
		!strings.Contains(out.String(), `"resource":"refs/heads/agent/x"`) {
		t.Fatalf("json %d %s %s", rc, out.String(), errs.String())
	}
	// An ended entry covers nothing, and is not listed.
	if err := ws.EndWorking(e.ID, brokerstore.EndDeleted); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	approveAll(context.Background(), nil, false, stores, &out, &errs)
	if strings.Contains(out.String(), e.ID) {
		t.Fatalf("an ended entry is listed:\n%s", out.String())
	}
}

// Through the registered verb, so the git store reaching `yolo approve` is pinned at its call
// site (runApprove).
func TestApproveRevokeWorkingID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	ws := workingStore(paths.BrokerSourceDir("git"))
	e := addEntry(t, ws, "/w", "refs/heads/agent/x")
	keep := addEntry(t, ws, "/w", "refs/heads/agent/y")
	if rc := registry["approve"]([]string{"approve", "--revoke", e.ID}); rc != 0 {
		t.Fatalf("revoke %s: %d", e.ID, rc)
	}
	if _, ok, _ := ws.LiveWorking("/w", e.Repo, e.Resource); ok {
		t.Fatal("revoked entry still live")
	}
	if _, ok, _ := ws.LiveWorking("/w", keep.Repo, keep.Resource); !ok {
		t.Fatal("revoking one entry ended another")
	}
	if rc := registry["approve"]([]string{"approve", "--revoke", "w-00000000000000000000000000000000"}); rc != approveExitUnknownID {
		t.Fatalf("unknown entry: %d, want %d", rc, approveExitUnknownID)
	}
}

// A working-set branch declared in config (docs/design/git-broker.md GB-D13) is listed beside the
// store's entries, marked as from config and naming the file, read from the scope file of the
// running broker that holds it; a --revoke of it refuses, naming the file to remove it from.
// Through the registered verb, so the scope-file read is pinned at its call site.
func TestApproveListsAndRefusesToRevokeAConfigBranch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	ws := "/home/u/code/app"
	f := brokerscope.File{Source: "git", LaunchID: "cfgbranch", Workspace: ws, Container: "c", PID: os.Getpid(),
		Form: brokerscope.FormRemoteURL, Repos: []string{"git@github.com:o/r.git"}, ConfigFile: "yolo-jail.jsonc",
		Grants: []brokerscope.Grant{{Kind: "branch", Repo: "github.com/o/r", Branch: "main"}, {Kind: "create-branch", Repo: "*"}}}
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
	t.Logf("listing:\n%s", listing)
	var line string
	for _, l := range strings.Split(listing, "\n") {
		if strings.Contains(l, "refs/heads/main") {
			line = l
		}
	}
	for _, want := range []string{"c-", "working set", "git", "[" + ws + "]", "github.com/o/r", "from config", ws + "/yolo-jail.jsonc"} {
		if !strings.Contains(line, want) {
			t.Fatalf("config branch line %q lacks %q in:\n%s", line, want, listing)
		}
	}
	if strings.Contains(listing, "create-branch *") || strings.Count(listing, "from config") != 1 {
		t.Fatalf("only the branch grant is a working-set branch:\n%s", listing)
	}
	id := strings.Fields(line)[0]
	var rc int
	refusal := captureStderr(t, func() { rc = registry["approve"]([]string{"approve", "--revoke", id}) })
	if rc != approveExitPolicy {
		t.Fatalf("revoke %s: %d, want %d\n%s", id, rc, approveExitPolicy, refusal)
	}
	for _, want := range []string{"declared in config", ws + "/yolo-jail.jsonc", "brokered.git.grants", "yolo config apply"} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, refusal)
		}
	}

	// --json carries the declared branch beside the store's entries, by "config", with its file.
	js := captureStdout(t, func() {
		if rc := registry["approve"]([]string{"approve", "--json"}); rc != 0 {
			t.Fatalf("--json: %d", rc)
		}
	})
	var state brokerstore.StateView
	if err := json.Unmarshal([]byte(js), &state); err != nil {
		t.Fatalf("--json: %v\n%s", err, js)
	}
	if len(state.Working) != 1 || state.Working[0].ID != id || state.Working[0].By != "config" ||
		state.Working[0].Resource != "refs/heads/main" || state.Working[0].File != ws+"/yolo-jail.jsonc" {
		t.Fatalf("--json working %+v, want the declared branch %s by config:\n%s", state.Working, id, js)
	}

	// --revoke all --workspace with only declared branches there: nothing in the store matched,
	// and the declared branches are named with the file to edit, not a loop back to the listing.
	allOut := captureStderr(t, func() { rc = registry["approve"]([]string{"approve", "--revoke", "all", "--workspace", ws}) })
	for _, want := range []string{"nothing in the store", "declared in config", ws + "/yolo-jail.jsonc", "brokered.git.grants", "refs/heads/main"} {
		if !strings.Contains(allOut, want) {
			t.Fatalf("--revoke all --workspace (rc %d) lacks %q:\n%s", rc, want, allOut)
		}
	}
	if strings.Contains(allOut, "give that path to --workspace") {
		t.Fatalf("--revoke all --workspace loops back to the listing:\n%s", allOut)
	}

	// A c- id no running jail declares (its jail stopped): said as a branch declared in config,
	// listed only while a jail of its project runs, never the store's 7-day text.
	unknown := captureStderr(t, func() {
		rc = registry["approve"]([]string{"approve", "--revoke", "c-00000000000000000000000000000000"})
	})
	if rc != approveExitUnknownID {
		t.Fatalf("unknown c- id: %d, want %d\n%s", rc, approveExitUnknownID, unknown)
	}
	for _, want := range []string{"declared in config", "while a jail of its project runs", "brokered.git.grants"} {
		if !strings.Contains(unknown, want) {
			t.Fatalf("unknown c- id lacks %q:\n%s", want, unknown)
		}
	}
	if strings.Contains(unknown, "7 days") {
		t.Fatalf("unknown c- id prints the store's ended-record text:\n%s", unknown)
	}
}

// A pattern branch grant and a tag grant (docs/design/git-broker.md GB-D25) are listed as the
// config declares them: the pattern under its namespace, the repository "*" as written, and a
// tag grant as a tag; --json carries each with its kind.
func TestApproveListsPatternAndTagGrants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	ws := "/home/u/code/app"
	f := brokerscope.File{Source: "git", LaunchID: "cfgpattern", Workspace: ws, Container: "c", PID: os.Getpid(),
		Form: brokerscope.FormRemoteURL, Repos: []string{"git@github.com:o/r.git"}, ConfigFile: "yolo-jail.jsonc",
		Grants: []brokerscope.Grant{{Kind: "branch", Repo: "*", Branch: "patch/*"}, {Kind: "tag", Repo: "github.com/o/r", Tag: "v*"}}}
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
	for _, want := range []string{"repo *  branch refs/heads/patch/*  from config", "repo github.com/o/r  tag refs/tags/v*  from config"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("the listing lacks %q:\n%s", want, listing)
		}
	}
	var tagID string
	for _, l := range strings.Split(listing, "\n") {
		if strings.Contains(l, "refs/tags/v*") {
			tagID = strings.Fields(l)[0]
		}
	}
	var rc int
	refusal := captureStderr(t, func() { rc = registry["approve"]([]string{"approve", "--revoke", tagID}) })
	if rc != approveExitPolicy || !strings.Contains(refusal, `{"kind": "tag", …}`) || !strings.Contains(refusal, "refs/tags/v*") {
		t.Fatalf("revoke %s: %d\n%s", tagID, rc, refusal)
	}
	js := captureStdout(t, func() {
		if rc := registry["approve"]([]string{"approve", "--json"}); rc != 0 {
			t.Fatalf("--json: %d", rc)
		}
	})
	var state brokerstore.StateView
	if err := json.Unmarshal([]byte(js), &state); err != nil {
		t.Fatalf("--json: %v\n%s", err, js)
	}
	got := map[string]string{}
	for _, w := range state.Working {
		got[w.Resource] = w.Kind + " " + w.Repo
	}
	if got["refs/heads/patch/*"] != "branch *" || got["refs/tags/v*"] != "tag github.com/o/r" {
		t.Fatalf("--json working %+v", state.Working)
	}
}

func TestApproveRevokeAllWorkspace(t *testing.T) {
	gh := &brokerstore.Store{Dir: t.TempDir(), Source: "github", Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
	grantIn := func(ws string) brokerstore.Grant {
		r, err := gh.File(brokerstore.Request{Jail: "j" + ws, Start: "s", Workspace: ws, Argv: []string{"pr", "merge"}, Repo: "o/r",
			StdinDigest: "d", Set: "read-write", Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 5}})
		if err != nil {
			t.Fatal(err)
		}
		g, err := gh.Decide(r.ID, "pending", brokerstore.Decision{For: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	root := t.TempDir()
	a, b := root+"/a", root+"/b"
	gA, gB := grantIn(a), grantIn(b)
	ws := workingStore(t.TempDir())
	eA, eB := addEntry(t, ws, a, "refs/heads/x"), addEntry(t, ws, b, "refs/heads/x")
	stores := []*brokerstore.Store{gh, ws}
	var out, errs bytes.Buffer
	if rc := approveAll(context.Background(), []string{"--revoke", "all", "--workspace", a + "/"}, false, stores, &out, &errs); rc != 0 {
		t.Fatalf("revoke all --workspace: %d %s", rc, errs.String())
	}
	// The output counts what it ended and says the config's create-branch grant stays, naming
	// both places that remove it.
	for _, want := range []string{"Revoked 2 ", "stays", "yolo config unset requests.git.<capability> --project", "allow.git.on_request"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("revoke output lacks %q:\n%s", want, out.String())
		}
	}
	_, gs, _ := gh.List()
	for _, g := range gs {
		if g.ID == gA.ID && g.EndedBy() != brokerstore.EndRevoked {
			t.Fatalf("workspace a's grant: %+v", g)
		}
		if g.ID == gB.ID && !g.Ended.IsZero() {
			t.Fatalf("workspace b's grant ended: %+v", g)
		}
	}
	if _, ok, _ := ws.LiveWorking(a, eA.Repo, eA.Resource); ok {
		t.Fatal("workspace a's entry still live")
	}
	if _, ok, _ := ws.LiveWorking(b, eB.Repo, eB.Resource); !ok {
		t.Fatal("workspace b's entry ended")
	}
	for _, args := range [][]string{{"--revoke", eB.ID, "--workspace", b}, {"--workspace", b}, {"--revoke", "all", "--workspace"}} {
		if rc := approveAll(context.Background(), args, false, stores, &out, &errs); rc != approveExitUsage {
			t.Fatalf("%v: %d, want usage", args, rc)
		}
	}
	if _, ok, _ := ws.LiveWorking(b, eB.Repo, eB.Resource); !ok {
		t.Fatal("a refused revoke ended an entry")
	}
}

// A --workspace path that matches nothing does not report success: it says nothing matched,
// points at the listing that shows each entry's workspace, and exits with the unknown-id code.
func TestApproveRevokeAllWorkspaceMatchingNothing(t *testing.T) {
	ws := workingStore(t.TempDir())
	root := t.TempDir()
	e := addEntry(t, ws, root+"/app", "refs/heads/x")
	var out, errs bytes.Buffer
	rc := approveAll(context.Background(), []string{"--revoke", "all", "--workspace", root + "/typo"}, false,
		[]*brokerstore.Store{ws}, &out, &errs)
	if rc != approveExitUnknownID {
		t.Fatalf("rc %d, want %d (out %q, errs %q)", rc, approveExitUnknownID, out.String(), errs.String())
	}
	if strings.Contains(out.String(), "Revoked") || !strings.Contains(errs.String(), "nothing to revoke") ||
		!strings.Contains(errs.String(), "Next: run `yolo approve`") {
		t.Fatalf("out %q errs %q", out.String(), errs.String())
	}
	if _, ok, _ := ws.LiveWorking(e.Workspace, e.Repo, e.Resource); !ok {
		t.Fatal("a revoke that matched nothing ended an entry")
	}
}
