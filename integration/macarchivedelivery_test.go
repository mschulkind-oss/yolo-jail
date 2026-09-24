package integration

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE DELTA ARCHIVE ON THE TWO MAC BACKENDS, THROUGH THE REAL LAUNCH PATH.
//
// deliverViaArchive (internal/image/autoload.go, deltaarchive.go) hands a Mac runtime an
// oci-archive that leaves out the layers the runtime already holds. Everything about it that a
// Linux machine can check is checked on Linux: archivedelta_test.go pins the placeholder
// behaviour against the real copier and the over-claim retry against a real REMOTE podman. What
// is left is docs/research/macos-layer-reusing-image-delivery.md's "What only a Mac can
// confirm", and these tests are that list run as CI instead of by hand:
//
//   - TestMacArchiveDeliveryReusesLayers — both backends. Two throwaway workspaces, two real
//     `yolo run` launches: IMAGE A (the stock image, no `packages:`) first, then IMAGE B
//     (`packages: ["hello"]`). A must actually be delivered (the preloaded image must not
//     short-circuit it) and B must go out as a delta: the copy report line names exactly the
//     layers B has that A does not, the archive is small, nothing retries, `hello` runs.
//   - TestMacArchiveFailClosedOnAppleContainer — a MEASUREMENT, reported and never failed on
//     its answer: does `container image load` exit nonzero, leaving no image, when the archive's
//     manifest names a blob neither the archive nor the content store holds? yolo's one retry
//     fires only on a nonzero exit, so this is the premise the Apple Container half rests on.
//   - TestMacArchiveFirstLoadCompressionOnPodman — a MEASUREMENT for OQ-LR2: a cold `podman
//     load` of image A from the uncompressed oci-archive yolo writes, against the same image
//     as a gzip oci-archive. Numbers only; no delivery code changes on its account.
//
// Every timing lands in the job's step summary ($GITHUB_STEP_SUMMARY) as well as the log.
//
// # Terms
//
//   - IMAGE A / IMAGE B: coined by the research doc's Mac commands and kept. A is `.#ociImage`
//     with no extras; B is the same flake with YOLO_EXTRA_PACKAGES=macArchivePackagesJSON.
//   - PRESENT SET, DELTA ARCHIVE: as defined in internal/image/deltaarchive.go.
//
// # The gate
//
// Selected by the same computed `go test -list` + zero-selection refusal the macOS jobs already
// use for their own suites, under the prefix macArchiveTestPrefix, and declared by the step that
// scheduled them (macArchiveDeclareEnv names the backend). Undeclared — every developer machine,
// every nightly shard that lists these among `.*` — they skip. Declared, anything that stops
// them from running is a failure, because a skip here is indistinguishable from a pass.
//
// # Hermetic yolo state
//
// Each test gives its launches a PRIVATE ~/.local/share/yolo-jail (macArchivePrivateState),
// where every other container test links the machine's real one back in. That is the only way
// the present sets are knowable in advance: Apple Container's present set is yolo's own
// delivery records, and on the maintainer's self-hosted Mac the real state dir holds records
// for whatever images that machine delivered last week. Only `cache/` is re-linked, so the
// jail's npm and mise downloads stay warm; nothing in the delivery path lives there.

// macArchiveDeclareEnv is how a CI step says which backend it scheduled these tests on:
// "podman" or "container". See the gate.
const macArchiveDeclareEnv = "YOLO_TEST_MAC_ARCHIVE_DELIVERY"

// macArchiveTestPrefix is the required name prefix, so a job's `go test -list '^TestMacArchive…'`
// selects exactly these and cannot silently miss one.
const macArchiveTestPrefix = "TestMacArchive"

// macArchivePackagesJSON is image B's `packages:` list, exactly as YOLO_EXTRA_PACKAGES spells it.
//
// ⚠ TWO-LANGUAGE PIN: nightly-macos.yml's `build-image` (x86_64-linux) and `push-arm-image-cache`
// (aarch64-linux) realize `.#ociImage` with this value so B's Linux closure is in Cachix — a Mac
// can substitute a Linux derivation but cannot build one. TestArchiveDeliveryWorkflowsRealizeImageB
// fails if the workflow and this constant disagree.
const macArchivePackagesJSON = `["hello"]`

// macArchiveLaunchTimeout bounds one launch here. A first delivery on the Intel runner is a
// full 3.45 GB `podman load` (10–33 min measured, research doc) PLUS a cold container start,
// which is more than YOLO_TEST_JAIL_TIMEOUT is sized for elsewhere. The job's step cap is the
// outer bound.
func macArchiveLaunchTimeout() time.Duration {
	const floor = 45 * time.Minute
	if d := jailTimeout(); d > floor {
		return d
	}
	return floor
}

// requireMacArchiveDelivery gates a test on the job that declared it, and returns the declared
// runtime. only, when non-empty, restricts the test to one backend: a job that declared the
// OTHER one skips it, which is a legitimate skip (it is the other job's measurement).
func requireMacArchiveDelivery(t *testing.T, only string) string {
	t.Helper()
	if !strings.HasPrefix(t.Name(), macArchiveTestPrefix) {
		t.Fatalf("%s is behind requireMacArchiveDelivery and is not named %s… — the CI steps "+
			"select these with `go test -list '^%s…'`, so it would never run. Rename it.",
			t.Name(), macArchiveTestPrefix, macArchiveTestPrefix)
	}
	if testing.Short() {
		t.Skip("skipping Mac archive-delivery test (-short)")
	}
	declared := strings.TrimSpace(os.Getenv(macArchiveDeclareEnv))
	if declared == "" {
		t.Skipf("skipping Mac archive-delivery test: set %s=podman|container in the job that "+
			"schedules it", macArchiveDeclareEnv)
	}
	if declared != "podman" && declared != "container" {
		t.Fatalf("%s=%q: want podman or container", macArchiveDeclareEnv, declared)
	}
	if only != "" && declared != only {
		t.Skipf("%s is a %s-only check and this job declared %s=%s", t.Name(), only,
			macArchiveDeclareEnv, declared)
	}
	var why string
	switch {
	case goruntime.GOOS != "darwin":
		why = "these are the Mac half of the delta archive and this is " + goruntime.GOOS
	case declared == "container":
		if out, err := exec.Command("container", "system", "status").CombinedOutput(); err != nil {
			why = "`container system status` failed: " + err.Error() + ": " + strings.TrimSpace(string(out))
		}
	default:
		if out, err := exec.Command("podman", "info").CombinedOutput(); err != nil {
			why = "`podman info` failed (is the Podman Machine up?): " + err.Error() + ": " +
				lastLines(string(out), 5)
		}
	}
	if why != "" {
		t.Fatalf("%s=%s, so this run was SCHEDULED to exercise the delta archive on %s, and it "+
			"cannot: %s. A skip here would read as a pass.", macArchiveDeclareEnv, declared,
			declared, why)
	}
	// Past this point nothing legitimately skips: the job declared the backend and it is here.
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s SKIPPED after its gate confirmed %s on a run that declared %s — "+
				"something inside the test gave up, and on this job that is a defect, not a skip",
				t.Name(), declared, macArchiveDeclareEnv)
		}
	})
	return declared
}

