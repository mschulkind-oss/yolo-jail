package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
// release, and CI on Linux has been seen substituting it back. The 2026-10-03 Intel Mac nightly
// measured all Linux image paths fetched and no Linux derivation built; the Apple-silicon human
// Final test remains unmeasured. The archive-delivery job's realize phase (macArchiveRealize)
// records fetched/built COUNTS for the images it builds; this test asks the sharper question for
// the image variants the shards' own launches ask for, and names the cache each fetched path
// would come from.
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
// # A MEASUREMENT: the Linux-builder verdict is separate from local Mac helpers
//
// One `CACHIX <variant> …` line per variant in the log and the step summary. The report keeps
// host-system helpers, Linux-image derivations, and unknown systems separate; only Linux-image
// derivations count as a Linux-builder gap. A dry run that exits non-zero printing no plan, or a
// flake.nix with no substituter to ask, still fails the instrument. Runs on any darwin host with
// nix; the nightly's shards are where it is scheduled (it falls into one shard of the computed
// partition), and it builds nothing.
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
	variants := []cachixVariantSpec{
		{label: "stock", pkgs: ""},
		{label: "zbar", pkgs: `["zbar"]`},
		{label: "libsodium.dev", pkgs: `["libsodium.dev"]`},
	}
	_, err = cachixMeasureAndReport(variants, goruntime.GOOS, goruntime.GOARCH, cache,
		func(pkgs string) (*nixDryRunPlan, string) { return cachixDryRun(t, flake, pkgs) },
		nixDrvSystem, cachixNarinfoHits,
		func(lines []string) { stepSummary(t, lines...) })
	if err != nil {
		t.Fatal(err)
	}
}

// nixDryRunPlan is what `nix build --dry-run` says it would do.
type nixDryRunPlan struct {
	build []string // .drv paths it would build
	fetch []string // store paths it would substitute
	// ignored is nix's own warning when it dropped a flake-declared substituter because this
	// user is not trusted, or "". With it the plan never asked the project cache at all.
	ignored string
}

