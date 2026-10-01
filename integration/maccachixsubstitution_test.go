package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// DOES A MAC SUBSTITUTE THE JAIL IMAGE FROM THIS PROJECT'S CACHE, OR BUILD IT?
// (docs/plans/handoff-cachix-cache.md, its `next`; nightly-macos.yml's `Load jail image` step.)
//
// THE GAP. CI pushes the image closure to the project's Cachix cache on every nightly
// (`build-image` for x86_64-linux, `push-arm-image-cache` for aarch64-linux) and on every
// release, and CI on Linux has been seen substituting it back. No run has shown a MAC doing so:
// the nightly's own comment says "no run has been instrumented to show a substitution rather
// than a build", and TestExtraPackageLibFarm spending up to its 40-minute budget on the Intel
// shards is the symptom it leaves unexplained. The archive-delivery job's realize phase
// (macArchiveRealize) records fetched/built COUNTS for the images it builds; this test asks the
// sharper question for the image variants the shards' own launches ask for, and names the cache
// each fetched path would come from.
//
// WHAT IT DOES, per variant, from the flake a launch resolves (macArchiveFlakeRoot):
// `nix build --dry-run` of `.#ociImage` with YOLO_EXTRA_PACKAGES as the launch would set it,
// through image.NixFlakeFlags (so `--accept-flake-config`, the flag that makes nix consult the
// flake's own substituter). nix prints the plan without realizing anything: the derivations it
// would BUILD and the paths it would FETCH. Each to-be-built derivation is named with its system
// (`nix derivation show`), since a darwin build is fine on a Mac and a Linux one needs a builder
// the Intel runner does not have. Each to-be-fetched path is asked of the project's cache
// directly (`HEAD <cache>/<hash>.narinfo`; the cache URL is read from flake.nix's nixConfig, the
// one place it is declared), so the record says how many come from it.
//
// THE VARIANTS are the stock image and the two `packages:` lists the nightly's `build-image`
// realizes for the shards' package tests and pushes for exactly this reason (`["zbar"]` and
// `["libsodium.dev"]`). A variant whose closure an earlier test in the same shard already
// realized lists nothing, and the record says that this run cannot tell how it arrived.
//
// # A MEASUREMENT: every answer passes
//
// One `CACHIX <variant> …` line per variant in the log and the step summary. What fails is the
// instrument: a dry run that exits non-zero printing no plan, or a flake.nix with no
// substituter to ask. Runs on any darwin host with nix; the nightly's shards are where it is
// scheduled (it falls into one shard of the computed partition), and it builds nothing.
func TestMacImageSubstitutesFromCachix(t *testing.T) {
	requireJail(t)
	if goruntime.GOOS != "darwin" {
		t.Skip("the question is whether a MAC substitutes the Linux image closure; this is " + goruntime.GOOS)
	}
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("no `nix` on PATH, so there is no build plan to read")
	}
	flake := macArchiveFlakeRoot()
	cache, err := flakeSubstituter(filepath.Join(flake, "flake.nix"))
	if err != nil {
		t.Fatalf("NOTHING WAS MEASURED: %v", err)
	}
	rows := []string{
		"### Does this Mac substitute the jail image from " + cache + "? (measurement only)",
		"",
		"| variant | would build | would fetch | of which from " + cache + " |",
		"| :--- | :--- | :--- | :--- |",
	}
	var verdicts []string
	for _, v := range []struct{ label, pkgs string }{
		{"stock", ""},
		{"zbar", `["zbar"]`},
		{"libsodium.dev", `["libsodium.dev"]`},
	} {
		plan, out := cachixDryRun(t, flake, v.pkgs)
		if plan == nil {
			t.Fatalf("CACHIX %s: `nix build --dry-run %s` printed no plan, so NOTHING WAS MEASURED "+
				"for it:\n%s", v.label, image.ImageAttrDefault, lastLines(out, 30))
		}
		built := cachixDescribeBuilds(plan.build)
		hits := cachixNarinfoHits(cache, plan.fetch)
		rows = append(rows, fmt.Sprintf("| %s | %d: %s | %d | %d |",
			v.label, len(plan.build), built, len(plan.fetch), hits))
		verdicts = append(verdicts, "- "+cachixVerdict(v.label, plan, hits, cache))
	}
	stepSummary(t, append(append(rows, ""), append(verdicts, "",
		"Record it in docs/plans/handoff-cachix-cache.md (its status line and the Final test).", "")...)...)
}

