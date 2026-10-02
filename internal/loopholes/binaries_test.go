package loopholes

// binaries_test.go pins where a manifest's `binaries` meets this machine (binaries.go,
// docs/design/broker-as-a-pack.md BP-D1): the argvs resolve to the cache and the container path,
// a build that is not fetched keeps the loophole off and names `yolo pack install`, a platform
// with no build is the platform axis's answer, the jail's build is mounted read-only from the
// cache — carrying an exec bit the pack's own tree does not have — and the macos-user guest and
// the doctor both decline what they cannot run.
//
// Mutation checks (does it fail if the production line goes?):
//   - drop ReplaceBinaryTokens from resolve → TestBinaryTokensResolveAtLoad.
//   - drop BinariesFetched from Active() → TestAnUnfetchedBuildKeepsTheLoopholeOff.
//   - drop the -v loop in runtimeArgsWith → TestTheJailBuildIsMountedFromTheCache.
//   - drop the binary half of unsupportedOnReason → TestNoBuildHereIsThePlatformAxis.
//   - drop the namesContainerBinary case → TestTheMacosUserGuestDeclinesAJailBinary.
//   - drop hostBinariesUnready from runDoctorChecks → TestTheDoctorDoesNotRunAnUnfetchedBuild.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packbin"
)

var (
	hostPlatform = runtime.GOOS + "/" + runtime.GOARCH
	jailPlatform = "linux/" + runtime.GOARCH
	sumHost      = strings.Repeat("1", 64)
	sumJail      = strings.Repeat("2", 64)
)

// isolateBinaryCache points the cache every record resolves against at a temp dir.
func isolateBinaryCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := BinaryCacheDir
	BinaryCacheDir = func() string { return dir }
	t.Cleanup(func() { BinaryCacheDir = prev })
	return dir
}

// cacheBuild puts a build in the cache the way packbin.Fetcher admits one: at its digest, 0555.
// A build already at that digest is the same build, so it is kept: on a Linux host both sides
// share one digest, and rewriting the 0555 file is EACCES for any user but root.
func cacheBuild(t *testing.T, dir, sum, name string) string {
	t.Helper()
	p := packbin.Path(dir, sum, name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o555); err != nil {
		t.Fatal(err)
	}
	return p
}

// builds declares toold for this machine and for the jail. On a Linux host the two platforms
// are one, so the jail build wins the key and both sides share it; the tests below read the
// digest each side resolves to rather than assuming two.
func builds(platforms map[string]string) map[string]any {
	out := map[string]any{}
	for platform, sum := range platforms {
		out[platform] = map[string]any{"url": "https://example.test/toold-" +
			strings.ReplaceAll(platform, "/", "-"), "sha256": sum}
	}
	return out
}

func sideSums() (host, jail string) {
	if hostPlatform == jailPlatform {
		return sumJail, sumJail
	}
	return sumHost, sumJail
}

// toolModule writes a loophole whose host daemon, doctor and jail daemon all run toold, with
// the module's own files read-only — the shape an embedded pack's leased tree has, where no
// file can carry an exec bit.
func toolModule(t *testing.T, md string, platforms map[string]string) string {
	t.Helper()
	mod := mkdir(t, filepath.Join(md, "tool"))
	writeManifest(t, mod, map[string]any{
		"name": "tool", "description": "ships toold",
		"binaries": map[string]any{"toold": builds(platforms)},
		"host_daemon": map[string]any{"cmd": []any{"{binary:toold}", "--socket", "{socket}"},
			"publishes": "socket"},
		"doctor_cmd":  []any{"{binary:toold}", "--self-check"},
		"jail_daemon": map[string]any{"cmd": []any{"{jail_binary:toold}", "serve"}},
	})
	if err := os.Chmod(filepath.Join(mod, "manifest.jsonc"), 0o444); err != nil {
		t.Fatal(err)
	}
	return mod
}

func bothBuilds() map[string]string {
	h, j := sideSums()
	return map[string]string{hostPlatform: h, jailPlatform: j}
}

func loadTool(t *testing.T, platforms map[string]string) (*Loophole, string, string) {
	t.Helper()
	unsetJail(t)
	cache := isolateBinaryCache(t)
	md := modsDir(t)
	lp, err := LoadLoophole(toolModule(t, md, platforms))
	if err != nil {
		t.Fatal(err)
	}
	return lp, cache, md
}