// macArchivePrivateState replaces the isolated home's link to the machine's yolo state dir with
// a private directory, re-linking only `cache/`. See the file header for why.
//
// Call it AFTER requireJail (which made the isolated home it edits). Its cleanup is registered
// after t.TempDir's, so it runs first and can clear a tree the jail wrote with modes RemoveAll
// cannot unlink through (removeWorkspaceTree).
func macArchivePrivateState(t *testing.T) {
	t.Helper()
	home := os.Getenv("HOME")
	if hostHome == "" || home == "" || home == hostHome {
		t.Fatalf("macArchivePrivateState needs requireJail's isolated home first (HOME=%q, machine home=%q)",
			home, hostHome)
	}
	state := paths.GlobalStorageUnder(home)
	if fi, err := os.Lstat(state); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(state); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeWorkspaceTree(t, state) })
	realCache := filepath.Join(paths.GlobalStorageUnder(hostHome), "cache")
	if err := os.MkdirAll(realCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realCache, filepath.Join(state, "cache")); err != nil {
		t.Fatal(err)
	}
}

// macArchiveEnv is what every launch here adds to the launcher's environment.
//
// YOLO_NO_AUTO_IMAGE_REAP is not incidental: the reaper's retention set is one pointer per
// workspace in the state dir, and a PRIVATE state dir knows only this test's two workspaces — so
// on a machine with other jail images (the maintainer's Mac) a reap would take them. The hatch
// exists for "a launch against a store with images worth keeping" (autoreapimages.go).
func macArchiveEnv(rt string) runOption {
	return withEnv("YOLO_RUNTIME="+rt, paths.TimingEnv+"=1", "YOLO_NO_AUTO_IMAGE_REAP=1")
}

// macArchiveImages is what the realize phase produced.
type macArchiveImages struct {
	flake            string // the flake the launches build from
	a, b             string // image.json store paths
	copier           string // the skopeo binary
	layersA, layersB []image.LayerInfo
	realize          []string // summary rows: what each nix build did and how long it took
}

// macArchiveFlakeRoot is the flake a launch resolves: YOLO_REPO_ROOT when the job names one
// (apple-container.yml's staged bundle), else this checkout (childRepoRootEnv's answer).
func macArchiveFlakeRoot() string {
	if r := os.Getenv("YOLO_REPO_ROOT"); r != "" {
		return r
	}
	return repoRoot
}

// nix's own summary of a build plan: "these 12 paths will be fetched (…)" / "this path will be
// fetched (…)", and the same two shapes for derivations "will be built".
var (
	nixWillBuildRe = regexp.MustCompile(`(?:these (\d+) derivations|this derivation) will be built`)
	nixWillFetchRe = regexp.MustCompile(`(?:these (\d+) paths|this path) will be fetched(?: \(([^)]*)\))?`)
)

// nixPlanCount is the count a nixWill*Re match names: the number, or 1 for the singular form.
func nixPlanCount(m []string) string {
	if m == nil {
		return "0"
	}
	if m[1] == "" {
		return "1"
	}
	return m[1]
}

// macArchiveRealize realizes A, B and the copier, timing each and recording what nix says it
// FETCHED versus BUILT — the nightly's own comments say no run has been instrumented to show a
// Mac substituting the image rather than building it, and this is that instrument.
//
// A plain `nix build`, deliberately not through a launch: a darwin launch whose build fails
// tries the Linux-builder offload, which has never worked on the Intel runner, and the failure
// would then be a slow timeout naming the wrong thing. Here it is nix's own refusal, at once.
func macArchiveRealize(t *testing.T) macArchiveImages {
	t.Helper()
	m := macArchiveImages{flake: macArchiveFlakeRoot()}
	build := func(label, attr, pkgs string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		defer cancel()
		argv := append(append([]string{}, image.NixFlakeFlags()...),
			"build", "--impure", "--no-link", "--print-out-paths", attr)
		cmd := exec.CommandContext(ctx, "nix", argv...)
		cmd.Dir = m.flake
		cmd.Env = append(envWithout("YOLO_EXTRA_PACKAGES"), "YOLO_EXTRA_PACKAGES="+pkgs)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		start := time.Now()
		err := cmd.Run()
		took := time.Since(start).Round(time.Second)
		se := stderr.String()
		fm := nixWillFetchRe.FindStringSubmatch(se)
		fetched := nixPlanCount(fm) + " paths"
		if fm != nil && fm[2] != "" {
			fetched += " (" + fm[2] + ")"
		}
		built := nixPlanCount(nixWillBuildRe.FindStringSubmatch(se)) + " derivations"
		row := fmt.Sprintf("| %s | `%s` | %s | %s | %s |", label, attr, took, fetched, built)
		m.realize = append(m.realize, row)
		if err != nil {
			t.Fatalf("realizing %s (%s, YOLO_EXTRA_PACKAGES=%q) failed after %s: %v\n\n"+
				"On the Intel nightly runner this means a Linux derivation was neither in a "+
				"substituter nor buildable here: the Mac has no Linux builder, so the closure "+
				"must come from Cachix, which nightly-macos.yml's `build-image` job fills — check "+
				"that it ran for this flake.nix/flake.lock and that CACHIX_AUTH_TOKEN is set. On "+
				"the Apple Container Mac it is the aarch64 closure (`push-arm-image-cache`) or the "+
				"Apple Container builder (NIX_CONFIG builders).\n\nnix said (last lines):\n%s",
				label, attr, pkgs, took, err, lastLines(se, 30))
		}
		var outs []string
		for _, l := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				outs = append(outs, l)
			}
		}
		if len(outs) == 0 {
			t.Fatalf("nix build %s printed no out path", attr)
		}
		// The copier has more than one output (bin and man); the image has one.
		for _, p := range outs {
			if _, err := os.Stat(image.ImageCopierBinary(p)); err == nil {
				return p
			}
		}
		return outs[0]
	}
	m.a = build("image A", image.ImageAttrDefault, "")
	m.b = build("image B", image.ImageAttrDefault, macArchivePackagesJSON)
	m.copier = image.ImageCopierBinary(build("copier", image.ImageCopierAttr, ""))
	if _, err := os.Stat(m.copier); err != nil {
		t.Fatalf("the realized copier has no %s: %v", m.copier, err)
	}
	var err error
	if m.layersA, err = image.ReadLayerInventory(m.a); err != nil || len(m.layersA) == 0 {
		t.Fatalf("image A's inventory: %d layers, %v", len(m.layersA), err)
	}
	if m.layersB, err = image.ReadLayerInventory(m.b); err != nil || len(m.layersB) == 0 {
		t.Fatalf("image B's inventory: %d layers, %v", len(m.layersB), err)
	}
	if m.a == m.b {
		t.Fatalf("image A and image B are the same store path (%s): YOLO_EXTRA_PACKAGES=%s did "+
			"not reach the flake, so there is no delta to measure", m.a, macArchivePackagesJSON)
	}
	return m
}

