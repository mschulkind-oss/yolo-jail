package run

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// unifiedTreeSnapshot is every path beneath root with its content or link target, for a byte-identity check.
func unifiedTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case d.Type()&os.ModeSymlink != 0:
			tgt, _ := os.Readlink(p)
			out[rel] = "-> " + tgt
		case d.IsDir():
			out[rel] = "dir"
		default:
			b, _ := os.ReadFile(p)
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAGuestFirstLaunchLinksTheUnifiedChildren (UHG-D2, UHG-D6, UHG-D7): a guest's slice has no
// .claude yet; its launch makes it and links the unified children with a RELATIVE target.
func TestAGuestFirstLaunchLinksTheUnifiedChildren(t *testing.T) {
	f := newUnifyFixture(t, "seatbelt-self")
	putUnify(t, paths.UnifiedHome(f.ws), ".claude/projects/-workspace/w.jsonl", "s")
	f.unify(t, "macos-user")

	link := filepath.Join(f.home("seatbelt-self"), ".claude", "projects")
	if got, want := mustReadlink(t, link), "../../../../unified-home/.claude/projects"; got != want {
		t.Fatalf(".claude/projects -> %q, want %q", got, want)
	}
	got, err := filepath.EvalSymlinks(link)
	want, _ := filepath.EvalSymlinks(f.unified(".claude/projects"))
	if err != nil || got != want {
		t.Errorf("the link resolves to %q (%v), want %q", got, err, want)
	}
}

