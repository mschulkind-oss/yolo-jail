package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// perSideShadowMounts extracts the `<backing>:/workspace/<rel>` targets from an
// assembled argv, in argv order.
func perSideShadowMounts(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" || !strings.Contains(argv[i+1], "/venv-shadows/") {
			continue
		}
		_, target, ok := strings.Cut(argv[i+1], ":")
		if ok {
			out = append(out, target)
		}
	}
	return out
}

// TestPerSideDefaultsIncludeNodeModules pins the DEFAULT shadow set through the
// assembled argv rather than through venvShadowMountArgs directly, so it fails
// if assemble.go stops calling the emitter — the call-site pin AGENTS.md asks
// for. Verified by mutation 2026-08-23: deleting the venvShadowMountArgs line
// in assemble.go fails this test.
//
// node_modules is in the default set on the correctness argument that put
// `.venv` there, not on a security one: a node_modules shared between a macOS
// host and a Linux jail is already broken for any package with a native build.
func TestPerSideDefaultsIncludeNodeModules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	got := perSideShadowMounts(o.assembleRunCmd(relocationInput(t, "podman", "/ws/.yolo/home", nil)))
	want := []string{"/workspace/.venv", "/workspace/node_modules"}
	if !slices.Equal(got, want) {
		t.Errorf("default per-side shadows = %v, want %v", got, want)
	}
}

// TestPerSideDefaultsUnionWithConfig: the default set is a floor, not a
// replacement — a config naming its own paths keeps node_modules, and naming
// node_modules explicitly does not double-mount it (the set is deduped).
func TestPerSideDefaultsUnionWithConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	cases := []struct {
		name    string
		perSide []any
		want    []string
	}{
		{
			"config adds a monorepo path",
			[]any{"packages/web/node_modules"},
			[]string{"/workspace/.venv", "/workspace/node_modules", "/workspace/packages/web/node_modules"},
		},
		{
			"explicit node_modules does not duplicate",
			[]any{"node_modules"},
			[]string{"/workspace/.venv", "/workspace/node_modules"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
			cfg := jsonx.NewOrderedMap()
			cfg.Set("per_side_paths", tc.perSide)
			in.cfg = cfg

			got := perSideShadowMounts(o.assembleRunCmd(in))
			if !slices.Equal(got, tc.want) {
				t.Errorf("per-side shadows = %v, want %v", got, tc.want)
			}
		})
	}
}