func envWithout(key string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, key+"=") {
			env = append(env, kv)
		}
	}
	return env
}

// macRun runs argv with a deadline and returns its combined output and exit code (-1 when it
// did not run or was killed).
func macRun(timeout time.Duration, argv ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return string(out), ee.ExitCode()
	}
	return string(out) + "\n" + err.Error(), -1
}

// macStdout is macRun reading STDOUT only, for the probes whose output is parsed: a podman
// remote client can print connection warnings on stderr, and they must not read as image IDs.
func macStdout(timeout time.Duration, argv ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return string(out), ee.ExitCode()
	}
	return string(out), -1
}

func macImagePresent(rt, ref string) bool {
	_, rc := macRun(2*time.Minute, rt, "image", "inspect", ref)
	return rc == 0
}

// macEvictImage makes ref's IMAGE absent from rt — every name it carries, not just ref, since on
// podman a second tag (the stock tag, :latest) keeps the layers and so the present set alive.
// Never forced: a force would also remove a container running on it, which on a developer's Mac
// is a live jail. Returns whether ref existed.
func macEvictImage(t *testing.T, rt, ref string) bool {
	t.Helper()
	if !macImagePresent(rt, ref) {
		return false
	}
	if rt == "container" {
		out, _ := macRun(5*time.Minute, "container", "image", "rm", ref)
		if macImagePresent(rt, ref) {
			t.Fatalf("could not remove %s from Apple Container — is a container using it? (stop "+
				"the jails running on it and re-dispatch)\n%s", ref, out)
		}
		return true
	}
	id, _ := macStdout(2*time.Minute, "podman", "image", "inspect", "--format", "{{.Id}}", ref)
	id = strings.TrimSpace(id)
	tags, _ := macStdout(2*time.Minute, "podman", "image", "inspect", "--format",
		"{{range .RepoTags}}{{println .}}{{end}}", ref)
	var log strings.Builder
	for _, tag := range strings.Fields(tags) {
		out, _ := macRun(5*time.Minute, "podman", "rmi", tag)
		log.WriteString(out)
	}
	if macImagePresent(rt, ref) || (id != "" && macImagePresent(rt, id)) {
		t.Fatalf("could not remove %s (image %s) from podman — is a container using it?\n%s",
			ref, id, log.String())
	}
	return true
}

// archiveReport is the copy report line an archive delivery prints
// (image.CopyReport.ArchiveString).
type archiveReport struct {
	sentLayers, reusedLayers int
	sent, reused, archive    string // as printed ("27 MB", "3.2 GB")
	runtime                  string
}

var archiveReportRe = regexp.MustCompile(`Copied image: (\d+) layer\(s\), ([0-9.]+ [MG]B) sent; ` +
	`(\d+) layer\(s\), ([0-9.]+ [MG]B) reused from ([A-Za-z0-9_-]+)'s store \(a ([0-9.]+ [MG]B) archive\)`)

// parseArchiveReports returns every archive copy-report line in out.
func parseArchiveReports(out string) []archiveReport {
	var rs []archiveReport
	for _, m := range archiveReportRe.FindAllStringSubmatch(out, -1) {
		s, _ := strconv.Atoi(m[1])
		r, _ := strconv.Atoi(m[3])
		rs = append(rs, archiveReport{sentLayers: s, sent: m[2], reusedLayers: r, reused: m[4],
			runtime: m[5], archive: m[6]})
	}
	return rs
}

// printedSizeBytes turns FormatImageSize's output back into bytes (to its rounding).
func printedSizeBytes(s string) int64 {
	f := strings.Fields(s)
	if len(f) != 2 {
		return -1
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return -1
	}
	switch f[1] {
	case "GB":
		return int64(v * (1 << 30))
	case "MB":
		return int64(v * (1 << 20))
	}
	return -1
}

var perfEndRe = regexp.MustCompile(`(?m) end +(image\.[a-z_]+) +dur=([0-9.]+)s`)

// launchSpans reads the image.* span durations a YOLO_TIMING launch recorded in
// <workspace>/.yolo/host-perf.log.
func launchSpans(ws string) map[string]string {
	spans := map[string]string{}
	b, err := os.ReadFile(filepath.Join(ws, ".yolo", "host-perf.log"))
	if err != nil {
		return spans
	}
	for _, m := range perfEndRe.FindAllStringSubmatch(string(b), -1) {
		spans[m[1]] = m[2] + "s"
	}
	return spans
}

func spanOr(spans map[string]string, name string) string {
	if v, ok := spans[name]; ok {
		return v
	}
	return "n/a"
}

// stepSummary writes lines to the log and, in CI, to the job's step summary.
func stepSummary(t *testing.T, lines ...string) {
	t.Helper()
	for _, l := range lines {
		t.Log(l)
	}
	p := os.Getenv("GITHUB_STEP_SUMMARY")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Logf("could not append to the step summary %s: %v", p, err)
		return
	}
	defer f.Close()
	_, _ = io.WriteString(f, strings.Join(lines, "\n")+"\n")
}

func lastLines(s string, n int) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}

func digestSet(layers []image.LayerInfo) map[string]struct{} {
	s := map[string]struct{}{}
	for _, l := range layers {
		s[l.Digest] = struct{}{}
	}
	return s
}

// splitByPresence returns how many of layers are in present, and the bytes of those that are not.
func splitByPresence(layers []image.LayerInfo, present map[string]struct{}) (inPresent int, missingBytes int64) {
	for _, l := range layers {
		if _, ok := present[l.Digest]; ok {
			inPresent++
		} else {
			missingBytes += l.Size
		}
	}
	return inPresent, missingBytes
}

// macPresentNow is the present set the NEXT delivery on rt will compute, asked the way
// production asks it: podman's own store, or (Apple Container) the delivery records in this
// test's private state whose ref is still loaded.
func macPresentNow(t *testing.T, rt string) map[string]struct{} {
	t.Helper()
	if rt == "podman" {
		return image.PresentLayerDigests(rt, func(argv []string) (string, bool) {
			out, rc := macStdout(2*time.Minute, argv...)
			return out, rc == 0
		})
	}
	present := map[string]struct{}{}
	for ref, layers := range macDeliveryRecords(t) {
		if macImagePresent(rt, ref) {
			for _, d := range layers {
				present[d] = struct{}{}
			}
		}
	}
	return present
}

// macDeliveryRecords reads the Apple Container delivery records in this test's (private) state
// dir: ref → layer digests. deltaarchive.go's deliveryRecord is the format.
func macDeliveryRecords(t *testing.T) map[string][]string {
	t.Helper()
	dir := paths.ImageDeliveryDirUnder(os.Getenv("HOME"))
	matches, _ := filepath.Glob(filepath.Join(dir, "*.delivered.json"))
	recs := map[string][]string{}
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r struct {
			Ref    string   `json:"ref"`
			Layers []string `json:"layers"`
		}
		if json.Unmarshal(b, &r) == nil && r.Ref != "" {
			recs[r.Ref] = r.Layers
		}
	}
	return recs
}

