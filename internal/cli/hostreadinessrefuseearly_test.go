package cli

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
)

// REFUSE EARLY: once the readiness act knows the launch is going to be refused (a patch series
// that does not apply), it starts no further OPTIONAL refresh for another program, such as an npm
// registry poll or a capture older than DefaultCaptureRefreshAge: that work would be spent on a
// launch that does not happen. The refusal prints as before.
//
// The fixture is a patched fork whose series stops applying, plus a second pack's npm program
// whose installed copy is due its poll. othercli's poll is counted at the fake registry (an
// event, not a time), and the registry offers a newer version so the poll would act.
func refuseEarlyFixture(t *testing.T, otherFirst bool) (*floortest.Dist, hostPackSet) {
	t.Helper()
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	t.Setenv("YOLO_ALLOW_MISSING_PROGRAMS", "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.relocatable = true
	real, err := filepath.EvalSymlinks(fx.home)
	if err != nil {
		t.Fatal(err)
	}
	fx.home = real
	t.Setenv("HOME", real)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(real, ".config"))
	t.Chdir(floortest.ResolvedTemp(t))
	dist := withForkFloor(t)
	fx.platform = floorPatchedPlatform()
	prev := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = prev })

	writeFile(t, filepath.Join(fx.packs, "otherpack", "pack.json"),
		`{"name":"otherpack","contributes":[{"kind":"program","bin":"othercli","via":"npm","package":"othercli-pkg"}]}`)
	other := `{"source":"file://` + filepath.Join(fx.packs, "otherpack") + `","name":"otherpack"}`
	base := `{"source":"file://` + filepath.Join(fx.packs, "basepack") + `","name":"basepack"}`
	fork := `{"source":"file://` + fx.forkDir + `","name":"forkpack"}`
	list := base + "," + fork + "," + other
	if otherFirst {
		list = other + "," + base + "," + fork
	}
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"),
		`{"host_management":"own","packs":[`+list+`]}`)
	dist.Publish("othercli-pkg", "1.0.0", "bin=othercli")

	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		t.Fatal(sel.loadErr)
	}
	// Install both programs at a clean series, so each has a usable copy on the floor.
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	floor := newHostFloor(io.Discard, floorPrograms(sel.packs))
	for _, p := range floorPrograms(sel.packs) {
		if _, _, err := floor.Ensure(context.Background(), p); err != nil {
			t.Fatalf("install %s: %v", p.Bin(), err)
		}
	}
	// The series stops applying, the floor's clock is two hours on (past the poll interval), and the
	// registry has a newer othercli: only a launch that still refreshes it would poll.
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	dist.Publish("othercli-pkg", "1.1.0", "bin=othercli")
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := orig(out, progs)
		f.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	return dist, sel
}

func TestAKnownPatchRefusalStartsNoOptionalRefreshOfAnotherProgram(t *testing.T) {
	for _, tc := range []struct {
		name       string
		otherFirst bool
	}{
		{"the other program is declared after the failing one", false},
		{"the other program is declared before the failing one", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist, sel := refuseEarlyFixture(t, tc.otherFirst)
			polls := len(dist.NpmCalls("view"))
			var errw strings.Builder
			_, rc := hostReadinessAct(sel.packs, []string{"other-command"}, &errw, &run.ActInterrupt{})
			if rc == 0 {
				t.Fatalf("the launch was not refused:\n%s", errw.String())
			}
			if got := len(dist.NpmCalls("view")) - polls; got != 0 {
				t.Errorf("the refused launch still polled the registry for othercli %d time(s):\n%s", got, errw.String())
			}
			for _, want := range []string{"yolo host: refusing to launch: a patch series does not apply\n", "\nStart anyway:\n"} {
				if !strings.Contains(errw.String(), want) {
					t.Errorf("the refusal lacks %q:\n%s", want, errw.String())
				}
			}
		})
	}
}