func TestBinaryTokensResolveAtLoad(t *testing.T) {
	lp, cache, _ := loadTool(t, bothBuilds())
	hostSum, _ := sideSums()
	hostPath := packbin.Path(cache, hostSum, "toold")
	if lp.HostDaemon.Cmd[0] != hostPath || lp.DoctorCmd[0] != hostPath {
		t.Errorf("host fields = %q / %q, want the cached build %s", lp.HostDaemon.Cmd, lp.DoctorCmd, hostPath)
	}
	if got, want := lp.JailDaemon.Cmd[0], "/etc/yolo-jail/loophole-binaries/tool/toold"; got != want {
		t.Errorf("jail argv[0] = %q, want the container path %q", got, want)
	}
}

func TestAnUnfetchedBuildKeepsTheLoopholeOff(t *testing.T) {
	lp, cache, _ := loadTool(t, bothBuilds())
	if !lp.SupportedHere() {
		t.Fatalf("both builds are declared, so the loophole is supported here: %v",
			func() string { r, _ := lp.UnsupportedHereReason(); return r }())
	}
	if lp.Active() || lp.BinariesFetched() {
		t.Fatal("a loophole whose builds are not in the cache is Active; a launch never fetches")
	}
	reason, off := lp.InactiveReason()
	if !off || !strings.Contains(reason, "yolo pack install") || !strings.Contains(reason, "toold") {
		t.Errorf("InactiveReason = %q, want it to name the binary and `yolo pack install`", reason)
	}
	hostSum, jailSum := sideSums()
	cacheBuild(t, cache, hostSum, "toold")
	cacheBuild(t, cache, jailSum, "toold")
	if !lp.Active() {
		r, _ := lp.InactiveReason()
		t.Errorf("with every build cached the loophole is still off: %s", r)
	}
}

func TestNoBuildHereIsThePlatformAxis(t *testing.T) {
	lp, _, _ := loadTool(t, map[string]string{"plan9/386": sumHost})
	if lp.SupportedHere() || lp.Active() {
		t.Fatal("a binary with no build for this machine left the loophole supported")
	}
	reason, ok := lp.UnsupportedHereReason()
	if !ok || !strings.Contains(reason, "has no build for") || !strings.Contains(reason, "plan9/386") ||
		!strings.Contains(reason, "nothing can be installed") {
		t.Errorf("reason = %q, want the missing platform, the declared ones and 'nothing can be installed'", reason)
	}
	notes := PlatformInertNotes([]*Loophole{lp})
	if len(notes) != 1 || notes[0].Axis != AxisPlatform {
		t.Errorf("PlatformInertNotes = %+v, want one platform-axis note", notes)
	}
	if n := BinaryInertNotes([]*Loophole{lp}); len(n) != 0 {
		t.Errorf("an unsupported loophole also got a binary-axis note: %+v", n)
	}
	// The unresolvable host token stays a token rather than naming a path nothing will fill.
	if lp.HostDaemon.Cmd[0] != "{binary:toold}" {
		t.Errorf("host argv[0] = %q, want the token left in place", lp.HostDaemon.Cmd[0])
	}
}

func TestBinaryInertNotesNameTheFix(t *testing.T) {
	lp, _, _ := loadTool(t, bothBuilds())
	notes := BinaryInertNotes([]*Loophole{lp, lp})
	if len(notes) != 1 || notes[0].Axis != AxisBinary || !strings.Contains(notes[0].Line(), "yolo pack install") {
		t.Fatalf("notes = %+v, want one binary-axis note naming `yolo pack install`", notes)
	}
	off := *lp
	off.Enabled = false
	if n := BinaryInertNotes([]*Loophole{&off}); len(n) != 0 {
		t.Errorf("a disabled loophole got a note: %+v", n)
	}
}

// THE POINT OF THE MECHANISM: the module's own file is read-only (an embedded pack's is 0444),
// and the jail still gets an executable, because the build is mounted from the cache.
func TestTheJailBuildIsMountedFromTheCache(t *testing.T) {
	lp, cache, md := loadTool(t, bothBuilds())
	hostSum, jailSum := sideSums()
	cacheBuild(t, cache, hostSum, "toold")
	src := cacheBuild(t, cache, jailSum, "toold")
	_ = lp
	set := approvedSetFrom(md)
	args := set.RuntimeArgsFor(set.Enabled(), "podman")
	want := src + ":/etc/yolo-jail/loophole-binaries/tool/toold:ro"
	if !hasPair(args, "-v", want) {
		t.Fatalf("argv lacks -v %s:\n%q", want, args)
	}
	fi, err := os.Stat(src)
	if err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the mounted build carries no exec bit: %v %v", fi, err)
	}
	payload := ""
	for _, a := range args {
		if strings.HasPrefix(a, "YOLO_JAIL_DAEMONS=") {
			payload = a
		}
	}
	if !strings.Contains(payload, `"/etc/yolo-jail/loophole-binaries/tool/toold"`) {
		t.Errorf("the jail daemon's argv does not name the mounted build: %s", payload)
	}
}