// macDelivery is one real launch and what it reported.
type macDelivery struct {
	res    result
	wall   time.Duration
	report archiveReport
	spans  map[string]string
}

// macDeliver launches ws with script and asserts what EVERY delivery here must show: the jail
// ran, a delivery happened (no stock-tag short-circuit, a load was needed), exactly one archive
// report line for this runtime, and no retry.
func macDeliver(t *testing.T, rt, label, ws, script, wantStdout string) macDelivery {
	t.Helper()
	start := time.Now()
	res := runYolo(t, ws, script, macArchiveEnv(rt), withTimeout(macArchiveLaunchTimeout()))
	d := macDelivery{res: res, wall: time.Since(start).Round(time.Second), spans: launchSpans(ws)}
	out := res.combined()
	if res.rc != 0 || !strings.Contains(res.stdout, wantStdout) {
		t.Fatalf("%s: the launch did not run its command (rc=%d, want %q on stdout)\nstdout:\n%s\nstderr:\n%s",
			label, res.rc, wantStdout, lastLines(res.stdout, 60), lastLines(res.stderr, 60))
	}
	if strings.Contains(out, "Image build skipped") {
		t.Fatalf("%s: the launch matched a STOCK TAG and delivered nothing — the preload "+
			"short-circuit this test exists to get past:\n%s", label, lastLines(out, 40))
	}
	if !strings.Contains(out, "Image load needed") {
		t.Fatalf("%s: the launch found its image already loaded, so nothing was delivered. The "+
			"eviction before it did not take.\n%s", label, lastLines(out, 40))
	}
	reps := parseArchiveReports(out)
	if len(reps) != 1 {
		t.Fatalf("%s: want exactly one archive copy report (`Copied image: … reused from %s's "+
			"store (a … archive)`), got %d. Without it this launch did not take the archive arm "+
			"(deliverViaArchive) at all.\n%s", label, rt, len(reps), lastLines(out, 60))
	}
	d.report = reps[0]
	if d.report.runtime != rt {
		t.Errorf("%s: the report names %q's store, want %q", label, d.report.runtime, rt)
	}
	if strings.Contains(out, "retrying once with the full archive") {
		t.Errorf("%s: the delta was REFUSED and retried with the full archive — the present set "+
			"over-claimed, or the loader will not import a layout with blobs left out:\n%s",
			label, lastLines(out, 60))
	}
	if strings.Contains(out, "the image copier wrote") {
		t.Errorf("%s: the copier ignored placeholders and wrote blobs the store already holds "+
			"(archivedelta_test.go's TestPlaceholderSeedingIsHonoredByTheRealCopier is the Linux "+
			"pin for this):\n%s", label, lastLines(out, 40))
	}
	return d
}

