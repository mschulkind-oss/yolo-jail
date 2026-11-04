package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// captureStd runs fn with os.Stdout and os.Stderr captured.
func captureStd(t *testing.T, fn func() int) (int, string, string) {
	t.Helper()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	rc := fn()
	os.Stdout, os.Stderr = oldOut, oldErr
	outW.Close()
	errW.Close()
	out, _ := io.ReadAll(outR)
	errs, _ := io.ReadAll(errR)
	return rc, string(out), string(errs)
}

// TestApproveGitRequestAllowOnceAndRefusesFor drives `yolo approve` as the CLI runs it, over
// the git store the git-broker files into: a git push request is listed with each ref, old to
// new, marked where it needs approval; --for session is refused; Allow once grants that exact
// push once, through the store's live host authority and required audit.
func TestApproveGitRequestAllowOnceAndRefusesFor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	selectConfig := selectApprovePacks(t, home, "git")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	const remote = "git@fixture.test:o/r.git"
	sf := brokerscope.File{Source: gitbroker.Source, LaunchID: "registry-git", PID: os.Getpid(), Container: "registry-git-jail",
		Workspace: "/fixture", Form: brokerscope.FormRemoteURL, Repos: []string{remote},
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
	defer brokercore.CloseLease(gitbroker.Source, lease)

	const old, covered, updated = "1111111111111111111111111111111111111111", "3333333333333333333333333333333333333333", "2222222222222222222222222222222222222222"
	empty := sha256.Sum256(nil)
	s := gitbroker.ApprovalStore()
	q, err := s.File(brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: start, Workspace: sf.Workspace,
		LaunchID: sf.LaunchID, Argv: []string{"git-receive-pack", old + " " + updated + " refs/heads/main", old + " " + covered + " refs/heads/agent/x", "push-option=ci.skip"},
		Repo: "fixture.test/o/r", Set: "push", StdinDigest: hex.EncodeToString(empty[:]),
		Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 15 * time.Minute, Uses: 1}, NeedsApproval: []string{"refs/heads/main"}})
	if err != nil {
		t.Fatal(err)
	}

	rc, out, errs := captureStd(t, func() int { return registry["approve"]([]string{"approve"}) })
	if rc != 0 || !strings.Contains(out, q.ID) ||
		!strings.Contains(out, "refs/heads/main  111111111111 -> 222222222222  needs approval") ||
		!strings.Contains(out, "refs/heads/agent/x  111111111111 -> 333333333333  covered") ||
		!strings.Contains(out, "    push option: ci.skip\n") {
		t.Fatalf("listing: rc %d\n%s%s", rc, out, errs)
	}

	rc, out, errs = captureStd(t, func() int { return registry["approve"]([]string{"approve", "--json"}) })
	if rc != 0 || !strings.Contains(out, `"needs_approval":["refs/heads/main"]`) || !strings.Contains(out, `"push-option=ci.skip"`) {
		t.Fatalf("JSON listing: rc %d\n%s%s", rc, out, errs)
	}

	for _, args := range [][]string{{"approve", q.ID, "--for", "session"}} {
		rc, _, errs := captureStd(t, func() int { return registry["approve"](args) })
		if rc != approveExitPolicy || !strings.Contains(errs, "not available for git pushes") || !strings.Contains(errs, "yolo approve "+q.ID) {
			t.Fatalf("%v: rc %d, %q", args, rc, errs)
		}
	}
	if rs, gs, err := s.List(); err != nil || len(gs) != 0 || rs[0].State != "pending" {
		t.Fatalf("refused decisions changed the store: %+v %+v %v", rs, gs, err)
	}

	rc, out, errs = captureStd(t, func() int { return registry["approve"]([]string{"approve", q.ID}) })
	if rc != 0 || !strings.Contains(out, "this exact command, once") {
		t.Fatalf("Allow once: rc %d\n%s%s", rc, out, errs)
	}
	_, gs, err := s.List()
	if err != nil || len(gs) != 1 || gs[0].Digest != q.Digest || gs[0].Uses != 1 || gs[0].Set != "push" ||
		gs[0].Expires.Sub(gs[0].Expires.Add(-15*time.Minute)) != 15*time.Minute {
		t.Fatalf("grant %+v %v", gs, err)
	}
	audit, err := os.ReadFile(paths.BrokerAuditLog())
	if err != nil || !bytes.Contains(audit, []byte(`"event":"decision"`)) || !bytes.Contains(audit, []byte(`"service":"git"`)) {
		t.Fatalf("audit %s %v", audit, err)
	}

	// Removing git revokes CLI approval authority even while this launch lease stays alive.
	writeApprovePackFile(t, selectConfig, `{"packs":[]}`)
	removed := s
	for _, flags := range [][]string{{}, {"--for", "15m"}, {"--add"}, {"--remember"}} {
		q, err := removed.File(brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: start, Workspace: sf.Workspace,
			LaunchID: sf.LaunchID, Argv: []string{"git-receive-pack", old + " " + updated + " refs/heads/agent/removed"},
			Repo: "fixture.test/o/r", Set: "push", StdinDigest: hex.EncodeToString(empty[:]),
			Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 24 * time.Hour, Uses: 1}, NeedsApproval: []string{"refs/heads/agent/removed"}})
		if err != nil {
			t.Fatal(err)
		}
		args := append([]string{"approve", q.ID}, flags...)
		if rc, _, errs := captureStd(t, func() int { return registry["approve"](args) }); rc != approveExitNotConfirmed || !strings.Contains(errs, "no active core approval authority") {
			t.Fatalf("removed live-lease git source accepted %v: rc=%d stderr=%s", flags, rc, errs)
		}
	}
	pending, _, err := removed.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range pending {
		if request.State == "pending" {
			if rc, _, errs := captureStd(t, func() int { return registry["approve"]([]string{"approve", request.ID, "--deny"}) }); rc != 0 {
				t.Fatalf("clean pending request %s: rc=%d stderr=%s", request.ID, rc, errs)
			}
		}
	}
	for _, aliasName := range []string{"custom-pack", gitbroker.Source} {
		selectApprovalAliasPack(t, home, gitbroker.Source, aliasName)
		assertSelectedAliasIsVisibleDenyOnly(t, gitbroker.Source)
		workspace := filepath.Join(t.TempDir(), "workspace")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		const policy = "[requests.git]\npush = [\"existing\"]\n"
		policyPath := filepath.Join(workspace, "yolo-jail.toml")
		if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
		aliasSF := sf
		aliasSF.LaunchID = "registry-git-alias-" + aliasName
		aliasSF.Container = "registry-git-alias-" + aliasName + "-jail"
		aliasSF.Workspace = workspace
		aliasStartDigest := sha256.Sum256([]byte("git approval alias " + aliasName))
		aliasStart := hex.EncodeToString(aliasStartDigest[:16])
		aliasScopePath, err := brokerscope.Write(aliasSF)
		if err != nil {
			t.Fatal(err)
		}
		_, aliasLease, err := brokercore.BindLease(gitbroker.Source, aliasScopePath, aliasSF, aliasStart)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { brokercore.CloseLease(gitbroker.Source, aliasLease) })
		for i, flags := range [][]string{{}, {"--for", "15m"}, {"--add"}, {"--remember"}} {
			ref := "refs/heads/agent/alias-" + aliasName + "-" + []string{"once", "for", "add", "remember"}[i]
			q, err := removed.File(brokerstore.Request{Jail: paths.JailShortHash(aliasSF.Container), Start: aliasStart, Workspace: workspace,
				LaunchID: aliasSF.LaunchID, Argv: []string{"git-receive-pack", old + " " + updated + " " + ref},
				Repo: "fixture.test/o/r", Set: "push", StdinDigest: hex.EncodeToString(empty[:]),
				Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 24 * time.Hour, Uses: 1}, NeedsApproval: []string{ref}})
			if err != nil {
				t.Fatal(err)
			}
			_, grantsBefore, err := removed.List()
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{"approve", q.ID}, flags...)
			if rc, _, errs := captureStd(t, func() int { return runApprove(args) }); rc != approveExitNotConfirmed || !strings.Contains(errs, "no active core approval authority") {
				t.Fatalf("custom selected pack %q restored live-lease git authority for %v: rc=%d stderr=%s", aliasName, flags, rc, errs)
			}
			requests, grantsAfter, err := removed.List()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(grantsBefore, grantsAfter) {
				t.Fatalf("custom selected pack %q changed grants for %v: before=%+v after=%+v", aliasName, flags, grantsBefore, grantsAfter)
			}
			for _, grant := range grantsAfter {
				if grant.Digest == q.Digest {
					t.Fatalf("custom selected pack %q granted request %s for %v: %+v", aliasName, q.ID, flags, grant)
				}
			}
			foundPending := false
			for _, request := range requests {
				if request.ID == q.ID && request.State == "pending" {
					foundPending = true
				}
			}
			if !foundPending {
				t.Fatalf("custom selected pack %q did not leave request %s pending for %v: %+v", aliasName, q.ID, flags, requests)
			}
			if got, err := os.ReadFile(policyPath); err != nil || string(got) != policy {
				t.Fatalf("custom selected pack %q changed persistent policy for %v: content=%q err=%v", aliasName, flags, got, err)
			}
			if rc, _, errs := captureStd(t, func() int { return runApprove([]string{"approve", q.ID, "--deny"}) }); rc != 0 {
				t.Fatalf("deny alias request %s: rc=%d stderr=%s", q.ID, rc, errs)
			}
		}
	}
}
