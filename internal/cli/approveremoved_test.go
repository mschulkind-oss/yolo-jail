package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
)

// TestApproveRefusesTheRemovedAddAndRememberFlags: `yolo approve <id> --add` and `--remember`
// are removed (git-broker.md GB-D28, github-write-scopes.md OQ-GW2). An old invocation is
// refused with the usage exit, decides nothing, and names the answers that replace it: Allow
// once, `--for 15m`, and the project request then `yolo config apply` (every stop names the
// next step). It goes through approveMain, the verb's own entry, so deleting the refusal fails
// it: the old flags would otherwise answer the request or be a bare "unexpected argument".
func TestApproveRefusesTheRemovedAddAndRememberFlags(t *testing.T) {
	for _, source := range []string{gitbroker.Source, ghbroker.Source} {
		for _, flags := range [][]string{{"--add"}, {"--remember"}, {"--add=yes"}, {"--remember=1"}, {"--add", "--deny"}} {
			s := &brokerstore.Store{Dir: t.TempDir(), Source: source,
				Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil },
				Entries: func(q brokerstore.Request, add bool) ([]brokerstore.WorkingEntry, error) {
					return []brokerstore.WorkingEntry{{Workspace: q.Workspace, Repo: q.Repo, Kind: brokerstore.WorkingBranch, Resource: "refs/heads/main", By: "human"}}, nil
				}}
			q, err := s.File(brokerstore.Request{Jail: "j", Start: "s", Workspace: "/code/app", Argv: []string{"git-receive-pack", "a b refs/heads/main"},
				Repo: "o/r", StdinDigest: "stdin", Set: "push", NeedsApproval: []string{"refs/heads/main"},
				Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 1}})
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			args := append([]string{q.ID}, flags...)
			rc := approveMain(context.Background(), args, false, s, &out, &errs)
			flag, _, _ := strings.Cut(flags[0], "=")
			msg := errs.String()
			if rc != approveExitUsage {
				t.Errorf("%s %v: rc %d, want the usage exit %d\n%s%s", source, flags, rc, approveExitUsage, out.String(), msg)
			}
			for _, want := range []string{
				flag + " was removed",
				"Nothing was decided",
				"`yolo approve " + q.ID + "`",
				"`yolo approve " + q.ID + " --for 15m`",
				"`yolo request git push <branch>`",
				"`yolo config apply`",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("%s %v: the refusal does not name %q:\n%s", source, flags, want, msg)
				}
			}
			if strings.Contains(msg, "unexpected argument") {
				t.Errorf("%s %v: a bare usage error instead of the removal:\n%s", source, flags, msg)
			}
			requests, grants, err := s.List()
			if err != nil || len(requests) != 1 || requests[0].State != "pending" || len(grants) != 0 {
				t.Errorf("%s %v decided the request: %+v %+v %v", source, flags, requests, grants, err)
			}
			if snap, err := s.Snapshot(); err != nil || len(snap.Working) != 0 {
				t.Errorf("%s %v wrote a working-set entry: %+v %v", source, flags, snap.Working, err)
			}
		}
	}

	// Without a request id, the refusal still names the next step, with a placeholder.
	var out, errs bytes.Buffer
	if rc := approveMain(context.Background(), []string{"--remember"}, false, &brokerstore.Store{Dir: t.TempDir()}, &out, &errs); rc != approveExitUsage ||
		!strings.Contains(errs.String(), "--remember was removed") || !strings.Contains(errs.String(), "`yolo approve <id> --for 15m`") {
		t.Fatalf("--remember without a request: rc %d %q", rc, errs.String())
	}
}

// TestApproveUsageNamesNoRemovedFlag: the help's synopsis and examples no longer offer --add
// or --remember, and its prose says they are removed.
func TestApproveUsageNamesNoRemovedFlag(t *testing.T) {
	synopsis, _, _ := strings.Cut(approveUsage, "\n\n")
	_, examples, _ := strings.Cut(approveUsage, "\nExamples:\n")
	for _, flag := range []string{"--add", "--remember"} {
		if strings.Contains(synopsis, flag) || strings.Contains(examples, flag) {
			t.Errorf("yolo approve --help still offers %s:\n%s", flag, approveUsage)
		}
	}
	if !strings.Contains(approveUsage, "--add and --remember\nare removed") {
		t.Errorf("yolo approve --help does not say --add and --remember are removed:\n%s", approveUsage)
	}
}