// TestMacArchiveDeliveryReusesLayers delivers image A and then image B through real launches in
// two throwaway workspaces, and asserts B went out as a delta over A.
//
// HOW THE PRELOAD IS KEPT FROM ANSWERING. Both jobs have a jail image around before this runs —
// apple-container.yml's `just load` plus its parity launches, and on a developer's podman a
// stock tag. Three things could make A's launch deliver nothing, and each is removed:
//
//   - A's CONTENT REF (`yolo-jail:<key of A's store path>`) — the name the launch inspects. It is
//     evicted first; on podman every other name on that image goes too, since a surviving tag
//     keeps the layers and so the present set.
//   - The STOCK TAG (`yolo-jail:stock-<identity>`, podman only; Apple Container never writes
//     one) — the pre-build short-circuit (stockimage.go). Evicted the same way. The launch
//     would print "Image build skipped", which macDeliver fails on.
//   - Stale DELIVERY RECORDS (Apple Container's present set) — the private state dir holds none.
//
// `yolo-jail:latest` from `just load` is left alone: a launch never resolves it, and Apple
// Container's present set does not read the store. On podman it IS in the present set if it
// shares layers with A, which is why the expected reuse for A is computed, not assumed zero.
func TestMacArchiveDeliveryReusesLayers(t *testing.T) {
	rt := requireMacArchiveDelivery(t, "")
	requireJail(t)
	macArchivePrivateState(t)
	imgs := macArchiveRealize(t)

	refA, refB := image.JailImageRef(rt, imgs.a), image.JailImageRef(rt, imgs.b)
	stockRef := ""
	if rt == "podman" {
		if id, ok := image.EvalImageIdentity(imgs.flake); ok {
			stockRef = image.StockImageRef(rt, id)
		}
	}
	hadA := macEvictImage(t, rt, refA)
	hadStock := stockRef != "" && macEvictImage(t, rt, stockRef)
	macEvictImage(t, rt, refB)
	t.Cleanup(func() {
		// B is this test's; A only if it was not here before (it is recreated either way).
		macEvictImage(t, rt, refB)
		if !hadA {
			macEvictImage(t, rt, refA)
		}
		if stockRef != "" && !hadStock && !hadA {
			macEvictImage(t, rt, stockRef)
		}
	})

	// ── Image A: the first delivery ──
	presentA := macPresentNow(t, rt)
	wantReusedA, _ := splitByPresence(imgs.layersA, presentA)
	wsA := writeProject(t, `{}`)
	a := macDeliver(t, rt, "image A", wsA, `echo MAC-ARCHIVE-A-OK`, "MAC-ARCHIVE-A-OK")
	if !macImagePresent(rt, refA) {
		t.Fatalf("image A's launch succeeded but %s is not in %s — the launch built a different "+
			"store path than %s (a different flake?), so nothing below describes it", refA, rt, imgs.a)
	}
	if a.report.sentLayers+a.report.reusedLayers != len(imgs.layersA) ||
		a.report.reusedLayers != wantReusedA {
		t.Errorf("image A: report says %d sent + %d reused; A has %d layers of which %d were "+
			"already in %s's store before the launch", a.report.sentLayers, a.report.reusedLayers,
			len(imgs.layersA), wantReusedA, rt)
	}
	if rt == "container" {
		if got := macDeliveryRecords(t)[refA]; len(got) != len(imgs.layersA) {
			t.Errorf("image A: no delivery record for %s with its %d layers under %s (got %d) — "+
				"the NEXT delivery's present set is built from this record (OQ-LR3)",
				refA, len(imgs.layersA), paths.ImageDeliveryDirUnder(os.Getenv("HOME")), len(got))
		}
	}

	// ── Image B: the delta ──
	presentB := macPresentNow(t, rt)
	wantReusedB, missingBytes := splitByPresence(imgs.layersB, presentB)
	if wantReusedB == 0 {
		t.Fatalf("before B's launch, %s's present set holds none of B's %d layers, so there is "+
			"no delta to observe — A's delivery did not leave its layers where the next "+
			"delivery looks for them", rt, len(imgs.layersB))
	}
	wsB := writeProject(t, `{"packages": `+macArchivePackagesJSON+`}`)
	b := macDeliver(t, rt, "image B", wsB, `hello`, "Hello, world!")
	if b.report.reusedLayers != wantReusedB || b.report.sentLayers != len(imgs.layersB)-wantReusedB {
		t.Errorf("image B: report says %d sent, %d reused; want %d sent (B's layers A lacks) and "+
			"%d reused", b.report.sentLayers, b.report.reusedLayers,
			len(imgs.layersB)-wantReusedB, wantReusedB)
	}
	// The archive is the layers it carries plus a manifest and a config: allow the printed
	// rounding and some metadata, nothing like a second copy of A.
	archB := printedSizeBytes(b.report.archive)
	if limit := missingBytes + 64<<20; archB < 0 || archB > limit {
		t.Errorf("image B: the delta archive is %s, want at most ~%s (B's layers A lacks, plus "+
			"metadata) — the left-out layers were written anyway", b.report.archive,
			image.FormatImageSize(limit))
	}
	if archA := printedSizeBytes(a.report.archive); a.report.reusedLayers == 0 && archA > 0 && archB*4 > archA {
		t.Errorf("image B's archive (%s) is not small beside image A's full archive (%s)",
			b.report.archive, a.report.archive)
	}
	if rt == "container" {
		if _, ok := macDeliveryRecords(t)[refB]; !ok {
			t.Errorf("image B: no delivery record for %s under %s", refB,
				paths.ImageDeliveryDirUnder(os.Getenv("HOME")))
		}
	}

	note := ""
	if rt == "container" && macImagePresent(rt, "yolo-jail:latest") {
		// Apple Container's present set is yolo's records, which the private state dir starts
		// without — so A's ARCHIVE was full. Its content store is another matter: `just load`
		// put this image there under :latest, so the import can skip every blob and A's time
		// is not a cold one.
		note = "_Note: `yolo-jail:latest` (from `just load`) was loaded, so Apple Container's " +
			"content store already held A's blobs; A's archive was full but its import was not cold._"
	}
	stepSummary(t,
		fmt.Sprintf("### Delta-archive delivery through a real launch — %s (%s)", rt, goruntime.GOARCH),
		"",
		"| realize | attr | took | substituted | built |",
		"| --- | --- | --- | --- | --- |",
		strings.Join(imgs.realize, "\n"),
		"",
		"| delivery | layers sent | layers reused | archive | `image.layer_copy` (copy+tar+load) | `image.nix_build` | whole launch |",
		"| --- | --- | --- | --- | --- | --- | --- |",
		fmt.Sprintf("| A — stock, first | %d (%s) | %d (%s) | %s | %s | %s | %s |",
			a.report.sentLayers, a.report.sent, a.report.reusedLayers, a.report.reused,
			a.report.archive, spanOr(a.spans, "image.layer_copy"), spanOr(a.spans, "image.nix_build"), a.wall),
		fmt.Sprintf("| B — `packages: %s` | %d (%s) | %d (%s) | %s | %s | %s | %s |",
			macArchivePackagesJSON, b.report.sentLayers, b.report.sent, b.report.reusedLayers,
			b.report.reused, b.report.archive, spanOr(b.spans, "image.layer_copy"),
			spanOr(b.spans, "image.nix_build"), b.wall),
		"",
		note,
		"",
	)
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// Apple Container: does a delta with a blob nobody holds FAIL CLOSED?
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestMacArchiveFailClosedOnAppleContainer is the research doc's step 2b, and a MEASUREMENT:
// both answers pass, and only a run that failed to conduct the experiment is red (the rule
// TestAppleContainerReachesHostLoopback states at length).
//
// THE QUESTION. deliverViaArchive retries a failed delta once with the full archive, and it can
// only notice the failure as a NONZERO EXIT from `container image load`. If Apple Container
// instead exits 0 on a manifest naming a blob that neither the archive nor its content store
// holds, an over-claimed present set leaves an image that inspects as present and cannot run —
// and yolo would never retry. deltaarchive.go calls this premise SOURCED and unrun.
//
// THE ARCHIVE is yolo's own delta, made by the production path (AutoLoadImage with image B and a
// present set claiming every one of B's layers, so it carries a manifest and a config and no
// layer at all), with ONE CHANGE: a fabricated layer appended to the manifest and the config's
// diff_ids, under a random digest (failClosedProbeArchive). That is what makes the missing blob
// certain rather than likely. The research doc's version — the delta for B loaded into a store
// "without A" — depends on what the content store kept from earlier runs, and on a persistent
// Mac nobody here has verified that `container image rm` drops orphaned blobs. The image is
// renamed to a throwaway ref so nothing real is touched, and removed afterwards however it came
// out.
func TestMacArchiveFailClosedOnAppleContainer(t *testing.T) {
	rt := requireMacArchiveDelivery(t, "container")
	requireJail(t)
	macArchivePrivateState(t)
	imgs := macArchiveRealize(t)

	refB := image.JailImageRef(rt, imgs.b)
	macEvictImage(t, rt, refB) // or AutoLoadImage finds it and never builds an archive
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	probeRef := image.JailImageRepository(rt) + ":failclosed-" + nonce
	t.Cleanup(func() { _, _ = macRun(2*time.Minute, "container", "image", "rm", probeRef) })

	var (
		calls    int
		probe    failClosedProbe
		probeErr error
		loadOut  string
		loadRC   int
		left     bool
		listed   bool
	)
	var out bytes.Buffer
	o := image.AutoLoadOptions{
		Runtime:        rt,
		RepoRoot:       imgs.flake,
		IsMacOS:        true,
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return imgs.b, nil },
		BuildOffload:   func(string, []any, string) (string, []string) { return "", nil },
		BuildCopier:    func(string) (string, []string) { return imgs.copier, nil },
		EvalIdentity:   func(string) (string, bool) { return "", false },
		PresentDigests: func() map[string]struct{} { return digestSet(imgs.layersB) },
		LoadArchive: func(path string) bool {
			calls++
			if calls > 1 {
				return false // never the full-archive retry: that is not what is measured
			}
			dst := filepath.Join(resolvedTempDir(t), "failclosed.oci-archive")
			probe, probeErr = failClosedProbeArchive(path, dst, probeRef)
			if probeErr != nil {
				return true
			}
			loadOut, loadRC = macRun(10*time.Minute, "container", "image", "load", "-i", dst)
			left = macImagePresent(rt, probeRef)
			ls, _ := macRun(2*time.Minute, "container", "image", "ls", "--quiet")
			listed = strings.Contains(ls, probeRef)
			// true, so AutoLoadImage stops here: the record it then writes is in the private
			// state dir and is discarded with it.
			return true
		},
	}
	image.AutoLoadImage(o)
	if calls == 0 {
		t.Fatalf("the delivery never reached the loader, so nothing was measured:\n%s", out.String())
	}
	if probeErr != nil {
		t.Fatalf("could not build the fail-closed probe archive from yolo's delta: %v\n%s", probeErr, out.String())
	}
	if probe.layerBlobs != 0 {
		t.Logf("NOTE: yolo's delta carried %d layer blob(s) although every layer was claimed "+
			"present; the probe drops them, so the over-claim still stands", probe.layerBlobs)
	}
	if loadRC == -1 {
		t.Fatalf("`container image load` did not complete (killed or not started), so nothing "+
			"was measured:\n%s", loadOut)
	}

	ver, _ := macRun(time.Minute, "container", "--version")
	var verdict string
	switch {
	case loadRC != 0 && !left:
		verdict = fmt.Sprintf("FAILS CLOSED — exit %d, no image left. The premise holds: an "+
			"over-claimed present set makes the load fail, and yolo's single retry with the full "+
			"archive fires.", loadRC)
	case loadRC == 0 && left:
		verdict = "⚠ LOADS AN IMAGE WITH A MISSING BLOB — exit 0 and the image inspects as " +
			"present. yolo's retry fires only on a nonzero exit, so an over-claim on this " +
			"release would leave an image that cannot run. The Apple Container present set " +
			"(delivery records, OQ-LR3) then needs a check after the load, or the registry route."
	case loadRC == 0:
		verdict = "exit 0 but NO image under the probe ref — the load silently did nothing. " +
			"yolo would report success for an image that is not there; its inspect-before-run " +
			"is then the only thing that notices."
	default:
		verdict = fmt.Sprintf("exit %d BUT an image was left under the probe ref — the retry "+
			"fires, and a half-written image remains beside the full one.", loadRC)
	}
	t.Logf("AC-FAIL-CLOSED (%s): %s\nload output:\n%s", strings.TrimSpace(ver), verdict, lastLines(loadOut, 20))
	stepSummary(t,
		"### Apple Container fail-closed check (a delta naming a blob no store holds)",
		"",
		fmt.Sprintf("- `container --version`: %s", strings.TrimSpace(ver)),
		fmt.Sprintf("- manifest names %d layers; the archive holds none of them; one (`%s…`) exists nowhere",
			probe.manifestLayers, probe.phantom[:19]),
		fmt.Sprintf("- `container image load` exit: **%d**; image left: **%v** (listed: %v)", loadRC, left, listed),
		"- verdict: "+verdict,
		"",
	)
}

