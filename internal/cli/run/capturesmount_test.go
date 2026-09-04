package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// capturesmount_test.go pins the host half of install-capture's materialize path: the machine
// capture store, bound :ro into the jail, plus the env var that tells the jail where it went.
//
// WHAT THIS CANNOT REACH, stated rather than left as a gap: run.go's one line
// (`capturesDir: o.CapturesDir()`) is on the CONTAINER arm of Run, and every unit test that
// drives Run in this package goes down the macos-user arm — which builds no podman argv at
// all. So the seam→assembleInput hop is pinned by integration/capturematerialize_test.go,
// which materializes through the real mount in a real jail and goes red if the field is never
// filled. The two tests below pin everything on either side of it.

// captureAssembleInput is the argv-assembly fixture with a capture store present.
func captureAssembleInput(t *testing.T, rt, capturesDir string) []string {
	t.Helper()
	ws := "/ws"
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	// goldenOptions stubs PathExists to a flat false so the frozen argv cannot depend on
	// the developer's /dev. The store mount is gated on the store actually being there —
	// podman CREATES a missing bind source, and an auto-created empty /ctx/captures is a
	// store that answers every lookup with a miss while looking like a store — so this
	// fixture needs the real stat back.
	o.PathExists = func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec),
		rt:           rt,
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "unknown",
		mountTargets: map[string]struct{}{},
		capturesDir:  capturesDir,
	})
}

// A launch with a capture store binds it :ro and names it, so an in-jail launcher can
// materialize an entry instead of downloading one.
//
// :ro is load-bearing rather than tidy: an entry is admitted by the host act alone and its
// files are frozen at admit, so a jail that could rewrite one would be rewriting bytes every
// other workspace on the machine runs.
func TestPodmanBindsTheCaptureStoreReadOnly(t *testing.T) {
	store := t.TempDir()
	got := captureAssembleInput(t, "podman", store)

	if want := store + ":/ctx/captures:ro"; !slices.Contains(got, want) {
		t.Errorf("the capture store is not bound: no %q in the argv\n%s",
			want, strings.Join(got, " "))
	}
	if want := entrypoint.CapturesDirEnv + "=/ctx/captures"; !slices.Contains(got, want) {
		t.Errorf("the jail is not told where the store landed: no %q in the argv\n%s",
			want, strings.Join(got, " "))
	}
	// The DESTINATION is what the entrypoint bakes into every native launcher, so the two
	// halves of this pair must name the same path. Reading it off the argv rather than off
	// the constant is what makes that checkable at all.
	for i, a := range got {
		if a == "-v" && strings.HasPrefix(got[i+1], store+":") {
			dest := strings.Split(got[i+1], ":")[1]
			if env := entrypoint.CapturesDirEnv + "=" + dest; !slices.Contains(got, env) {
				t.Errorf("the store is mounted at %s but the jail is told something else\n%s",
					dest, strings.Join(got, " "))
			}
		}
	}
}

// Apple Container reads the HOST PATH. It puts the whole workspace state at /home/agent in one
// bind and cannot nest another, which is the same reason the staged pack tree is passed by
// host path there (assemble.go's YOLO_PACK_ROOT branch).
func TestAppleContainerNamesTheCaptureStoreByHostPath(t *testing.T) {
	store := t.TempDir()
	got := captureAssembleInput(t, "container", store)

	if want := entrypoint.CapturesDirEnv + "=" + store; !slices.Contains(got, want) {
		t.Errorf("Apple Container must read the store from its host path: no %q\n%s",
			want, strings.Join(got, " "))
	}
	if slices.Contains(got, store+":/ctx/captures:ro") {
		t.Errorf("Apple Container cannot nest this bind, but the argv emits one:\n%s",
			strings.Join(got, " "))
	}
}

// THREE WAYS TO GET NOTHING, and all three must leave the argv exactly as it was before
// slice 4: an empty seam (the capture jail's own launch), a store path that does not exist,
// and — implicitly — every backend that never reaches this code.
//
// The missing-path case is not defensive tidying. podman CREATES a bind source that is not
// there, so without the check a machine that had never captured anything would grow an empty
// /ctx/captures that answers every lookup with a miss while looking exactly like a store.
func TestNoCaptureStoreEmitsNothing(t *testing.T) {
	for name, dir := range map[string]string{
		"suppressed by the seam": "",
		"path does not exist":    filepath.Join(t.TempDir(), "never-created"),
	} {
		got := captureAssembleInput(t, "podman", dir)
		for _, a := range got {
			if strings.Contains(a, "/ctx/captures") || strings.Contains(a, entrypoint.CapturesDirEnv) {
				t.Errorf("%s: the argv still mentions the capture store (%q)", name, a)
			}
		}
	}
}

// fillDefaults wires the seam to the real machine store, so an ordinary launch needs no
// caller to know the path.
func TestFillDefaultsResolvesTheMachineCaptureStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := &Options{}
	fillDefaults(o)
	if o.CapturesDir == nil {
		t.Fatal("fillDefaults left Options.CapturesDir nil")
	}
	if got, want := o.CapturesDir(), paths.CapturesDir(); got != want {
		t.Errorf("CapturesDir() = %q, want the machine store %q", got, want)
	}
	if !strings.HasPrefix(o.CapturesDir(), home+string(os.PathSeparator)) {
		t.Errorf("CapturesDir() = %q, which is not under this HOME (%q)", o.CapturesDir(), home)
	}
}