// parseNixDryRun reads nix's plan out of its stderr: the store paths listed under each of the two
// headers, which are nixWillBuildRe's and nixWillFetchRe's (macarchivedelivery_test.go, the
// package's one spelling of nix's plan summary). ok is false when neither header appears and nix
// failed, which is a dry run that planned nothing rather than one with nothing to do.
func parseNixDryRun(stderr string, failed bool) (*nixDryRunPlan, bool) {
	plan := &nixDryRunPlan{}
	var into *[]string
	seen := false
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(trimmed, "ignoring untrusted substituter"):
			plan.ignored, into = trimmed, nil
		case nixWillBuildRe.MatchString(trimmed):
			into, seen = &plan.build, true
		case nixWillFetchRe.MatchString(trimmed):
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

// cachixVariantMeasurement is one variant's report, with derivations classified from nix's
// reported `system`. Cache hits intentionally remain an aggregate over fetched paths: the dry-run
// path list alone does not carry a trustworthy platform association.
type cachixVariantMeasurement struct {
	label         string
	hostBuilds    []string
	linuxBuilds   []string
	unknownBuilds []string
	cacheHits     int
	row           string
	verdict       string
}

type cachixVariantSpec struct {
	label string
	pkgs  string
}

type cachixMeasurementReport struct {
	variants []cachixVariantMeasurement
	summary  []string
}

// cachixNixDarwinSystem translates the host runtime's GOARCH vocabulary to Nix's system
// vocabulary. Unknown architectures remain unmatched instead of being guessed.
func cachixNixDarwinSystem(goos, goarch string) string {
	if goos != "darwin" {
		return ""
	}
	var nixArch string
	switch goarch {
	case "arm64":
		nixArch = "aarch64"
	case "amd64":
		nixArch = "x86_64"
	default:
		return ""
	}
	return nixArch + "-darwin"
}

// cachixMeasureAndReport is the complete production per-variant orchestration used by the Mac
// test. The dry-run, derivation-system, cache-hit, and summary readers are injectable so offline
// fixtures exercise the caller's classifications and delivered report without nix or HTTP.
func cachixMeasureAndReport(variants []cachixVariantSpec, hostGOOS, hostGOARCH, cache string,
	dryRun func(string) (*nixDryRunPlan, string), systemFor func(string) string,
	narinfoHits func(string, []string) int, deliverSummary func([]string)) (*cachixMeasurementReport, error) {
	report := &cachixMeasurementReport{}
	rows := []string{
		"### Does this Mac substitute the jail image from " + cache + "? (measurement only)",
		"",
		"| variant | host-system builds (local) | Linux-image builds | unknown-system builds | would fetch | from " + cache + " |",
		"| :--- | :--- | :--- | :--- | :--- | :--- |",
	}
	verdicts := []string{}
	hostSystem := cachixNixDarwinSystem(hostGOOS, hostGOARCH)
	for _, variant := range variants {
		plan, output := dryRun(variant.pkgs)
		if plan == nil {
			return nil, fmt.Errorf("CACHIX %s: `nix build --dry-run %s` printed no plan, so NOTHING WAS MEASURED "+
				"for it:\n%s", variant.label, image.ImageAttrDefault, lastLines(output, 30))
		}
		measured := cachixMeasurePlan(variant.label, plan, hostSystem, cache, systemFor, narinfoHits)
		report.variants = append(report.variants, *measured)
		rows = append(rows, measured.row)
		verdicts = append(verdicts, "- "+measured.verdict)
	}
	report.summary = append(append(rows, ""), append(verdicts,
		"Record it in docs/plans/handoff-cachix-cache.md (its status line and the Final test).", "")...)
	deliverSummary(report.summary)
	return report, nil
}

func cachixMeasurePlan(label string, plan *nixDryRunPlan, hostSystem, cache string,
	systemFor func(string) string, narinfoHits func(string, []string) int) *cachixVariantMeasurement {
	measured := &cachixVariantMeasurement{label: label}
	for _, drv := range plan.build {
		system := systemFor(drv)
		description := nixStoreName(drv) + " (" + system + ")"
		switch {
		case system != "" && system == hostSystem:
			measured.hostBuilds = append(measured.hostBuilds, description)
		case strings.HasSuffix(system, "-linux"):
			measured.linuxBuilds = append(measured.linuxBuilds, description)
		default:
			measured.unknownBuilds = append(measured.unknownBuilds, description)
		}
	}
	for _, group := range []*[]string{&measured.hostBuilds, &measured.linuxBuilds, &measured.unknownBuilds} {
		sort.Strings(*group)
	}
	measured.cacheHits = narinfoHits(cache, plan.fetch)
	measured.row = fmt.Sprintf("| %s | %s | %s | %s | %d | %d |",
		label, cachixFormatBuilds(measured.hostBuilds), cachixFormatBuilds(measured.linuxBuilds),
		cachixFormatBuilds(measured.unknownBuilds), len(plan.fetch), measured.cacheHits)
	measured.verdict = cachixMeasurementVerdict(label, plan, *measured, cache)
	return measured
}

func cachixFormatBuilds(builds []string) string {
	if len(builds) == 0 {
		return "none"
	}
	return fmt.Sprintf("%d: %s", len(builds), strings.Join(builds, ", "))
}

func cachixMeasurementVerdict(label string, plan *nixDryRunPlan, measured cachixVariantMeasurement, cache string) string {
	switch {
	case plan.ignored != "":
		return fmt.Sprintf("CACHIX %s: VOID — nix ignored the project cache, so this plan never "+
			"asked it (%s); the runner's nix user must be trusted for the flake's substituter "+
			"to count", label, plan.ignored)
	case len(plan.build) == 0 && len(plan.fetch) == 0:
		return fmt.Sprintf("CACHIX %s: NOTHING TO DO — the closure is already in this Mac's store "+
			"(an earlier test realized it), so this run cannot say whether it was fetched or built", label)
	case len(measured.unknownBuilds) != 0:
		known := ""
		if len(measured.linuxBuilds) != 0 {
			known = fmt.Sprintf("; %d known Linux-image derivation(s) would build", len(measured.linuxBuilds))
		}
		return fmt.Sprintf("CACHIX %s: UNKNOWN DERIVATION SYSTEM — cannot classify %d build(s) as "+
			"host-local or Linux-image derivations%s; %d of %d fetched paths come from %s",
			label, len(measured.unknownBuilds), known, measured.cacheHits, len(plan.fetch), cache)
	case len(measured.linuxBuilds) != 0:
		return fmt.Sprintf("CACHIX %s: WOULD BUILD %d LINUX IMAGE DERIVATION(S); %d host-system "+
			"helper(s) would build locally; %d of %d fetched paths come from %s", label,
			len(measured.linuxBuilds), len(measured.hostBuilds), measured.cacheHits, len(plan.fetch), cache)
	case len(measured.hostBuilds) != 0:
		return fmt.Sprintf("CACHIX %s: NO LINUX BUILDS — %d host-system helper(s) would build locally; "+
			"%d of %d fetched paths come from %s", label, len(measured.hostBuilds),
			measured.cacheHits, len(plan.fetch), cache)
	default:
		return fmt.Sprintf("CACHIX %s: SUBSTITUTES — nothing would be built; %d of %d paths would "+
			"be fetched from %s", label, measured.cacheHits, len(plan.fetch), cache)
	}
}

func TestCachixMeasurementClassifiesPlansThroughProductionOrchestration(t *testing.T) {
	cache := "https://cache.example"
	variants := []cachixVariantSpec{
		{label: "stock", pkgs: "stock-pkgs"},
		{label: "zbar", pkgs: "zbar-pkgs"},
		{label: "libsodium.dev", pkgs: "sodium-pkgs"},
	}
	for _, arch := range []struct{ goArch, nixArch string }{
		{"arm64", "aarch64"},
		{"amd64", "x86_64"},
	} {
		t.Run(arch.goArch, func(t *testing.T) {
			darwinHelper := "/nix/store/host-helper.drv"
			linuxImage := "/nix/store/linux-image.drv"
			mixedHelper := "/nix/store/mixed-helper.drv"
			mixedImage := "/nix/store/mixed-image.drv"
			plans := map[string]*nixDryRunPlan{
				"stock-pkgs":  {build: []string{darwinHelper}, fetch: []string{"/nix/store/fetch-a", "/nix/store/fetch-b"}},
				"zbar-pkgs":   {build: []string{linuxImage}, fetch: []string{"/nix/store/fetch-c"}},
				"sodium-pkgs": {build: []string{mixedHelper, mixedImage}, fetch: []string{"/nix/store/fetch-d", "/nix/store/fetch-e"}},
			}
			systems := map[string]string{
				darwinHelper: arch.nixArch + "-darwin",
				linuxImage:   "aarch64-linux",
				mixedHelper:  arch.nixArch + "-darwin",
				mixedImage:   "x86_64-linux",
			}
			var delivered [][]string
			askedCache := ""
			askedPathCount := 0
			report, err := cachixMeasureAndReport(variants, "darwin", arch.goArch, cache,
				func(pkgs string) (*nixDryRunPlan, string) { return plans[pkgs], "fixture output: " + pkgs },
				func(drv string) string { return systems[drv] },
				func(cache string, paths []string) int {
					askedCache = cache
					askedPathCount += len(paths)
					return len(paths)
				},
				func(lines []string) { delivered = append(delivered, append([]string(nil), lines...)) })
			if err != nil {
				t.Fatal(err)
			}
			if askedCache != cache || askedPathCount != 5 {
				t.Errorf("aggregate cache-hit reader queried %s and %d paths; want %s and 5 total fetched paths",
					askedCache, askedPathCount, cache)
			}
			if len(report.variants) != 3 || len(delivered) != 1 || !sameStrings(delivered[0], report.summary) {
				t.Fatalf("report variants=%d, summary deliveries=%v, want all three variants delivered once",
					len(report.variants), delivered)
			}
			want := []struct {
				label                      string
				host, linux, unknown, hits int
				verdict                    string
			}{
				{"stock", 1, 0, 0, 2, "NO LINUX BUILDS"},
				{"zbar", 0, 1, 0, 1, "WOULD BUILD 1 LINUX IMAGE DERIVATION"},
				{"libsodium.dev", 1, 1, 0, 2, "WOULD BUILD 1 LINUX IMAGE DERIVATION"},
			}
			for i, expected := range want {
				got := report.variants[i]
				if got.label != expected.label || len(got.hostBuilds) != expected.host ||
					len(got.linuxBuilds) != expected.linux || len(got.unknownBuilds) != expected.unknown ||
					got.cacheHits != expected.hits || !strings.Contains(got.verdict, expected.verdict) {
					t.Errorf("variant report[%d] = %+v, want label/classification/hits/verdict %s/%d/%d/%d/%d/%s",
						i, got, expected.label, expected.host, expected.linux, expected.unknown, expected.hits, expected.verdict)
				}
			}
			if len(report.summary) < 11 || report.summary[4] != report.variants[0].row ||
				report.summary[5] != report.variants[1].row || report.summary[6] != report.variants[2].row ||
				report.summary[8] != "- "+report.variants[0].verdict ||
				report.summary[9] != "- "+report.variants[1].verdict ||
				report.summary[10] != "- "+report.variants[2].verdict {
				t.Errorf("summary rows/verdicts do not contain structured reports: %v", report.summary)
			}
		})
	}
}

func TestCachixUnknownSystemsAndExistingPlanVerdictsThroughOrchestration(t *testing.T) {
	variants := []cachixVariantSpec{
		{label: "unknown", pkgs: "unknown"},
		{label: "untrusted", pkgs: "untrusted"},
		{label: "empty", pkgs: "empty"},
	}
	plans := map[string]*nixDryRunPlan{
		"unknown":   {build: []string{"/nix/store/no-system.drv"}, fetch: []string{"/nix/store/fetched"}},
		"untrusted": {build: []string{"/nix/store/host-helper.drv"}, ignored: "ignoring untrusted substituter"},
		"empty":     {},
	}
	var delivered [][]string
	report, err := cachixMeasureAndReport(variants, "darwin", "arm64", "https://cache.example",
		func(pkgs string) (*nixDryRunPlan, string) { return plans[pkgs], "fixture" },
		func(drv string) string {
			if drv == "/nix/store/host-helper.drv" {
				return "aarch64-darwin"
			}
			return ""
		}, func(_ string, paths []string) int { return len(paths) },
		func(lines []string) { delivered = append(delivered, append([]string(nil), lines...)) })
	if err != nil {
		t.Fatal(err)
	}
	unknown := report.variants[0]
	if len(unknown.unknownBuilds) != 1 || strings.Contains(unknown.verdict, "NO LINUX BUILDS") ||
		strings.Contains(unknown.verdict, "SUBSTITUTES") {
		t.Errorf("unknown system was silently green: %+v", unknown)
	}
	if !strings.Contains(report.variants[1].verdict, "VOID") || !strings.Contains(report.variants[2].verdict, "NOTHING TO DO") {
		t.Errorf("untrusted/empty plan verdicts changed: %+v", report.variants)
	}
	if len(delivered) != 1 || !sameStrings(delivered[0], report.summary) {
		t.Errorf("summary delivery = %v, want the report's complete lines", delivered)
	}
	calledSummary := false
	_, err = cachixMeasureAndReport([]cachixVariantSpec{{label: "missing", pkgs: "missing"}},
		"darwin", "arm64", "https://cache.example",
		func(string) (*nixDryRunPlan, string) { return nil, "failed dry-run fixture" },
		func(string) string { return "" }, func(string, []string) int { return 0 },
		func([]string) { calledSummary = true })
	if err == nil || !strings.Contains(err.Error(), "NOTHING WAS MEASURED") || calledSummary {
		t.Errorf("missing plan error=%v summary delivered=%v; want measurement failure and no summary", err, calledSummary)
	}
}

func TestCachixUnknownGoArchitectureIsNotGuessed(t *testing.T) {
	var reportSummary []string
	report, err := cachixMeasureAndReport([]cachixVariantSpec{
		{label: "known Nix system", pkgs: "fixture"},
		{label: "missing Nix system", pkgs: "missing-system"},
	},
		"darwin", "mystery", "https://cache.example",
		func(pkgs string) (*nixDryRunPlan, string) {
			if pkgs == "missing-system" {
				return &nixDryRunPlan{build: []string{"/nix/store/missing-system.drv"}}, "fixture"
			}
			return &nixDryRunPlan{build: []string{"/nix/store/arm-helper.drv"}}, "fixture"
		}, func(drv string) string {
			if drv == "/nix/store/missing-system.drv" {
				return ""
			}
			return "aarch64-darwin"
		}, func(string, []string) int { return 0 },
		func(lines []string) { reportSummary = append([]string(nil), lines...) })
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range report.variants {
		if len(got.hostBuilds) != 0 || len(got.linuxBuilds) != 0 || len(got.unknownBuilds) != 1 ||
			!strings.Contains(got.verdict, "UNKNOWN DERIVATION SYSTEM") ||
			strings.Contains(got.verdict, "NO LINUX BUILDS") {
			t.Errorf("unknown Go architecture or missing Nix system was guessed as host-local: %+v", got)
		}
	}
	if !sameStrings(reportSummary, report.summary) {
		t.Errorf("unknown architectures/systems were not included in summary: %v", reportSummary)
	}
}

func TestMacImageSubstitutesFromCachixPinsProductionOrchestrationCall(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "maccachixsubstitution_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var foundOrchestration, foundSummary, foundGOOS, foundGOARCH bool
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestMacImageSubstitutesFromCachix" || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "cachixMeasureAndReport" {
				foundOrchestration = true
				if len(call.Args) > 2 {
					foundGOOS = isRuntimeSelector(call.Args[1], "GOOS")
					foundGOARCH = isRuntimeSelector(call.Args[2], "GOARCH")
				}
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "stepSummary" {
				foundSummary = true
			}
			return true
		})
		return false
	})
	if !foundOrchestration || !foundSummary || !foundGOOS || !foundGOARCH {
		t.Errorf("Mac measurement wrapper orchestration pin: call=%v stepSummary=%v GOOS=%v GOARCH=%v; want actual orchestration, runtime platform inputs, and summary delivery",
			foundOrchestration, foundSummary, foundGOOS, foundGOARCH)
	}
}

func isRuntimeSelector(node ast.Node, name string) bool {
	selector, ok := node.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "goruntime"
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	if got := cachixMeasurePlan("stock", untrusted, "aarch64-darwin", "https://yolo-jail.cachix.org",
		func(string) string { return "aarch64-darwin" }, func(string, []string) int { return 0 }); !strings.Contains(got.verdict, "VOID") {
		t.Errorf("a plan that never asked the cache is not VOID: %+v", got)
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
		"NOTHING TO DO":                        {},
		"SUBSTITUTES":                          {fetch: []string{"/nix/store/a-x"}},
		"WOULD BUILD 1 LINUX IMAGE DERIVATION": {build: []string{"/nix/store/b-y.drv"}},
	} {
		got := cachixMeasurePlan("stock", p, "aarch64-darwin", cache,
			func(string) string { return "aarch64-linux" }, func(string, []string) int { return 0 })
		if !strings.Contains(got.verdict, want) {
			t.Errorf("measurement verdict for %+v = %+v, want it to say %q", p, got, want)
		}
	}
}
