package run

// cgroupdefault_test.go pins T15 of docs/design/loophole-access-approval-plan.md: the
// cgroup-delegate pack is selected by default (PD-13, LG-D13), so its delegate runs with an empty
// config, the "no coding agent" notice still prints, and a launch does not report it inert.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// writeUserConfigBody writes the user config verbatim, so a test can state "no packs key".
func writeUserConfigBody(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A launch with NO `packs` key stages cgroup-delegate, the converged loophole set honors the
// delegate, and the delegate publishes its socket. Before T15 the pack had to be named in `packs`
// and its loophole switched on, so `yolo-cglimit` did nothing in a jail with an empty config.
func TestTheCgroupDelegateRunsWithAnEmptyConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the in-process delegate is Linux-only; off Linux the pack is selected and inert")
	}
	hostLauncher(t)
	home := packHome(t)
	writeUserConfigBody(t, home, `{}`)
	isolatePackModules(t)

	var out bytes.Buffer
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Stdout = &out
	o.Stderr = &out
	_, loaded, _, err := o.stagePacks("yolo-test-cgd-default")
	if err != nil {
		t.Fatalf("staging an empty config: %v\n%s", err, out.String())
	}
	var selected *packload.Pack
	for _, p := range loaded {
		if p.Name == paths.BuiltinCgroupLoopholeName {
			selected = p
		}
	}
	if selected == nil || !selected.SelectedByDefault() {
		t.Fatalf("a launch with no `packs` key staged %v, want cgroup-delegate selected by default",
			packNamesOf(loaded))
	}
	set := loopholes.NewHostSet(nil)
	if !o.cgroupDelegateHonored(set) {
		t.Fatal("the default-selected cgroup-delegate loophole is not honored by the launch's set")
	}

	o.IsMacOS = false
	o.PathExists = func(string) bool { return true }
	dir := t.TempDir()
	h, ok := o.startCgroupDelegate("yolo-test-cgd-default", "podman", dir)
	if !ok {
		t.Fatalf("startCgroupDelegate did not start:\n%s", out.String())
	}
	defer h.stop()
	fi, err := os.Stat(filepath.Join(dir, paths.CgdSocketName))
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("no socket published at %s (err %v)", filepath.Join(dir, paths.CgdSocketName), err)
	}
}

// The default-selected pack is not a coding agent: with it alone selected, the launch still says
// it has none.
func TestWarnIfNoPacksStillWarnsWithOnlyTheDefaultPack(t *testing.T) {
	userPacksConfig(t, `{}`)
	entries, err := config.LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].ByDefault {
		t.Fatalf("entries = %+v, want only the default-selected pack", entries)
	}
	if got := noPacksOutput(t); !strings.Contains(got, "no coding agent") {
		t.Errorf("with only cgroup-delegate selected the launch said nothing about the missing "+
			"agent:\n%s", got)
	}
}

// NOT REPORTED INERT AT LAUNCH (LG-D13): the user did not choose it. On Apple Container (every pack
// loophole inert) and off Linux (the manifest's platforms) the shipped pack would otherwise print
// a line on every launch; `yolo loopholes status` reports it instead.
func TestTheDefaultPackIsNotReportedInertAtLaunch(t *testing.T) {
	var shipped *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == paths.BuiltinCgroupLoopholeName {
			shipped = p
		}
	}
	if shipped == nil || !shipped.SelectedByDefault() {
		t.Fatal("the shipped cgroup-delegate pack is not selected by default")
	}
	if got := inertOutput(t, "container", shipped); got != "" {
		t.Errorf("Apple Container reported the default-selected pack inert:\n%s", got)
	}
	if got := platformInertLines([]*packload.Pack{shipped}, nil); runtime.GOOS == "linux" && len(got) != 0 {
		t.Errorf("unexpected platform lines on Linux: %v", got)
	}
	if got := chosenPacks([]*packload.Pack{shipped}); len(got) != 0 {
		t.Errorf("chosenPacks kept the default-selected pack: %v", packNamesOf(got))
	}
	// A pack the user chose is still reported, so the filter is not a blanket silence.
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy", "transport": "none"}`)
	if got := inertOutput(t, "container", shipped, p); !strings.Contains(got, "acme-proxy") ||
		strings.Contains(got, "cgroup-delegate") {
		t.Errorf("inert report = %q, want acme-proxy alone", got)
	}
}

// A kernel with no cgroup v2 is not reported on the terminal either: the line goes to the launch
// log alone, where whoever asks why yolo-cglimit is unavailable finds it.
func TestNoCgroupV2GoesToTheLaunchLogAlone(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the in-process delegate is Linux-only")
	}
	var term, log bytes.Buffer
	o := &Options{Stderr: logTee{term: &term, log: &log}}
	fillDefaults(o)
	o.IsMacOS = false
	o.PathExists = func(string) bool { return false }
	if _, ok := o.startCgroupDelegate("yolo-ws-cgd0000", "podman", t.TempDir()); ok {
		t.Fatal("the delegate started without cgroup v2")
	}
	if term.Len() != 0 {
		t.Errorf("the terminal heard about a default-selected pack's inert delegate:\n%s", term.String())
	}
	for _, want := range []string{"cgroup-delegate", "cgroup v2", "Nothing is missing", "yolo-cglimit"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("launch log %q missing %q", log.String(), want)
		}
	}
}

// logTee is a launch stream with a launch.log half (LogWriter), for a test.
type logTee struct{ term, log *bytes.Buffer }

func (l logTee) Write(p []byte) (int, error)    { l.log.Write(p); return l.term.Write(p) }
func (l logTee) WriteLog(p []byte) (int, error) { return l.log.Write(p) }

// chosenModules is mods less the default-selected pack's, for a test about the packs its config
// named: every resolution now carries cgroup-delegate's module too (PD-13).
func chosenModules(mods []loopholes.PackModule) []loopholes.PackModule {
	var out []loopholes.PackModule
	for _, m := range mods {
		if !config.IsSelectedByDefault(m.Pack) {
			out = append(out, m)
		}
	}
	return out
}

func packNamesOf(packs []*packload.Pack) []string {
	var out []string
	for _, p := range packs {
		out = append(out, p.Name)
	}
	return out
}
