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

// A refused push names, per ref, the grant that would have covered it, and following that hint
// makes the same push land: the hint is the next step, not a description of the rule.

// suggestedGrant is the one grant the refusal output's `yolo request git <capability> [ref]`
// declares for ref, on the workspace's own remote (the repository a request reaches).
func suggestedGrant(t *testing.T, out, ref string) brokerscope.Grant {
	t.Helper()
	const prefix = "run `yolo request " + Source + " "
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, ref+":") {
			continue
		}
		start := strings.Index(line, prefix)
		if start < 0 {
			continue
		}
		rest := line[start+len(prefix):]
		end := strings.Index(rest, "`")
		if end < 0 {
			t.Fatalf("an unterminated command on the hint line %q", line)
		}
		fields := strings.Fields(rest[:end])
		g := brokerscope.Grant{Repo: fixtureRepo}
		for kind, capability := range KindCapabilities {
			if len(fields) > 0 && fields[0] == capability {
				g.Kind = kind
			}
		}
		switch {
		case g.Kind == grantBranch && len(fields) == 2:
			g.Branch = fields[1]
		case g.Kind == grantTag && len(fields) == 2:
			g.Tag = fields[1]
		case g.Kind == grantCreateBranch && len(fields) == 1:
		default:
			t.Fatalf("an unreadable request on the hint line %q", line)
		}
		if RequestCommand(g) != "yolo request "+Source+" "+rest[:end] {
			t.Fatalf("the hint's command %q is not RequestCommand(%+v)", rest[:end], g)
		}
		return g
	}
	t.Fatalf("no grant suggested for %s in:\n%s", ref, out)
	return brokerscope.Grant{}
}

// convertThenAllow asserts out tells a legacy JSONC user to convert, then names the exact
// `yolo allow git on_request <capability>` for kind, and names no legacy key to hand-edit.
func convertThenAllow(t *testing.T, out, kind string) {
	t.Helper()
	migrate := strings.Index(out, "`yolo config migrate`")
	allow := strings.Index(out, "`"+AllowOnRequestCommand(kind)+"`")
	if migrate < 0 || allow < migrate {
		t.Fatalf("the hint does not name `yolo config migrate`, then `%s`:\n%s", AllowOnRequestCommand(kind), out)
	}
	for _, legacy := range []string{"grant_kinds", "brokered.git.grants", `{"kind":`} {
		if strings.Contains(out, legacy) {
			t.Fatalf("the hint names the legacy %q to hand-edit:\n%s", legacy, out)
		}
	}
}

// approve adds g to the live scope file, as `yolo config apply` approving the workspace config
// does, and lists its kind in the user config.
func (f *fixture) approve(t *testing.T, g brokerscope.Grant) {
	t.Helper()
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.ReplaceScopeContext(context.Background(), f.scopeFile, f.broker.bound, sf.Repos, append(sf.Grants, g)); err != nil {
		t.Fatal(err)
	}
	allowKinds(t, g.Kind)
}

func TestRefusedUpdateNamesTheBranchGrantThatCoversIt(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{})
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:main")
	if err == nil {
		t.Fatalf("an update of main with no grant landed:\n%s", out)
	}
	g := suggestedGrant(t, out, "refs/heads/main")
	if g != (brokerscope.Grant{Kind: "branch", Repo: fixtureRepo, Branch: "main"}) {
		t.Fatalf("suggested %+v", g)
	}
	for _, want := range []string{"not in this workspace's working set", "run `yolo request git push main`", "yolo-jail.jsonc", "yolo config apply"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the hint lacks %q:\n%s", want, out)
		}
	}
	convertThenAllow(t, out, grantBranch)
	f.approve(t, g)
	f.mustPush("origin", "HEAD:main")
	if got, want := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("after following the hint, upstream main %q, local HEAD %q", got, want)
	}
}

func TestRefusedCreationNamesTheCreateBranchGrant(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{})
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:refs/heads/push/trunk")
	if err == nil {
		t.Fatalf("a creation with no grant landed:\n%s", out)
	}
	g := suggestedGrant(t, out, "refs/heads/push/trunk")
	if g != (brokerscope.Grant{Kind: "create-branch", Repo: fixtureRepo}) {
		t.Fatalf("suggested %+v", g)
	}
	if !strings.Contains(out, "any new branch") || !strings.Contains(out, "run `yolo request git new_branches`") {
		t.Fatalf("the hint does not say the grant covers any new branch name:\n%s", out)
	}
	convertThenAllow(t, out, grantCreateBranch)
	f.approve(t, g)
	f.mustPush("origin", "HEAD:refs/heads/push/trunk")
}

// A TOML user config that does not allow the kind gets `yolo allow git on_request <capability>`
// directly, with no conversion before it, both in the push hint and in the not-in-effect line.
func TestRefusedUpdateNamesYoloAllowToATOMLUser(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{grants: []brokerscope.Grant{{Kind: "tag", Repo: fixtureRepo, Tag: "v*"}}})
	legacy := paths.UserConfigPath()
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(legacy), paths.UserConfigTOMLName),
		[]byte("packs = [\"git\"]\n\n[allow.git]\nalways = [\"fetch\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commit("one")
	mustGit(t, f.ws, "tag", "v1")
	out, err := f.git("push", "origin", "HEAD:main", "refs/tags/v1")
	if err == nil {
		t.Fatalf("a push with no allowed kind landed:\n%s", out)
	}
	for _, want := range []string{
		"run `yolo request git push main` in this workspace",
		"run `" + AllowOnRequestCommand(grantBranch) + "` on the host",
		"tag grant on " + fixtureRepo + " is not in effect",
		"run `" + AllowOnRequestCommand(grantTag) + "` on the host",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("a TOML user's refusal lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "convert your user config") || strings.Contains(out, "grant_kinds") {
		t.Fatalf("a TOML user is told to convert or to hand-edit a legacy key:\n%s", out)
	}
}

// With the kind already listed in the user config, the hint does not send the user to list it.
func TestRefusedUpdateHintOmitsAKindAlreadyAllowed(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{})
	allowKinds(t, "branch")
	f.commit("one")
	out, _ := f.git("push", "origin", "HEAD:main")
	suggestedGrant(t, out, "refs/heads/main")
	if strings.Contains(out, "yolo allow") {
		t.Fatalf("the hint asks for a kind already listed:\n%s", out)
	}
}

// A tag no grant covers asks for approval, and its hint names the tag grant that would cover it
// (GB-D25) and the host push; following the hint makes the same push land.
func TestRefusedTagNamesTheTagGrant(t *testing.T) {
	f := grantedFixture(t)
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:refs/tags/v1")
	if err == nil {
		t.Fatalf("a tag landed:\n%s", out)
	}
	g := suggestedGrant(t, out, "refs/tags/v1")
	if g != (brokerscope.Grant{Kind: "tag", Repo: fixtureRepo, Tag: "v1"}) {
		t.Fatalf("suggested %+v", g)
	}
	for _, want := range []string{"no tag grant covers it", "asks for approval", "run `yolo request git push_tags v1`", "from the host", "yolo config apply"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the tag hint lacks %q:\n%s", want, out)
		}
	}
	convertThenAllow(t, out, grantTag)
	f.approve(t, g)
	f.mustPush("origin", "HEAD:refs/tags/v1")
	if got, want := f.upstreamRef("refs/tags/v1"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("after following the hint, upstream v1 %q, local HEAD %q", got, want)
	}
}
