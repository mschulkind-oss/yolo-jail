package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// commitFixture is a layout every kind of commit step touches: a user config that is a symlink
// into a dotfiles tree and includes an old file, and a workspace whose TOML file includes an old
// file (a respelled parent). It returns home, the workspace and the request.
func commitFixture(t *testing.T) (string, string, Request) {
	t.Helper()
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	dots := filepath.Join(home, "dotfiles", "yolo")
	write(t, filepath.Join(dots, "config.jsonc"), `{"packages": ["just"], "include_if_found": ["overrides.jsonc"]}`+"\n")
	write(t, filepath.Join(dir, "overrides.jsonc"), `{"packages": ["jq"]}`+"\n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../dotfiles/yolo/config.jsonc", filepath.Join(dir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	ws := realDir(t)
	write(t, filepath.Join(ws, "yolo-jail.toml"), "include_if_found = [\"extra.jsonc\"]\n")
	write(t, filepath.Join(ws, "extra.jsonc"), `{"mise_tools": {"lua": "5.4.9"}}`+"\n")
	return home, ws, Request{UserConfigDir: dir, Workspace: ws}
}

// normalized is treeState over home and ws with each root spelled the same, so two fixtures
// compare.
func normalized(t *testing.T, home, ws string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for root, state := range map[string]map[string]string{"home": treeState(t, home), "ws": treeState(t, ws)} {
		for k, v := range state {
			v = strings.ReplaceAll(strings.ReplaceAll(v, home, "<home>"), ws, "<ws>")
			out[root+"/"+k] = v
		}
	}
	return out
}

// The commit never restores and never rewrites an original: a kill after ANY step leaves a state
// that either still loads as before, or that the launch refuses naming `yolo config migrate`,
// which finishes the moves (Resume) to exactly the layout an uninterrupted run leaves.
func TestAKillAtEveryCommitStepRollsForward(t *testing.T) {
	var want map[string]string
	var steps int
	t.Run("uninterrupted", func(t *testing.T) {
		home, ws, req := commitFixture(t)
		p := plan(t, req)
		if _, err := Apply(p, ApplyOptions{StopAfter: func(i int, _ string) bool { steps = i + 1; return false }}); err != nil {
			t.Fatal(err)
		}
		want = normalized(t, home, ws)
	})
	if steps < 8 {
		t.Fatalf("the fixture makes %d commit steps; it must reach every kind", steps)
	}
	for n := 0; n < steps; n++ {
		t.Run("", func(t *testing.T) {
			home, ws, req := commitFixture(t)
			before := launchGateProblems(t, ws)
			p := plan(t, req)
			var stoppedAt string
			_, err := Apply(p, ApplyOptions{StopAfter: func(i int, name string) bool {
				if i == n {
					stoppedAt = name
					return true
				}
				return false
			}})
			if !errors.Is(err, ErrInterrupted) {
				t.Fatalf("Apply = %v, want it stopped after step %d", err, n)
			}
			journal := Interrupted(req)
			if journal == "" {
				t.Fatalf("killed after %q, and no journal says so", stoppedAt)
			}
			// The launch names the half-done state and its one step.
			problems := launchGateProblems(t, ws)
			named := false
			for _, e := range problems {
				if strings.Contains(e, "stopped part way") && strings.Contains(e, "`yolo config migrate`") {
					named = true
				}
			}
			if !named {
				t.Fatalf("killed after %q: the launch does not name the interrupted migration: %v (before: %v)", stoppedAt, problems, before)
			}
			if _, err := Resume(journal); err != nil {
				t.Fatalf("killed after %q: Resume: %v", stoppedAt, err)
			}
			if got := normalized(t, home, ws); !reflect.DeepEqual(got, want) {
				t.Fatalf("killed after %q and resumed:\ngot  %v\nwant %v", stoppedAt, got, want)
			}
			if problems := launchGateProblems(t, ws); len(problems) != len(before) {
				t.Fatalf("killed after %q and resumed, the launch says %v (before: %v)", stoppedAt, problems, before)
			}
		})
	}
}

// A kill before the journal (while staging) changes nothing a reader reads: the old layout loads
// as before, and the next run plans and applies over the staged leftovers.
func TestAKillWhileStagingLeavesTheOldLayout(t *testing.T) {
	home, ws, req := commitFixture(t)
	p := plan(t, req)
	for _, tg := range p.Targets {
		write(t, stagedPath(tg.New), "left by a killed run\n")
	}
	if Interrupted(req) != "" {
		t.Fatal("staged files alone read as an interrupted commit")
	}
	if problems := launchGateProblems(t, ws); len(problems) != 0 {
		t.Fatalf("the old layout no longer loads: %v", problems)
	}
	if _, err := Apply(plan(t, req), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	for k := range normalized(t, home, ws) {
		if strings.HasSuffix(k, ".migrate-staged") {
			t.Errorf("a staged file outlived the migration: %s", k)
		}
	}
}

// A collision a kill left (config.toml beside config.jsonc) names the interrupted migration rather
// than asking the user to choose one by hand.
func TestTheCollisionAKillLeavesNamesTheInterruptedMigration(t *testing.T) {
	_, ws, req := commitFixture(t)
	p := plan(t, req)
	_, err := Apply(p, ApplyOptions{StopAfter: func(_ int, name string) bool {
		return strings.HasPrefix(name, "linking ") && strings.HasSuffix(name, filepath.Join(".config", "yolo-jail", "config.toml"))
	}})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("Apply = %v", err)
	}
	_, _, err = config.LoadConfigWithSources(ws, true, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "stopped part way") || !strings.Contains(err.Error(), "`yolo config migrate`") {
		t.Fatalf("the collision does not name the interrupted migration: %v", err)
	}
}

// A kill inside a move (the hard link made, the old name not yet removed) is finished too, and a
// step run over its own result finds it made.
func TestAStepCutOffInsideIsFinished(t *testing.T) {
	dir := realDir(t)
	from, to := filepath.Join(dir, "a.jsonc"), filepath.Join(dir, "a.jsonc.pre-migrate")
	write(t, from, "old\n")
	if err := os.Link(from, to); err != nil {
		t.Fatal(err)
	}
	s := commitStep{Kind: stepBackup, From: from, To: to, Hash: hashOf([]byte("old\n"))}
	for i := 0; i < 2; i++ {
		if err := s.do(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if exists(from) || readFile(t, to) != "old\n" {
		t.Fatalf("backup not finished: from exists %v", exists(from))
	}

	staged, dest := filepath.Join(dir, ".b.toml.migrate-staged"), filepath.Join(dir, "b.toml")
	write(t, staged, "new\n")
	if err := os.Link(staged, dest); err != nil {
		t.Fatal(err)
	}
	p := commitStep{Kind: stepPlace, From: staged, To: dest, Hash: hashOf([]byte("new\n"))}
	for i := 0; i < 2; i++ {
		if err := p.do(); err != nil {
			t.Fatalf("place run %d: %v", i, err)
		}
	}
	if exists(staged) || readFile(t, dest) != "new\n" {
		t.Fatal("place not finished")
	}

	// A file changed since the plan is refused, never moved.
	write(t, filepath.Join(dir, "c.jsonc"), "edited\n")
	c := commitStep{Kind: stepBackup, From: filepath.Join(dir, "c.jsonc"), To: filepath.Join(dir, "c.jsonc.pre-migrate"), Hash: hashOf([]byte("old\n"))}
	if err := c.do(); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("a changed original was moved: %v", err)
	}
}
