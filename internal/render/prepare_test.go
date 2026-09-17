package render

// prepare_test.go pins the ${workspace} half of the notch — the one decision the collapse
// MOVED rather than merely relocated. Both old render paths resolved the placeholder
// themselves, one against Env.WorkspaceDir() and one against a "/workspace" constant beside
// the CLI, and neither could see that the host notch has no referent to resolve against.
// Target.Prepare is now the single answer, so these are the cases it has to get right.

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// workspaceKeyedSurface is a surface shaped like the shipped claude pack's: a per-jail
// table keyed by the workspace path, beside a key that has nothing to do with any
// workspace. The pair is deliberate — a notch that drops the first must keep the second.
func workspaceKeyedSurface() manifest.Surface {
	return manifest.Surface{
		Agent: "probe", Name: "settings", Path: "~/.probe/settings.json", Codec: "json",
		Defaults: map[string]any{
			"projects": map[string]any{
				agentcfg.WorkspacePlaceholder: map[string]any{"trusted": true},
			},
			"theme": "dark",
		},
	}
}

// projectKeys returns the keys of the surface's `projects` table, which is where the
// placeholder sits in the fixture above.
func projectKeys(t *testing.T, s manifest.Surface) []string {
	t.Helper()
	defaults, isMap := s.Defaults.(map[string]any)
	if !isMap {
		t.Fatalf("fixture lost its Defaults object: %#v", s.Defaults)
	}
	projects, isMap := defaults["projects"].(map[string]any)
	if !isMap {
		t.Fatalf("fixture lost its projects table: %#v", defaults["projects"])
	}
	out := make([]string, 0, len(projects))
	for k := range projects {
		out = append(out, k)
	}
	return out
}

// TestPrepareBindsThePlaceholderOnlyWhereThereIsOneToBindItTo is the whole rule in three
// notches: a jail binds it to the container workspace, a preview binds it inside the
// scratch dir it is allowed to write, and a host binds NOTHING because it has no
// per-workspace referent.
//
// The host case is the one with teeth. Delete the `t.Workspace == ""` gate in Prepare and
// the other two still pass, because substituting against a workspace they HAVE is what
// they wanted; the host would substitute against "" and write a key at the empty path —
// a key under a directory that is not the user's, which is exactly what the host entry
// prunes the branch to avoid.
func TestPrepareBindsThePlaceholderOnlyWhereThereIsOneToBindItTo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target Target
		want   string
	}{
		{"jail", Jail("/home/agent", "/workspace", nil), "/workspace"},
		{"preview", Preview("/tmp/preview"), "/tmp/preview"},
		{"host", Host("/Users/real", nil, OwnershipAssert), agentcfg.WorkspacePlaceholder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := projectKeys(t, tc.target.Prepare(workspaceKeyedSurface()))
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("projects keys = %q, want [%q]", got, tc.want)
			}
		})
	}
}

// TestPrepareLeavesTheDeclarationAlone guards the aliasing hazard
// agentcfg.SubstituteWorkspace's own docstring names: a manifest surface is a value whose
// maps are shared with the manifest, so a renderer that rewrote in place would corrupt the
// declaration for every later render in the process. Prepare inherits that guarantee and
// must keep inheriting it.
func TestPrepareLeavesTheDeclarationAlone(t *testing.T) {
	s := workspaceKeyedSurface()
	Jail("/home/agent", "/workspace", nil).Prepare(s)
	if got := projectKeys(t, s); len(got) != 1 || got[0] != agentcfg.WorkspacePlaceholder {
		t.Fatalf("Prepare mutated the surface it was given: projects keys = %q", got)
	}
}

// TestPrepareIsIdempotent is what lets a caller state the preparation at the top of a
// function AND let Compose state it again, so `surface` means one thing for the whole body
// (entrypoint.composeStatefulSurface does exactly this). It holds because nothing is keyed
// on the placeholder after the first pass — but that is a property of the substitution, not
// a promise it makes, so it is asserted rather than assumed.
func TestPrepareIsIdempotent(t *testing.T) {
	jail := Jail("/home/agent", "/workspace", nil)
	once := jail.Prepare(workspaceKeyedSurface())
	twice := jail.Prepare(once)
	if !reflect.DeepEqual(once.Defaults, twice.Defaults) {
		t.Fatalf("preparing twice differs from preparing once:\n once:  %#v\n twice: %#v",
			once.Defaults, twice.Defaults)
	}
}

// TestComposePreparesBeforeFolding is the call-site half: it is not enough that Prepare is
// right, the two render entries have to run it. Both are checked, because they are separate
// functions and a fix applied to one has twice been applied to only one.
func TestComposePreparesBeforeFolding(t *testing.T) {
	jail := Jail("/home/agent", "/workspace", nil)

	prepared, res, err := jail.Compose(workspaceKeyedSurface(), Layers{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if got := projectKeys(t, prepared); len(got) != 1 || got[0] != "/workspace" {
		t.Fatalf("Compose returned an unprepared surface: projects keys = %q", got)
	}
	assertComposedProjectKey(t, res.ConfigMap())

	prepared, out, err := jail.ComposeStateful(workspaceKeyedSurface(), Layers{}, State{})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	if got := projectKeys(t, prepared); len(got) != 1 || got[0] != "/workspace" {
		t.Fatalf("ComposeStateful returned an unprepared surface: projects keys = %q", got)
	}
	assertComposedProjectKey(t, out.Result.ConfigMap())
}

// assertComposedProjectKey checks the COMPOSED output, not just the returned surface — the
// substitution has to reach the bytes, or a caller that writes res.Encoded rather than
// re-reading the surface would still emit the literal placeholder.
func assertComposedProjectKey(t *testing.T, cfg map[string]any) {
	t.Helper()
	projects, isMap := cfg["projects"].(map[string]any)
	if !isMap {
		t.Fatalf("composed config has no projects table: %#v", cfg)
	}
	if _, ok := projects["/workspace"]; !ok {
		t.Fatalf("composed config kept the unsubstituted key: %#v", projects)
	}
}
