package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"golang.org/x/sys/unix"
)

// agedLink makes link -> target with an mtime age ago.
func agedLink(t *testing.T, target, link string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	tv := []unix.Timeval{unix.NsecToTimeval(when.UnixNano()), unix.NsecToTimeval(when.UnixNano())}
	if err := unix.Lutimes(link, tv); err != nil {
		t.Fatal(err)
	}
	return link
}

func present(p string) bool { _, err := os.Lstat(p); return err == nil }

// buildStateFixture lays out one link of every build-state class under a temp HOME's
// build dir: for each, one a running jail holds and one nothing holds.
type buildStateFixture struct {
	livePrefixRoot, deadPrefixRoot string
	liveOutLink, deadOutLink       string
	liveExtras, deadExtras         string
	newestExtras                   string
	newCopier, oldCopier, oldMan   string
}

const (
	livePrefix  = "/nix/store/aaaa-yolo-jail-install-prefix"
	deadPrefix  = "/nix/store/bbbb-yolo-jail-install-prefix"
	liveExtrasP = "/nix/store/cccc-yolo-image-extras"
)

func newBuildStateFixture(t *testing.T) buildStateFixture {
	t.Helper()
	month := 30 * 24 * time.Hour
	b := paths.BuildDir()
	skopeo := filepath.Join(t.TempDir(), "skopeo")
	if err := os.MkdirAll(skopeo, 0o755); err != nil {
		t.Fatal(err)
	}
	return buildStateFixture{
		livePrefixRoot: agedLink(t, livePrefix, filepath.Join(b, "prefix-roots", image.ImageStoreKey(livePrefix)), month),
		deadPrefixRoot: agedLink(t, deadPrefix, filepath.Join(b, "prefix-roots", image.ImageStoreKey(deadPrefix)), month),
		liveOutLink:    agedLink(t, livePrefix, filepath.Join(b, "jail-prefix-1111111111111111"), month),
		deadOutLink:    agedLink(t, deadPrefix, filepath.Join(b, "jail-prefix-2222222222222222"), month),
		liveExtras:     agedLink(t, liveExtrasP, filepath.Join(paths.PackageRootsDir(), "extras-1111111111111111"), month),
		deadExtras:     agedLink(t, "/nix/store/dddd-yolo-image-extras", filepath.Join(paths.PackageRootsDir(), "extras-2222222222222222"), month),
		newestExtras:   agedLink(t, "/nix/store/eeee-yolo-image-extras", filepath.Join(paths.PackageRootsDir(), "extras-3333333333333333"), time.Hour),
		newCopier:      agedLink(t, skopeo, filepath.Join(b, "image-copier-1111111111111111"), time.Hour),
		oldCopier:      agedLink(t, skopeo, filepath.Join(b, "image-copier-2222222222222222"), month),
		oldMan:         agedLink(t, skopeo, filepath.Join(b, "image-copier-2222222222222222-man"), month),
	}
}

// liveRuntime answers as podman with one running jail executing from livePrefix and
// started with liveExtrasP.
func liveRuntime(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
	joined := strings.Join(argv, " ")
	switch {
	case strings.Contains(joined, " ps "):
		return ExecResult{Ran: true, Stdout: "yolo-live running\n"}
	case strings.Contains(joined, ".Mounts"):
		return ExecResult{Ran: true, Stdout: `[{"Type":"bind","Source":"` + livePrefix + `/` +
			image.JailPrefixSubdir + `/bin","Destination":"` + prune.PrefixBinMountDest + `"}]`}
	case strings.Contains(joined, ".Config.Env"):
		return ExecResult{Ran: true, Stdout: `["YOLO_STORE_FHS_PROFILES=` + liveExtrasP + `"]`}
	}
	return ExecResult{Ran: true, RC: 1}
}

func buildStateOptions(t *testing.T, exec func([]string, string, []string, time.Duration) ExecResult) *Options {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_NO_AUTO_IMAGE_REAP", "")
	o := &Options{}
	fillDefaults(o)
	o.Now = time.Now
	o.Workspace = t.TempDir()
	o.Exec = exec
	return o
}

func housekeepingLog(t *testing.T, o *Options) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(o.Workspace, ".yolo", "logs", housekeepingLogName))
	return string(b)
}

// TestTheBuildStateClassKeepsWhatRunsAndRemovesTheRest drives the slot's class end to
// end: every link a running jail holds survives, every other one past its rule goes, the
// class stamps, and the log says what it did.
func TestTheBuildStateClassKeepsWhatRunsAndRemovesTheRest(t *testing.T) {
	o := buildStateOptions(t, liveRuntime)
	f := newBuildStateFixture(t)

	o.reapBuildStateLinks("podman", nil)

	for _, p := range []string{f.livePrefixRoot, f.liveOutLink, f.liveExtras, f.newestExtras, f.newCopier} {
		if !present(p) {
			t.Errorf("%s was removed; a running jail holds it, or it is the newest of its kind", p)
		}
	}
	for _, p := range []string{f.deadPrefixRoot, f.deadOutLink, f.deadExtras, f.oldCopier, f.oldMan} {
		if present(p) {
			t.Errorf("%s survived; nothing holds it", p)
		}
	}
	if due, _ := o.classDebounce(BuildStateHousekeepingClass); due {
		t.Error("a completed pass did not stamp its debounce")
	}
	if log := housekeepingLog(t, o); !strings.Contains(log, "build state: removed 1 prefix root(s), 1 retired prefix out-link(s), 2 image-copier link(s), 1 package-profile root(s)") {
		t.Errorf("housekeeping.log = %q, want the class's removal line", log)
	}
}