// failClosedProbe describes the archive failClosedProbeArchive wrote.
type failClosedProbe struct {
	phantom        string // the fabricated layer digest
	manifestLayers int    // layers the rewritten manifest names
	layerBlobs     int    // layer blobs the INPUT archive carried (a delta claiming all: 0)
}

// failClosedProbeArchive rewrites the oci-archive at src into dst with one fabricated layer
// appended, renamed to ref. The result is a coherent image — every digest matches its content,
// the config's diff_ids list the manifest's layers — whose one blob exists nowhere.
//
// Only the metadata is carried over (oci-layout, index.json, the new manifest and config).
// Layer blobs in src are dropped: the probe's point is that the manifest names blobs the archive
// does not hold. Pure file work, so TestFailClosedProbeArchiveIsCoherent pins it on Linux.
func failClosedProbeArchive(src, dst, ref string) (failClosedProbe, error) {
	var p failClosedProbe
	files, blobs, layerBlobs, err := readSmallOCIArchive(src)
	if err != nil {
		return p, err
	}
	p.layerBlobs = layerBlobs
	idx, err := decodeJSONObject(files["index.json"])
	if err != nil {
		return p, fmt.Errorf("index.json: %w", err)
	}
	manifests, _ := idx["manifests"].([]any)
	if len(manifests) != 1 {
		return p, fmt.Errorf("index.json names %d manifests, want 1", len(manifests))
	}
	desc, _ := manifests[0].(map[string]any)
	mdig, _ := desc["digest"].(string)
	man, err := decodeJSONObject(blobs[mdig])
	if err != nil {
		return p, fmt.Errorf("manifest %s: %w", mdig, err)
	}
	cdesc, _ := man["config"].(map[string]any)
	cdig, _ := cdesc["digest"].(string)
	cfg, err := decodeJSONObject(blobs[cdig])
	if err != nil {
		return p, fmt.Errorf("config %s: %w", cdig, err)
	}

	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return p, err
	}
	sum := sha256.Sum256(random)
	p.phantom = "sha256:" + hex.EncodeToString(sum[:])

	layers, _ := man["layers"].([]any)
	layers = append(layers, map[string]any{
		"mediaType": "application/vnd.oci.image.layer.v1.tar",
		"digest":    p.phantom,
		"size":      json.Number("4096"),
	})
	man["layers"] = layers
	p.manifestLayers = len(layers)
	rootfs, _ := cfg["rootfs"].(map[string]any)
	if rootfs == nil {
		return p, fmt.Errorf("config %s has no rootfs", cdig)
	}
	diffs, _ := rootfs["diff_ids"].([]any)
	rootfs["diff_ids"] = append(diffs, p.phantom) // uncompressed: digest == diffID
	if hist, ok := cfg["history"].([]any); ok {
		cfg["history"] = append(hist, map[string]any{"created_by": "yolo fail-closed probe: a layer no store holds"})
	}

	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		return p, err
	}
	newC := sha256Digest(cfgBytes)
	cdesc["digest"], cdesc["size"] = newC, json.Number(strconv.Itoa(len(cfgBytes)))
	manBytes, err := json.Marshal(man)
	if err != nil {
		return p, err
	}
	newM := sha256Digest(manBytes)
	desc["digest"], desc["size"] = newM, json.Number(strconv.Itoa(len(manBytes)))
	ann, _ := desc["annotations"].(map[string]any)
	if ann == nil {
		ann = map[string]any{}
		desc["annotations"] = ann
	}
	ann["org.opencontainers.image.ref.name"] = ref
	idxBytes, err := json.Marshal(idx)
	if err != nil {
		return p, err
	}

	layout := files["oci-layout"]
	if layout == nil {
		layout = []byte(`{"imageLayoutVersion":"1.0.0"}`)
	}
	return p, writeOCIArchive(dst, []ociEntry{
		{"oci-layout", layout},
		{"index.json", idxBytes},
		{"blobs/sha256/" + strings.TrimPrefix(newC, "sha256:"), cfgBytes},
		{"blobs/sha256/" + strings.TrimPrefix(newM, "sha256:"), manBytes},
	})
}

type ociEntry struct {
	name string
	data []byte
}

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func decodeJSONObject(b []byte) (map[string]any, error) {
	if b == nil {
		return nil, errors.New("missing")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // sizes stay integers through the round trip
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// readSmallOCIArchive reads an oci-archive's top-level files and every blob under 1 MiB
// (manifests, configs), counting the larger blobs without reading them.
func readSmallOCIArchive(path string) (files map[string][]byte, blobs map[string][]byte, large int, err error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, err
	}
	defer fh.Close()
	files, blobs = map[string][]byte{}, map[string][]byte{}
	tr := tar.NewReader(fh)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, 0, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(h.Name, "./")
		if hexd, ok := strings.CutPrefix(name, "blobs/sha256/"); ok {
			if h.Size >= 1<<20 {
				large++
				continue
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, 0, err
			}
			blobs["sha256:"+hexd] = b
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, 0, err
		}
		files[name] = b
	}
	return files, blobs, large, nil
}