func TestAnUnfetchedLoopholeMountsNothing(t *testing.T) {
	_, _, md := loadTool(t, bothBuilds())
	set := approvedSetFrom(md)
	for _, a := range set.RuntimeArgsFor(set.Enabled(), "podman") {
		if strings.Contains(a, "loophole-binaries") || strings.Contains(a, "YOLO_JAIL_DAEMONS") {
			t.Errorf("an inactive loophole reached the argv: %q", a)
		}
	}
}

func TestTheMacosUserGuestDeclinesAJailBinary(t *testing.T) {
	spec := JailDaemonSpec{Name: "tool", Cmd: []string{"/etc/yolo-jail/loophole-binaries/tool/toold"}}
	runs, declined := JailDaemonsRunIn("macos-user", []JailDaemonSpec{spec})
	if len(runs) != 0 || len(declined) != 1 || !strings.Contains(declined[0].Why, "binary") {
		t.Errorf("runs %+v declined %+v, want the jail binary declined by name", runs, declined)
	}
	if runs, _ := JailDaemonsRunIn("podman", []JailDaemonSpec{spec}); len(runs) != 1 {
		t.Error("a container runs a jail binary's daemon")
	}
}

func TestTheDoctorDoesNotRunAnUnfetchedBuild(t *testing.T) {
	_, _, md := loadTool(t, bothBuilds())
	set := approvedSetFrom(md)
	results := set.RunDoctorChecks(set.Enabled(), 0)
	if len(results) != 1 || results[0].RC != nil ||
		!strings.Contains(results[0].Output, "not run") || !strings.Contains(results[0].Output, "yolo pack install") {
		t.Errorf("doctor results = %+v, want 'not run' naming `yolo pack install`", results)
	}
}

// `yolo loopholes status` grades a loophole whose build is not fetched `inactive`: its
// self-check did not run, and `no-check` would say it declares none. Deleting the
// BinariesFetched case in doctorState fails this.
func TestLoopholesStatusGradesAnUnfetchedBuildInactive(t *testing.T) {
	_, _, md := loadTool(t, bothBuilds())
	set := approvedSetFrom(md)
	results := set.RunDoctorChecks(set.Enabled(), 0)
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	if got := doctorState(set, results[0]); got != "inactive" {
		t.Errorf("doctorState = %q, want inactive", got)
	}
}

// A NESTED LAUNCH whose own cache lacks a jail build binds on the copy its outer jail mounted,
// rather than counting the loophole on and then mounting nothing (BP-D6's in-jail rule).
// Deleting the in-jail branch of jailBinarySource fails both halves.
func TestANestedLaunchBindsTheBuildItsOuterJailMounted(t *testing.T) {
	_, _, md := loadTool(t, bothBuilds())
	outer := filepath.Join(t.TempDir(), "toold")
	if err := os.WriteFile(outer, []byte("#!/bin/sh\n"), 0o555); err != nil {
		t.Fatal(err)
	}
	prev := mountedJailBinary
	mountedJailBinary = func(loophole, name string) string {
		if loophole == "tool" && name == "toold" {
			return outer
		}
		return prev(loophole, name)
	}
	t.Cleanup(func() { mountedJailBinary = prev })
	t.Setenv("YOLO_VERSION", "test")

	set := approvedSetFrom(md)
	lp := set.All()[0]
	if !lp.BinariesFetched() || !lp.Active() {
		r, _ := lp.InactiveReason()
		t.Fatalf("in a jail whose outer launch mounted the build, the loophole is off: %s", r)
	}
	want := outer + ":/etc/yolo-jail/loophole-binaries/tool/toold:ro"
	if args := set.RuntimeArgsFor(set.Enabled(), "podman"); !hasPair(args, "-v", want) {
		t.Errorf("the nested launch does not bind the outer jail's copy (-v %s):\n%q", want, args)
	}
}

// hostToolModule writes a loophole whose one program is its HOST daemon's: no jail reference,
// so on every platform the loophole's only need is the host build, and a test of the host half
// cannot be answered by the jail half instead (on Linux the two platforms are one).
func hostToolModule(t *testing.T, md string) string {
	t.Helper()
	mod := mkdir(t, filepath.Join(md, "hosttool"))
	writeManifest(t, mod, map[string]any{
		"name": "hosttool", "description": "runs toold on the host",
		"binaries": map[string]any{"toold": builds(map[string]string{hostPlatform: sumHost})},
		"host_daemon": map[string]any{"cmd": []any{"{binary:toold}", "--socket", "{socket}"},
			"publishes": "socket"},
	})
	return mod
}