// TestTheBuildStateClassDeclinesWhenTheRuntimeCannotAnswer: every liveness-gated part
// removes nothing, the decline is logged, and the class is left due for the next launch.
// The copier links need no liveness and are still swept.
func TestTheBuildStateClassDeclinesWhenTheRuntimeCannotAnswer(t *testing.T) {
	o := buildStateOptions(t, func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: false}
	})
	f := newBuildStateFixture(t)

	o.reapBuildStateLinks("podman", nil)

	for _, p := range []string{f.livePrefixRoot, f.deadPrefixRoot, f.liveOutLink, f.deadOutLink, f.liveExtras, f.deadExtras} {
		if !present(p) {
			t.Errorf("%s was removed while liveness was unknown", p)
		}
	}
	if present(f.oldCopier) {
		t.Error("the aged copier link survived; that part needs no liveness")
	}
	if due, _ := o.classDebounce(BuildStateHousekeepingClass); !due {
		t.Error("a declined pass stamped its debounce — the next launch must retry")
	}
	if log := housekeepingLog(t, o); !strings.Contains(log, "declined prefix roots") || !strings.Contains(log, "declined package-profile roots") {
		t.Errorf("housekeeping.log = %q, want both declines recorded", log)
	}
}

// TestTheEmbeddedPackTreeClassReapsOtherBuildsTrees drives the slot's embedded-tree
// class: an unfinished, unleased leftover past its floor goes, the class stamps.
func TestTheEmbeddedPackTreeClassReapsOtherBuildsTrees(t *testing.T) {
	o := buildStateOptions(t, liveRuntime)
	leftover := filepath.Join(paths.EmbeddedPacksDir(), ".tmp-leftover")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(leftover, old, old); err != nil {
		t.Fatal(err)
	}

	o.reapEmbeddedPackTrees()

	if present(leftover) {
		t.Error("an unleased leftover two hours old survived the slot's embedded-tree class")
	}
	if due, _ := o.classDebounce(EmbeddedPacksHousekeepingClass); due {
		t.Error("a completed pass did not stamp its debounce")
	}
	if log := housekeepingLog(t, o); !strings.Contains(log, "embedded pack trees: reclaimed") {
		t.Errorf("housekeeping.log = %q, want the reclaim recorded", log)
	}
}

// TestTheBuildStateClassesHonorTheOptOut: YOLO_NO_AUTO_IMAGE_REAP stops every automatic
// reap in the slot, these included.
func TestTheBuildStateClassesHonorTheOptOut(t *testing.T) {
	o := buildStateOptions(t, liveRuntime)
	t.Setenv("YOLO_NO_AUTO_IMAGE_REAP", "1")
	f := newBuildStateFixture(t)
	o.reapBuildStateLinks("podman", nil)
	o.reapEmbeddedPackTrees()
	if !present(f.deadOutLink) {
		t.Error("the opt-out did not stop the build-state class")
	}
}

// TestPackageRootFamiliesMatchTheWriters pins the package-root sweep's name families to the
// launch's two writers. A renamed writer would make its roots invisible to the sweep (they
// would accumulate again), and the families must stay distinct, since each keeps its own
// newest root.
func TestPackageRootFamiliesMatchTheWriters(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	extras := filepath.Base(extrasProfileRootLink("/nix/store/x-yolo-image-extras"))
	jail := filepath.Base(storeProfileRootLink([]any{"git"}))
	fe, fj := prune.PackageRootFamily(extras), prune.PackageRootFamily(jail)
	if fe == "" || fj == "" || fe == fj {
		t.Fatalf("the writers name %q and %q, families %q and %q: each must match its own family",
			extras, jail, fe, fj)
	}
	if filepath.Dir(extrasProfileRootLink("/nix/store/x")) != paths.PackageRootsDir() ||
		filepath.Dir(storeProfileRootLink([]any{"git"})) != paths.PackageRootsDir() {
		t.Fatal("a package-profile root is written outside paths.PackageRootsDir, which the sweep reads")
	}
}

// TestTheSlotRunsTheBuildStateClasses is the call-site pin: deleting either call from
// the slot leaves every unit test above green and the stores growing again. The
// build-state class must come before the store-output class, whose candidates its
// links keep rooted.
func TestTheSlotRunsTheBuildStateClasses(t *testing.T) {
	src, err := os.ReadFile("housekeeping.go")
	if err != nil {
		t.Fatal(err)
	}
	slot := afterFunc(string(src), "func (o *Options) runHousekeeping(")
	links := strings.Index(slot, "o.reapBuildStateLinks(rt, guard)")
	outputs := strings.Index(slot, "o.reapSupersededStoreOutputs(rt, guard)")
	if links < 0 || outputs < 0 || links > outputs {
		t.Fatal("runHousekeeping must call o.reapBuildStateLinks(rt, guard) before " +
			"o.reapSupersededStoreOutputs(rt, guard): the prefix roots and retired out-links it " +
			"removes are what keep superseded install prefixes out of that class's reach")
	}
	if !strings.Contains(slot, "o.reapEmbeddedPackTrees()") {
		t.Error("the container slot no longer runs the embedded pack tree class")
	}
	if !strings.Contains(afterFunc(string(src), "func (o *Options) runMacosUserHousekeeping("), "o.reapEmbeddedPackTrees()") {
		t.Error("the macos-user slot no longer runs the embedded pack tree class, which grows on every backend")
	}
	fn, err := os.ReadFile("buildstatehousekeeping.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"prune.LivePrefixSources(", "if srcKnown {", "prune.LiveStoreProfiles(",
		"if profKnown {", "if srcKnown && profKnown {", ", guard)"} {
		if !strings.Contains(string(fn), want) {
			t.Errorf("the build-state class no longer contains %q — it must ask the runtime, decline "+
				"on unknown, and delete under the slot's guard", want)
		}
	}
}