func writeOCIArchive(dst string, entries []ociEntry) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	for _, d := range []string{"blobs/", "blobs/sha256/"} {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: d, Mode: 0o755}); err != nil {
			f.Close()
			return err
		}
	}
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: e.name, Mode: 0o644,
			Size: int64(len(e.data))}); err != nil {
			f.Close()
			return err
		}
		if _, err := tw.Write(e.data); err != nil {
			f.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// OQ-LR2: the first load, uncompressed (what yolo writes) against gzip.
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestMacArchiveFirstLoadCompressionOnPodman times a COLD `podman load` of image A twice — from
// the uncompressed oci-archive yolo's own writer produces, and from a gzip oci-archive of the
// same image — and reports both. A MEASUREMENT for OQ-LR2 ("measure the candidates … then build
// what measurably wins"): nothing is asserted about which is faster, and no delivery code
// changes on it.
//
// The uncompressed archive comes from AutoLoadImage itself (empty present set, the real copier,
// yolo's tar), with the loader seam timing the real `podman load -i` — so the number is the load
// of exactly the file a first delivery hands podman. The gzip archive is skopeo's own
// `oci-archive:` destination, whose default for an OCI destination is gzip (deltaarchive.go's
// ociAcceptUncompressedFlag says why yolo overrides that); its layer media types are checked, so
// a copier that stopped compressing fails this as a broken instrument instead of reporting two
// uncompressed numbers.
//
// COLD means A's image is evicted before each load, and the report says how many of A's layers
// the store held anyway (other jail images). On the nightly's fresh machine that is zero. One
// sample each, uncompressed first; the summary says so.
func TestMacArchiveFirstLoadCompressionOnPodman(t *testing.T) {
	rt := requireMacArchiveDelivery(t, "podman")
	requireJail(t)
	macArchivePrivateState(t)
	imgs := macArchiveRealize(t)
	refA := image.JailImageRef(rt, imgs.a)
	loadTimeout := macArchiveLaunchTimeout()

	// ── uncompressed, through yolo's own writer ──
	hadA := macEvictImage(t, rt, refA)
	if hadA {
		t.Logf("%s was loaded before this measurement and has been evicted for it", refA)
	}
	// A failure between the load and the eviction below must not leave this test's copy behind.
	t.Cleanup(func() { macEvictImage(t, rt, refA) })
	warmU, _ := splitByPresence(imgs.layersA, macPresentNow(t, rt))
	var (
		writeU, loadU time.Duration
		sizeU         int64
		outU          string
		rcU           = -1
	)
	start := time.Now()
	var out bytes.Buffer
	res := image.AutoLoadImage(image.AutoLoadOptions{
		Runtime:        rt,
		RepoRoot:       imgs.flake,
		IsMacOS:        true,
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return imgs.a, nil },
		BuildOffload:   func(string, []any, string) (string, []string) { return "", nil },
		BuildCopier:    func(string) (string, []string) { return imgs.copier, nil },
		EvalIdentity:   func(string) (string, bool) { return "", false },
		PresentDigests: func() map[string]struct{} { return map[string]struct{}{} },
		LoadArchive: func(path string) bool {
			writeU = time.Since(start)
			if st, err := os.Stat(path); err == nil {
				sizeU = st.Size()
			}
			t0 := time.Now()
			outU, rcU = macRun(loadTimeout, rt, "load", "-i", path)
			loadU = time.Since(t0)
			return rcU == 0
		},
	})
	if !res.OK || rcU != 0 || !macImagePresent(rt, refA) {
		t.Fatalf("the uncompressed load did not land %s (rc=%d):\n%s\n%s", refA, rcU,
			lastLines(outU, 20), lastLines(out.String(), 30))
	}
	macEvictImage(t, rt, refA)

	// ── gzip, through skopeo's oci-archive destination ──
	gz := filepath.Join(resolvedTempDir(t), "image-a.gzip.oci-archive")
	gzRef := image.JailImageRepository(rt) + ":lr2-gzip-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() { macEvictImage(t, rt, gzRef) })
	t0 := time.Now()
	cout, crc := macRun(40*time.Minute, imgs.copier, "--insecure-policy", "copy", "nix:"+imgs.a,
		"oci-archive:"+gz+":"+gzRef)
	writeG := time.Since(t0)
	if crc != 0 {
		t.Fatalf("writing the gzip oci-archive failed (rc=%d):\n%s", crc, lastLines(cout, 20))
	}
	gzipped, total, err := ociArchiveLayerCompression(gz)
	if err != nil || gzipped != total || total == 0 {
		t.Fatalf("the gzip variant is not gzip: %d of %d layers carry a +gzip media type (%v) — "+
			"two uncompressed numbers would be reported as a comparison", gzipped, total, err)
	}
	var sizeG int64
	if st, err := os.Stat(gz); err == nil {
		sizeG = st.Size()
	}
	warmG, _ := splitByPresence(imgs.layersA, macPresentNow(t, rt))
	t0 = time.Now()
	outG, rcG := macRun(loadTimeout, rt, "load", "-i", gz)
	loadG := time.Since(t0)
	if rcG != 0 || !macImagePresent(rt, gzRef) {
		t.Fatalf("the gzip load did not land %s (rc=%d):\n%s", gzRef, rcG, lastLines(outG, 20))
	}
	macEvictImage(t, rt, gzRef)
	_ = os.Remove(gz)

	r := func(d time.Duration) string { return d.Round(100 * time.Millisecond).String() }
	stepSummary(t,
		"### OQ-LR2: first `podman load` of image A — uncompressed (yolo's archive) vs gzip",
		"",
		"| archive | size | write | `podman load` | write + load | A's layers already in the store |",
		"| --- | --- | --- | --- | --- | --- |",
		fmt.Sprintf("| uncompressed oci-archive (yolo: copy + tar) | %s | %s | **%s** | %s | %d of %d |",
			image.FormatImageSize(sizeU), r(writeU), r(loadU), r(writeU+loadU), warmU, len(imgs.layersA)),
		fmt.Sprintf("| gzip oci-archive (skopeo `oci-archive:`) | %s | %s | **%s** | %s | %d of %d |",
			image.FormatImageSize(sizeG), r(writeG), r(loadG), r(writeG+loadG), warmG, len(imgs.layersA)),
		"",
		"One sample each, uncompressed first, each into a store with A evicted. Measurement only (OQ-LR2); no delivery code depends on it.",
		"",
	)
}