// TestAGuestSecondLaunchAdoptsWhatTheAgentWrote: a real session in the guest's dotted slice moves
// to the unified home, a link takes its place, and a generated file stays real.
func TestAGuestSecondLaunchAdoptsWhatTheAgentWrote(t *testing.T) {
	f := newUnifyFixture(t, "seatbelt-self")
	slice := f.home("seatbelt-self")
	putUnify(t, slice, ".claude/projects/-Users-Shared-yolo-uh/g.jsonl", "g")
	putUnify(t, slice, ".claude/settings.json", "generated")
	f.unify(t, "macos-user")

	if got := readFileT(t, f.unified(".claude/projects/-Users-Shared-yolo-uh/g.jsonl")); got != "g" {
		t.Fatalf("the session is not in the unified home: %q", got)
	}
	if _, err := os.Readlink(filepath.Join(slice, ".claude", "projects")); err != nil {
		t.Errorf(".claude/projects is not a link: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(slice, ".claude", "settings.json")); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("a generated file must stay real: %v", err)
	}
	line := regexp.MustCompile(`(?m)^Merged 1 files from 1 homes into this workspace's shared home\.$`)
	if !line.MatchString(f.stderr.String()) {
		t.Errorf("the merge line is missing:\n%s", f.stderr.String())
	}
}

// TestAStrandedGuestSliceMergesWhenNothingRuns: a stopped guest's slice merges into the unified
// home on a container launch; while its keeper runs it stays and the line names `yolo stop`.
func TestAStrandedGuestSliceMergesWhenNothingRuns(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	putUnify(t, f.home("seatbelt-self"), ".claude/projects/g.jsonl", "g")
	f.unify(t, "podman")
	if got := readFileT(t, f.unified(".claude/projects/g.jsonl")); got != "g" {
		t.Fatalf("a stopped guest's session did not merge: %q", got)
	}

	f = newUnifyFixture(t, "podman-self")
	putUnify(t, f.home("seatbelt-self"), ".claude/projects/g.jsonl", "g")
	holdKeeperKey(t, keeperKey(yoloruntime.JailName(f.ws, "seatbelt-self"), keeperNotchMacosUser))
	f.unify(t, "podman")
	if _, err := os.Lstat(filepath.Join(f.home("seatbelt-self"), ".claude", "projects", "g.jsonl")); err != nil {
		t.Errorf("a running guest's session moved: %v", err)
	}
	for _, want := range []string{"seatbelt-self", "`yolo stop`"} {
		if !strings.Contains(f.stderr.String(), want) {
			t.Errorf("the line does not say %q:\n%s", want, f.stderr.String())
		}
	}
}

// TestAComposedSliceReMergesAsANoOp (UHG-D1): a composed container slice and a composed guest slice
// are merged by another setup's launch as a no-op: no .from- name, links and tree byte-identical.
func TestAComposedSliceReMergesAsANoOp(t *testing.T) {
	for _, tc := range []struct{ setup, rt, dir string }{
		{"podman-self", "podman", "claude"},
		{"seatbelt-self", "macos-user", ".claude"},
	} {
		t.Run(tc.setup, func(t *testing.T) {
			a := newUnifyFixture(t, tc.setup)
			putUnify(t, a.home(tc.setup), tc.dir+"/projects/-w/a.jsonl", "a")
			a.unify(t, tc.rt)
			b := newUnifyFixtureIn(t, a.ws, "apple-container-self")
			roots := []string{paths.UnifiedHome(a.ws), a.home(tc.setup)}
			before := []map[string]string{unifiedTreeSnapshot(t, roots[0]), unifiedTreeSnapshot(t, roots[1])}
			b.unify(t, "container")
			for i, root := range roots {
				after := unifiedTreeSnapshot(t, root)
				for p := range after {
					if strings.Contains(p, ".from-") {
						t.Errorf("a composed slice was carried into the unified home as %s", p)
					}
				}
				if !reflect.DeepEqual(before[i], after) {
					t.Errorf("%s changed:\nbefore %v\nafter  %v", root, before[i], after)
				}
			}
			if strings.Contains(b.stderr.String(), "Merged") {
				t.Errorf("a composed slice's re-merge said it moved files:\n%s", b.stderr.String())
			}
		})
	}
}

// TestTheYoloAccountStaysPrivateWhileTheGateIsClosed (UHG-D8, UHG-D9, UHG-D12).
func TestTheYoloAccountStaysPrivateWhileTheGateIsClosed(t *testing.T) {
	t.Run("as the launcher", func(t *testing.T) {
		f := newUnifyFixture(t, "seatbelt-yolo-account")
		putUnify(t, f.home("seatbelt-yolo-account"), ".claude/projects/a.jsonl", "a")
		before := unifiedTreeSnapshot(t, f.home("seatbelt-yolo-account"))
		f.unify(t, "macos-user")
		after := unifiedTreeSnapshot(t, f.home("seatbelt-yolo-account"))
		if len(before) != len(after) || after[".claude/projects"] != "dir" {
			t.Errorf("the gated account's slice changed: %v -> %v", before, after)
		}
		if _, err := os.Lstat(paths.UnifiedHome(f.ws)); err == nil {
			t.Error("the gated account's launch created the unified home")
		}
		if n := strings.Count(f.stderr.String(), "This setup keeps a private home:"); n != 1 {
			t.Errorf("the private-home line was said %d times, want once:\n%s", n, f.stderr.String())
		}
	})
	t.Run("as a source", func(t *testing.T) {
		f := newUnifyFixture(t, "podman-self")
		putUnify(t, f.home("seatbelt-yolo-account"), ".claude/projects/g.jsonl", "g")
		f.unify(t, "podman")
		if _, err := os.Lstat(filepath.Join(f.home("seatbelt-yolo-account"), ".claude", "projects", "g.jsonl")); err != nil {
			t.Errorf("the gated account's session left its slice: %v", err)
		}
		if !strings.Contains(f.stderr.String(), "The seatbelt-yolo-account setup's home stays private until") {
			t.Errorf("the source line is missing:\n%s", f.stderr.String())
		}
	})
	t.Run("open, the account's uid owns its slice", func(t *testing.T) {
		me := uint32(os.Geteuid())
		for _, tc := range []struct {
			account uint32
			moves   bool
		}{{me, true}, {me + 2, false}} {
			f := newUnifyFixture(t, "podman-self")
			withGateOpen(t, me+1, tc.account)
			putUnify(t, f.home("seatbelt-yolo-account"), ".claude/projects/g.jsonl", "g")
			f.unify(t, "podman")
			_, err := os.Lstat(f.unified(".claude/projects/g.jsonl"))
			if (err == nil) != tc.moves {
				t.Errorf("account uid %d: moved = %v, want %v\n%s", tc.account, err == nil, tc.moves, f.stderr.String())
			}
		}
	})
}

func withGateOpen(t *testing.T, invoking, account uint32) {
	t.Helper()
	oldGate, oldAcct, oldUID := unifiedHomeJoinsYoloAccount, legacyHomeAccountUID, unifyHomeUID
	unifiedHomeJoinsYoloAccount = true
	legacyHomeAccountUID = func() (uint32, error) { return account, nil }
	unifyHomeUID = func() int { return int(invoking) }
	t.Cleanup(func() {
		unifiedHomeJoinsYoloAccount, legacyHomeAccountUID, unifyHomeUID = oldGate, oldAcct, oldUID
	})
}

// TestUnifiedHomeWaitingListsAGuestSliceButNotTheGatedAccount.
func TestUnifiedHomeWaitingListsAGuestSliceButNotTheGatedAccount(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	putUnify(t, f.home("seatbelt-self"), ".claude/projects/g.jsonl", "g")
	putUnify(t, f.home("seatbelt-yolo-account"), ".claude/projects/a.jsonl", "a")
	noPath := func(string) (string, bool) { return "", false }
	noExec := func([]string, string, []string, time.Duration) ExecResult { return ExecResult{} }
	wait := UnifiedHomeWaiting(f.ws, packsFixture(t, "claude"), noPath, noExec)
	if len(wait.Homes) != 1 || wait.Homes[0] != "seatbelt-self" {
		t.Errorf("waiting homes = %v, want [seatbelt-self]", wait.Homes)
	}
}