// nixDryRunPlan is what `nix build --dry-run` says it would do.
type nixDryRunPlan struct {
	build []string // .drv paths it would build
	fetch []string // store paths it would substitute
	// ignored is nix's own warning when it dropped a flake-declared substituter because this
	// user is not trusted, or "". With it the plan never asked the project cache at all.
	ignored string
}

var (
	nixPlanBuildHeader = regexp.MustCompile(`^(?:these \d+ derivations|this derivation) will be built:`)
	nixPlanFetchHeader = regexp.MustCompile(`^(?:these \d+ paths|this path) will be fetched`)
)

// parseNixDryRun reads nix's plan out of its stderr: the store paths listed under each of the two
// headers. ok is false when neither header appears and nix failed, which is a dry run that
// planned nothing rather than one with nothing to do.
func parseNixDryRun(stderr string, failed bool) (*nixDryRunPlan, bool) {
	plan := &nixDryRunPlan{}
	var into *[]string
	seen := false
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(trimmed, "ignoring untrusted substituter"):
			plan.ignored, into = trimmed, nil
		case nixPlanBuildHeader.MatchString(trimmed):
			into, seen = &plan.build, true
		case nixPlanFetchHeader.MatchString(trimmed):
			into, seen = &plan.fetch, true
		case into != nil && strings.HasPrefix(trimmed, "/nix/store/"):
			*into = append(*into, trimmed)
		default:
			into = nil
		}
	}
	if !seen && failed {
		return nil, false
	}
	return plan, true
}

// cachixDryRun runs the dry run for one YOLO_EXTRA_PACKAGES value ("" is the stock image).
func cachixDryRun(t *testing.T, flake, pkgs string) (*nixDryRunPlan, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	argv := append(append([]string{}, image.NixFlakeFlags()...),
		"build", "--dry-run", "--impure", image.ImageAttrDefault)
	cmd := exec.CommandContext(ctx, "nix", argv...)
	cmd.Dir = flake
	cmd.Env = envWithout("YOLO_EXTRA_PACKAGES")
	if pkgs != "" {
		cmd.Env = append(cmd.Env, "YOLO_EXTRA_PACKAGES="+pkgs)
	}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	err := cmd.Run()
	plan, ok := parseNixDryRun(stderr.String(), err != nil)
	if !ok {
		return nil, stderr.String() + fmt.Sprintf("\n(%v)", err)
	}
	return plan, stderr.String()
}

// flakeSubstituter is the first `extra-substituters` URL flake.nix's nixConfig declares.
func flakeSubstituter(flakeNix string) (string, error) {
	body, err := os.ReadFile(flakeNix)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", flakeNix, err)
	}
	m := regexp.MustCompile(`extra-substituters\s*=\s*\[\s*"([^"]+)"`).FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("%s declares no extra-substituters, so there is no project cache to ask", flakeNix)
	}
	return strings.TrimRight(string(m[1]), "/"), nil
}