// ociArchiveLayerCompression reports how many of an oci-archive's manifest layers carry a gzip
// media type, out of how many.
func ociArchiveLayerCompression(path string) (gzipped, total int, err error) {
	files, blobs, _, err := readSmallOCIArchive(path)
	if err != nil {
		return 0, 0, err
	}
	idx, err := decodeJSONObject(files["index.json"])
	if err != nil {
		return 0, 0, fmt.Errorf("index.json: %w", err)
	}
	manifests, _ := idx["manifests"].([]any)
	if len(manifests) != 1 {
		return 0, 0, fmt.Errorf("index.json names %d manifests", len(manifests))
	}
	desc, _ := manifests[0].(map[string]any)
	mdig, _ := desc["digest"].(string)
	man, err := decodeJSONObject(blobs[mdig])
	if err != nil {
		return 0, 0, fmt.Errorf("manifest: %w", err)
	}
	layers, _ := man["layers"].([]any)
	for _, l := range layers {
		total++
		if m, _ := l.(map[string]any); m != nil {
			if mt, _ := m["mediaType"].(string); strings.HasSuffix(mt, "+gzip") {
				gzipped++
			}
		}
	}
	return gzipped, total, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────
// The Linux-checkable halves of the instruments above (no container; run under -short).
// Deliberately NOT named TestMacArchive…: the Mac jobs select that prefix as "facts only that
// hardware can produce", and these need none.
// ─────────────────────────────────────────────────────────────────────────────────────────

// TestArchiveReportParserReadsTheProductionLine pins the parser to the formatter it reads. If
// ArchiveString's wording moves, the Mac jobs would report "no archive copy report" — a failure
// naming the wrong cause — so the drift is caught here instead.
func TestArchiveReportParserReadsTheProductionLine(t *testing.T) {
	layers := []image.LayerInfo{{Digest: "sha256:a", Size: 3 << 30}, {Digest: "sha256:b", Size: 27 << 20}}
	for _, rt := range []string{"podman", "container"} {
		line := "  Copied image: " + image.ReportFor(layers, map[string]struct{}{"sha256:a": {}}).
			ArchiveString(28<<20, rt)
		got := parseArchiveReports("noise\n" + line + "\nDone: loaded image\n")
		if len(got) != 1 {
			t.Fatalf("%s: parsed %d report lines out of %q", rt, len(got), line)
		}
		r := got[0]
		if r.sentLayers != 1 || r.reusedLayers != 1 || r.runtime != rt {
			t.Errorf("%s: parsed %+v from %q", rt, r, line)
		}
		if b := printedSizeBytes(r.archive); b != 28<<20 {
			t.Errorf("%s: archive %q read back as %d bytes, want %d", rt, r.archive, b, 28<<20)
		}
		if b := printedSizeBytes(r.reused); b != 3<<30 {
			t.Errorf("%s: reused %q read back as %d bytes, want %d", rt, r.reused, b, 3<<30)
		}
	}
	// The COPY arm's line (Linux podman) is a different claim and must not be read as an
	// archive delivery.
	copyLine := "  Copied image: " + image.ReportFor(layers, nil).String()
	if got := parseArchiveReports(copyLine); len(got) != 0 {
		t.Errorf("the containers-storage copy line %q parsed as an archive report", copyLine)
	}
}

// TestFailClosedProbeArchiveIsCoherent runs failClosedProbeArchive on a small hand-built delta
// and checks the one thing the Mac measurement depends on: the result is a COHERENT image whose
// only defect is a blob that exists nowhere. A probe with a broken digest or a config that
// disagrees with its manifest would make `container image load` fail for a reason unrelated to
// the question, and the measurement would report "fails closed" having measured nothing.
func TestFailClosedProbeArchiveIsCoherent(t *testing.T) {
	dir := t.TempDir()
	layer := "sha256:" + strings.Repeat("ab", 32)
	cfg := []byte(`{"architecture":"arm64","os":"linux","rootfs":{"type":"layers","diff_ids":["` +
		layer + `"]},"history":[{"created_by":"nix"}]}`)
	cdig := sha256Digest(cfg)
	man := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + cdig +
		`","size":` + strconv.Itoa(len(cfg)) + `},"layers":[{"mediaType":` +
		`"application/vnd.oci.image.layer.v1.tar","digest":"` + layer + `","size":123456789}]}`)
	mdig := sha256Digest(man)
	idx := []byte(`{"schemaVersion":2,"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"digest":"` + mdig + `","size":` + strconv.Itoa(len(man)) +
		`,"annotations":{"org.opencontainers.image.ref.name":"yolo-jail:0123456789abcdef"}}]}`)
	src := filepath.Join(dir, "delta.oci-archive")
	if err := writeOCIArchive(src, []ociEntry{
		{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{"index.json", idx},
		{"blobs/sha256/" + strings.TrimPrefix(cdig, "sha256:"), cfg},
		{"blobs/sha256/" + strings.TrimPrefix(mdig, "sha256:"), man},
	}); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "probe.oci-archive")
	p, err := failClosedProbeArchive(src, dst, "yolo-jail:failclosed-x")
	if err != nil {
		t.Fatal(err)
	}
	files, blobs, large, err := readSmallOCIArchive(dst)
	if err != nil {
		t.Fatal(err)
	}
	for d, b := range blobs {
		if sha256Digest(b) != d {
			t.Errorf("blob %s does not hash to its name", d)
		}
	}
	if large != 0 || p.layerBlobs != 0 {
		t.Errorf("the probe carries layer blobs (large=%d, input=%d)", large, p.layerBlobs)
	}
	gotIdx, _ := decodeJSONObject(files["index.json"])
	desc := gotIdx["manifests"].([]any)[0].(map[string]any)
	if ann := desc["annotations"].(map[string]any)["org.opencontainers.image.ref.name"]; ann != "yolo-jail:failclosed-x" {
		t.Errorf("the probe is named %v, want the throwaway ref", ann)
	}
	gotMan, err := decodeJSONObject(blobs[desc["digest"].(string)])
	if err != nil {
		t.Fatalf("index.json names manifest %v, which is not in the archive: %v", desc["digest"], err)
	}
	if n, _ := desc["size"].(json.Number).Int64(); int(n) != len(blobs[desc["digest"].(string)]) {
		t.Errorf("index.json's manifest size %d disagrees with the blob", n)
	}
	layers := gotMan["layers"].([]any)
	if len(layers) != 2 || p.manifestLayers != 2 {
		t.Fatalf("manifest has %d layers, want the original plus the phantom", len(layers))
	}
	if d := layers[1].(map[string]any)["digest"]; d != p.phantom || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(p.phantom) {
		t.Errorf("the appended layer is %v, want a well-formed phantom %s", d, p.phantom)
	}
	if _, in := blobs[p.phantom]; in {
		t.Error("the phantom blob is IN the archive, so nothing is missing")
	}
	cdesc := gotMan["config"].(map[string]any)
	gotCfg, err := decodeJSONObject(blobs[cdesc["digest"].(string)])
	if err != nil {
		t.Fatalf("the manifest names config %v, which is not in the archive: %v", cdesc["digest"], err)
	}
	diffs := gotCfg["rootfs"].(map[string]any)["diff_ids"].([]any)
	if len(diffs) != 2 || diffs[0] != layer || diffs[1] != p.phantom {
		t.Errorf("config diff_ids = %v, want [%s %s] — out of step with the manifest", diffs, layer, p.phantom)
	}
	if h := gotCfg["history"].([]any); len(h) != 2 {
		t.Errorf("config history has %d entries for 2 layers", len(h))
	}
	// The original blobs are not carried: the old manifest and config would only be clutter.
	var names []string
	for d := range blobs {
		names = append(names, d)
	}
	sort.Strings(names)
	if len(names) != 2 {
		t.Errorf("the probe carries %d blobs %v, want exactly the new manifest and config", len(names), names)
	}
}