func loadHostTool(t *testing.T) (*Loophole, string, string) {
	t.Helper()
	unsetJail(t)
	cache := isolateBinaryCache(t)
	md := modsDir(t)
	lp, err := LoadLoophole(hostToolModule(t, md))
	if err != nil {
		t.Fatal(err)
	}
	return lp, cache, md
}

// Inside a jail a HOST reference is the host's business: the jail's own cache not holding it
// does not make the loophole read as off, for `requires`' reason (presence decides in a jail).
// The module names the build on the host side only, so this bites on every platform; deleting
// the `!n.Jail` half of UnfetchedBinaryReason's in-jail skip fails it.
func TestInAJailAHostBuildIsTheHostsBusiness(t *testing.T) {
	lp, _, _ := loadHostTool(t)
	if lp.BinariesFetched() {
		t.Fatal("precondition: out of a jail, an uncached host build counted as fetched")
	}
	t.Setenv("YOLO_VERSION", "test")
	if !lp.BinariesFetched() || !lp.Active() {
		r, _ := lp.InactiveReason()
		t.Errorf("in a jail, an uncached HOST build made the loophole read as off: %s", r)
	}
}

// A NESTED LAUNCH runs its host daemons in THIS jail, so the rule above (a host build is the
// host's business) cannot also decide the spawn: the argv names this jail's cache, and a build
// it lacks is a file this jail does not have. The spawn list leaves the daemon out and says why,
// as the doctor does (BP-D6), and admits it once the build is here. Deleting the
// hostBinariesUnready gate in manifestHostDaemonSpecs fails this.
func TestANestedLaunchStartsNoHostDaemonWhoseBuildItLacks(t *testing.T) {
	_, cache, md := loadHostTool(t)
	t.Setenv("YOLO_VERSION", "test")
	said := captureWarnings(t)
	set := approvedSetFrom(md)
	if specs := set.ManifestHostDaemonSpecs(set.Enabled()); specs.Len() != 0 {
		got, _ := jsonxDump(specs)
		t.Fatalf("a nested launch would spawn a host build its own cache lacks: %s", got)
	}
	if joined := strings.Join(*said, "\n"); !strings.Contains(joined, "hosttool") ||
		!strings.Contains(joined, "yolo pack install") {
		t.Errorf("the daemon left out was not explained, naming the loophole and the fix: %q", *said)
	}
	cacheBuild(t, cache, sumHost, "toold")
	if specs := set.ManifestHostDaemonSpecs(set.Enabled()); specs.Len() != 1 {
		t.Error("with the build in this jail's cache, the nested launch still leaves the daemon out")
	}
}

// The brokered half of the same rule: a scope file is written for exactly the brokers that will
// start (brokered.go), so a broker whose host build a nested launch lacks is not one of them.
// Deleting the hostBinariesUnready gate in BrokeredToStart fails this.
func TestANestedLaunchCountsNoBrokerWhoseBuildItLacks(t *testing.T) {
	unsetJail(t)
	cache := isolateBinaryCache(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "gb"))
	// Off by default, as every brokered manifest must be (OQ-BB13); switched on below as a
	// per-workspace file switches it.
	writeManifest(t, mod, map[string]any{
		"name": "gb", "description": "a broker that runs toold", "transport": "loopback-tls",
		"default_enabled": false,
		"binaries":        map[string]any{"toold": builds(map[string]string{hostPlatform: sumHost})},
		"host_daemon": map[string]any{"cmd": []any{"{binary:toold}", "{repository_scope}"},
			"publishes": "socket"},
		"brokered": map[string]any{"source": "gbsrc", "remote_host": "github.com"},
	})
	t.Setenv("YOLO_VERSION", "test")
	set := approvedSetFrom(md)
	for _, lp := range set.All() {
		lp.Enabled = true
	}
	if len(set.All()) != 1 {
		t.Fatalf("the brokered manifest did not load: %d loopholes", len(set.All()))
	}
	if got := set.BrokeredToStart(nil); len(got) != 0 {
		t.Fatalf("a nested launch counts a broker whose host build its cache lacks: %v", got[0].Name)
	}
	cacheBuild(t, cache, sumHost, "toold")
	if got := set.BrokeredToStart(nil); len(got) != 1 {
		t.Error("with the build in this jail's cache, the broker is still not counted")
	}
}