// cachixDescribeBuilds names each to-be-built derivation with its system, or "none".
func cachixDescribeBuilds(drvs []string) string {
	if len(drvs) == 0 {
		return "none"
	}
	var out []string
	for _, d := range drvs {
		out = append(out, strings.TrimSuffix(nixStoreName(d), ".drv")+" ("+nixDrvSystem(d)+")")
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// nixDrvSystem is a derivation's `system`, or "system unknown".
func nixDrvSystem(drv string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nix", "--extra-experimental-features", "nix-command",
		"derivation", "show", drv).Output()
	if err != nil {
		return "system unknown"
	}
	return drvSystemFromShow(out)
}

// drvSystemFromShow reads `system` out of `nix derivation show` JSON, whichever of its two
// shapes the installed nix prints (an object keyed by path, or one under "derivations").
func drvSystemFromShow(out []byte) string {
	var keyed map[string]json.RawMessage
	if json.Unmarshal(out, &keyed) != nil {
		return "system unknown"
	}
	if inner, ok := keyed["derivations"]; ok {
		keyed = nil
		if json.Unmarshal(inner, &keyed) != nil {
			return "system unknown"
		}
	}
	for _, raw := range keyed {
		var d struct {
			System string `json:"system"`
		}
		if json.Unmarshal(raw, &d) == nil && d.System != "" {
			return d.System
		}
	}
	return "system unknown"
}

// nixStoreName is a store path's name without its hash: /nix/store/<hash>-<name> → <name>.
func nixStoreName(p string) string {
	base := filepath.Base(p)
	if _, name, ok := strings.Cut(base, "-"); ok {
		return name
	}
	return base
}

// nixStoreHash is a store path's hash part, which is what a binary cache keys its narinfo on.
func nixStoreHash(p string) string {
	hash, _, _ := strings.Cut(filepath.Base(p), "-")
	return hash
}

// cachixNarinfoHits counts the paths cache holds, asking `<cache>/<hash>.narinfo` for each,
// sixteen at a time. A request that fails counts as a miss: this is a record, not a gate.
func cachixNarinfoHits(cache string, paths []string) int {
	client := &http.Client{Timeout: 15 * time.Second}
	var (
		mu   sync.Mutex
		hits int
		wg   sync.WaitGroup
	)
	sem := make(chan struct{}, 16)
	for _, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			resp, err := client.Head(cache + "/" + nixStoreHash(p) + ".narinfo")
			if err != nil {
				return
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				mu.Lock()
				hits++
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	return hits
}

// cachixVerdict is the one line recorded per variant.
func cachixVerdict(label string, plan *nixDryRunPlan, hits int, cache string) string {
	switch {
	case plan.ignored != "":
		return fmt.Sprintf("CACHIX %s: VOID — nix ignored the project cache, so this plan never "+
			"asked it (%s); the runner's nix user must be trusted for the flake's substituter "+
			"to count", label, plan.ignored)
	case len(plan.build) == 0 && len(plan.fetch) == 0:
		return fmt.Sprintf("CACHIX %s: NOTHING TO DO — the closure is already in this Mac's store "+
			"(an earlier test realized it), so this run cannot say whether it was fetched or built", label)
	case len(plan.build) == 0:
		return fmt.Sprintf("CACHIX %s: SUBSTITUTES — nothing would be built; %d of %d paths would "+
			"be fetched from %s", label, hits, len(plan.fetch), cache)
	default:
		return fmt.Sprintf("CACHIX %s: WOULD BUILD %d derivation(s) no substituter serves; %d of %d "+
			"fetched paths come from %s", label, len(plan.build), hits, len(plan.fetch), cache)
	}
}

// TestMacImageSubstitutesFromCachixParsesNixsPlan is the -short check of the parts that need no
// Mac: the plan parser on nix's own shapes (plural, singular, a failed eval), the substituter
// read from the real flake.nix, a store path's hash and name, both `nix derivation show`
// shapes, and the verdict for each kind of plan.
func TestMacImageSubstitutesFromCachixParsesNixsPlan(t *testing.T) {
	stderr := "warning: ignoring the client-specified setting 'trusted-public-keys', because it is a restricted setting\n" +
		"these 2 derivations will be built:\n" +
		"  /nix/store/aaa-nix-ld-2.0.6.drv\n  /nix/store/bbb-yolo-jail-root.drv\n" +
		"these 3 paths will be fetched (12.3 MiB download, 40.1 MiB unpacked):\n" +
		"  /nix/store/ccc-glibc\n  /nix/store/ddd-bash\n  /nix/store/eee-zbar\n" +
		"warning: something else\n"
	plan, ok := parseNixDryRun(stderr, false)
	if !ok || len(plan.build) != 2 || len(plan.fetch) != 3 {
		t.Fatalf("parseNixDryRun = %+v, %v; want 2 builds and 3 fetches", plan, ok)
	}
	if plan.fetch[2] != "/nix/store/eee-zbar" {
		t.Errorf("the fetch list ran past its block or lost an entry: %v", plan.fetch)
	}
	single, ok := parseNixDryRun("this path will be fetched (1 KiB download):\n  /nix/store/fff-x\n", false)
	if !ok || len(single.fetch) != 1 || len(single.build) != 0 {
		t.Errorf("the singular form did not parse: %+v", single)
	}
	if plan.ignored != "" {
		t.Errorf("a plan with no ignored-substituter warning recorded one: %q", plan.ignored)
	}
	// nix's own words, measured 2026-10-01 in a jail whose nix user is not trusted.
	untrusted, ok := parseNixDryRun("warning: ignoring untrusted substituter 'https://yolo-jail.cachix.org', "+
		"you are not a trusted user.\nthese 8 derivations will be built:\n  /nix/store/p-bin-path-links.drv\n", false)
	if !ok || untrusted.ignored == "" || len(untrusted.build) != 1 {
		t.Errorf("an untrusted user's plan did not record the ignored cache: %+v", untrusted)
	}
	if got := cachixVerdict("stock", untrusted, 0, "https://yolo-jail.cachix.org"); !strings.Contains(got, "VOID") {
		t.Errorf("a plan that never asked the cache is not VOID: %s", got)
	}
	if _, ok := parseNixDryRun("error: flake 'path:.' does not provide attribute", true); ok {
		t.Error("a failed dry run with no plan parsed as a plan with nothing to do")
	}
	if empty, ok := parseNixDryRun("", false); !ok || len(empty.build)+len(empty.fetch) != 0 {
		t.Errorf("a successful dry run with nothing to do did not parse as an empty plan: %+v", empty)
	}

	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := flakeSubstituter(filepath.Join(root, "flake.nix"))
	if err != nil || !strings.HasPrefix(cache, "https://") {
		t.Errorf("flakeSubstituter(flake.nix) = %q, %v; want the project cache's https URL", cache, err)
	}
	if h, n := nixStoreHash("/nix/store/w77lrjfj5dkri0q0g1gln3z5jcfb9qjm-nix-ld-2.0.6.drv"),
		nixStoreName("/nix/store/w77lrjfj5dkri0q0g1gln3z5jcfb9qjm-nix-ld-2.0.6.drv"); h != "w77lrjfj5dkri0q0g1gln3z5jcfb9qjm" || n != "nix-ld-2.0.6.drv" {
		t.Errorf("store path split = %q, %q", h, n)
	}
	for _, show := range []string{
		`{"/nix/store/aaa-x.drv": {"system": "x86_64-linux"}}`,
		`{"version": 4, "derivations": {"aaa-x.drv": {"system": "x86_64-linux"}}}`,
	} {
		if got := drvSystemFromShow([]byte(show)); got != "x86_64-linux" {
			t.Errorf("drvSystemFromShow(%s) = %q, want x86_64-linux", show, got)
		}
	}
	for want, p := range map[string]*nixDryRunPlan{
		"NOTHING TO DO": {},
		"SUBSTITUTES":   {fetch: []string{"/nix/store/a-x"}},
		"WOULD BUILD 1": {build: []string{"/nix/store/b-y.drv"}},
	} {
		if got := cachixVerdict("stock", p, 0, cache); !strings.Contains(got, want) {
			t.Errorf("cachixVerdict(%+v) = %q, want it to say %q", p, got, want)
		}
	}
}
