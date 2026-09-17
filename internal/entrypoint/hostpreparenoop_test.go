package entrypoint

// hostpreparenoop_test.go pins the one claim the render collapse makes that a reader has
// to take on trust: that moving the ${workspace} substitution onto the Target
// (render.Target.Prepare, which does nothing at a host notch) changed no host render.
//
// The argument is two steps and each is somebody else's code, which is why it needs a
// test rather than a comment. The host entry PRUNES every ${workspace}-KEYED branch out of
// a surface before composing it (PruneWorkspaceKeyed, on `Contains`), and
// agentcfg.SubstituteWorkspace rewrites only keys that EQUAL the placeholder — so after a
// prune there is nothing left for it to rewrite, and the old unconditional substitution
// against Env.WorkspaceDir()'s "/workspace" default was already a no-op there. Either half
// can stop being true without anyone noticing: a future prune that matched on equality, or
// a substitution extended to values, would each make the host notch start diverging from
// the jail's silently.

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// shippedSurfacesForPrepare is every surface the embedded packs declare — the real corpus,
// not a fixture, because the claim is about what yolo actually renders into real homes.
func shippedSurfacesForPrepare(t *testing.T) []manifest.Surface {
	t.Helper()
	var out []manifest.Surface
	for _, p := range packload.Embedded() {
		surfaces, probs := p.Surfaces()
		if len(probs) > 0 {
			t.Fatalf("embedded pack %q does not decode: %v", p.Name, probs)
		}
		out = append(out, surfaces...)
	}
	if len(out) == 0 {
		t.Fatal("no embedded pack surfaces — this test is covering nothing")
	}
	return out
}

// TestTheHostNotchNeverHadAPlaceholderToBind asserts, over every shipped surface, that
// preparing a PRUNED surface at the host notch gives exactly what the old boot path's
// unconditional substitution gave. That is the behavior-preservation claim
// render.Target.Prepare's docstring makes.
func TestTheHostNotchNeverHadAPlaceholderToBind(t *testing.T) {
	host := render.Host(t.TempDir(), nil, render.OwnershipAssert)
	for _, s := range shippedSurfacesForPrepare(t) {
		pruned, _ := PruneWorkspaceKeyed(s)
		// What the host render does today: the target binds nothing.
		now := host.Prepare(pruned)
		// What it did before the collapse: substitute against Env.WorkspaceDir()'s
		// container default, which is the value a host Env (Workspace: "") resolved to.
		before := agentcfg.SubstituteWorkspace(pruned, "/workspace")
		if !reflect.DeepEqual(now.Defaults, before.Defaults) {
			t.Errorf("%s/%s: host prepare changed the defaults layer\n now:    %#v\n before: %#v",
				s.Agent, s.Name, now.Defaults, before.Defaults)
		}
		if !reflect.DeepEqual(now.Managed, before.Managed) {
			t.Errorf("%s/%s: host prepare changed the managed layer\n now:    %#v\n before: %#v",
				s.Agent, s.Name, now.Managed, before.Managed)
		}
	}
}

// TestSomeShippedSurfaceIsWorkspaceKeyed is the control for the test above, and without it
// that test is vacuous: if no shipped surface mentions the placeholder, prune and
// substitute are both no-ops on everything and the parity holds for a reason that has
// nothing to do with the claim.
//
// It asserts the corpus still contains a case where the two DISAGREE before the prune —
// i.e. a surface the old unconditional substitution really would have rewritten at the
// host notch, which is the whole thing the prune is there to have already removed.
func TestSomeShippedSurfaceIsWorkspaceKeyed(t *testing.T) {
	host := render.Host(t.TempDir(), nil, render.OwnershipAssert)
	keyed := 0
	for _, s := range shippedSurfacesForPrepare(t) {
		if !reflect.DeepEqual(host.Prepare(s).Defaults,
			agentcfg.SubstituteWorkspace(s, "/workspace").Defaults) {
			keyed++
			continue
		}
		if !reflect.DeepEqual(host.Prepare(s).Managed,
			agentcfg.SubstituteWorkspace(s, "/workspace").Managed) {
			keyed++
		}
	}
	if keyed == 0 {
		t.Fatal("no shipped surface is keyed on ${workspace} any more, so " +
			"TestTheHostNotchNeverHadAPlaceholderToBind now proves nothing — either " +
			"restore a fixture with the shape or retire both tests together")
	}
	t.Logf("%d shipped surface(s) are ${workspace}-keyed and reach the host notch pruned", keyed)
}
