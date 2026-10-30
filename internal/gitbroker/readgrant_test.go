package gitbroker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The broker of a selected pack grants nothing (docs/design/boundary-broker.md BB-D104, BB-D108):
// with no read grant it refuses an SSH fetch and push, naming the conversion a legacy JSONC user
// config needs, the `yolo allow git fetch` that grants the read, and `yolo config apply`, without
// starting the host's ssh. Public HTTPS fetch behavior is pinned in https_test.go.
func TestSSHNoReadGrantRefusesEveryCall(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{noRead: true})
	for _, args := range [][]string{{"fetch", "origin"}, {"push", "origin", "HEAD:refs/heads/agent/x"}} {
		out, err := f.git(args...)
		if err == nil {
			t.Fatalf("git %v succeeded with no read grant:\n%s", args, out)
		}
		for _, want := range []string{"no read grant", "`yolo config migrate`", "`yolo allow git fetch`",
			"yolo config apply"} {
			if !strings.Contains(out, want) {
				t.Fatalf("git %v refusal lacks %q:\n%s", args, want, out)
			}
		}
		if strings.Contains(out, "brokered.git.grants") {
			t.Fatalf("git %v refusal names the legacy key to hand-edit:\n%s", args, out)
		}
	}
	if f.sshRan() {
		t.Fatal("the host's ssh ran with no read grant")
	}
	if !strings.Contains(auditRaw(t), `"reason":"no read grant"`) {
		t.Fatalf("no refusal audit event:\n%s", auditRaw(t))
	}
}

// A TOML user is sent to the readable grant, `yolo allow git fetch` (the manifest's
// read_capability), never to a `brokered.git.grants` entry their config.toml cannot hold.
func TestSSHNoReadGrantNamesYoloAllowToATOMLUser(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{noRead: true})
	legacy := paths.UserConfigPath()
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(legacy), paths.UserConfigTOMLName), []byte("packs = [\"git\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := f.git("fetch", "origin")
	if err == nil {
		t.Fatalf("fetch succeeded with no read grant:\n%s", out)
	}
	if !strings.Contains(out, "no read grant") || !strings.Contains(out, "`yolo allow git fetch`") ||
		strings.Contains(out, "brokered.git.grants") {
		t.Fatalf("a TOML user's refusal does not name `yolo allow git fetch`:\n%s", out)
	}
}

// The user config's read grant, approved by `yolo config apply` into the live scope file, lets
// the running broker serve the workspace's fetch at its next call.
func TestUserReadGrantAppliedLetsTheFetchThrough(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{noRead: true})
	if out, err := f.git("fetch", "origin"); err == nil {
		t.Fatalf("fetch before the grant succeeded:\n%s", out)
	}
	writeUserConfig(t, userConfigBody(false))
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopeFile, f.broker.bound, sf.Repos,
		[]brokerscope.Grant{readGrant}); err != nil {
		t.Fatal(err)
	}
	if out, err := f.git("fetch", "origin"); err != nil {
		t.Fatalf("fetch with the read grant: %v\n%s", err, out)
	}
	if !f.sshRan() {
		t.Fatal("the host's ssh did not run for a granted fetch")
	}
}

// The broker reads the live scope file at each call: a read grant the gate took out (a `yolo
// config apply` after the user config dropped it) refuses the next fetch.
func TestUserReadGrantWithdrawnRefusesTheNextFetch(t *testing.T) {
	f := newFixture(t, "")
	if out, err := f.git("fetch", "origin"); err != nil {
		t.Fatalf("granted fetch: %v\n%s", err, out)
	}
	writeUserConfig(t, userConfigBody(true))
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopeFile, f.broker.bound, sf.Repos, nil); err != nil {
		t.Fatal(err)
	}
	out, err := f.git("fetch", "origin")
	if err == nil || !strings.Contains(out, "no read grant") {
		t.Fatalf("fetch after the user config dropped the grant: %v\n%s", err, out)
	}
}

// A read grant names one repository or every one: a grant on another remote covers nothing.
func TestReadGrantOnAnotherRepoCoversNothing(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{noRead: true})
	writeUserConfig(t, userConfigBody(false))
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	other := []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: "other.test/o/r"}}
	if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopeFile, f.broker.bound, sf.Repos, other); err != nil {
		t.Fatal(err)
	}
	if out, err := f.git("fetch", "origin"); err == nil || !strings.Contains(out, "no read grant") {
		t.Fatalf("fetch under another repository's read grant: %v\n%s", err, out)
	}
	if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopeFile, f.broker.bound, sf.Repos,
		[]brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: fixtureRepo}}); err != nil {
		t.Fatal(err)
	}
	if out, err := f.git("fetch", "origin"); err != nil {
		t.Fatalf("fetch under its own repository's read grant: %v\n%s", err, out)
	}
}

// A workspace whose launch recorded no remotes, because it held no read grant and so stayed out
// of the gate (docs/design/boundary-broker.md BB-D110), refuses this SSH fetch naming the read
// grant, not told to add a remote.
func TestNoReadGrantAndNoRemotesNamesTheGrant(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{noRead: true})
	if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopeFile, f.broker.bound, nil, nil); err != nil {
		t.Fatal(err)
	}
	out, err := f.git("fetch", "origin")
	if err == nil {
		t.Fatalf("fetch succeeded with no read grant and no remotes:\n%s", out)
	}
	if !strings.Contains(out, "holds no read grant") || strings.Contains(out, "approved scope") {
		t.Fatalf("the refusal does not name the read grant alone:\n%s", out)
	}
	if f.sshRan() {
		t.Fatal("the host's ssh ran with no read grant")
	}
}
