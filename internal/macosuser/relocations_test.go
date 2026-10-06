package macosuser

// relocations_test.go pins the macos-user delivery of `cache_relocations`
// (docs/plans/cache-relocation.md, the macos-user section; ctxlinks.go's cache_relocations
// section): the siting, the profile's two rules in their positions, the link named to the
// bootstrap, the DAC preflight and the write under the session profile, PlanInvariants' rules,
// the dry run's lines, the launch's disclosure, and where the launch runs each probe. Every
// assertion is on what the plan builder or the orchestrator emits, so deleting a call site in
// either fails a test here. Whether the kernel enforces it is
// integration/macosuserrelocations_test.go's and integration/macosuserseatbelt_test.go's.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const relocWS = "/Users/Shared/yolo/proj"

var (
	sharedReloc = CacheRelocation{Subdir: "huggingface", Target: "/Users/Shared/caches/hf", Named: "/Users/Shared/caches/hf"}
	volumeReloc = CacheRelocation{Subdir: "ms-playwright", Target: "/Volumes/Data/yolo/playwright", Created: true}
)

func planWithRelocations(t *testing.T, relocs ...CacheRelocation) RunPlan {
	t.Helper()
	return BuildRunPlan(relocWS, jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostStaged, HomeOverlay{},
		HostContext{Relocations: relocs}, jsonx.NewOrderedMap(), nil, nil)
}

// EVERY SITING RULE, one target at a time, each refusal naming why (SiteCacheRelocations' list).
func TestSiteCacheRelocationsRefusesEachUndeliverableTarget(t *testing.T) {
	ro := ContextLink{Dest: "/ctx/lib", Source: "/Users/Shared/ci/lib", Dir: true}
	for _, tc := range []struct {
		name  string
		reloc CacheRelocation
		why   string
	}{
		{"a key that is a path", CacheRelocation{Subdir: "a/b", Target: "/opt/c"}, "not one path segment"},
		{"a key that is ..", CacheRelocation{Subdir: "..", Target: "/opt/c"}, "not one path segment"},
		{"an unresolved spelling", CacheRelocation{Subdir: "hf", Target: "/opt/a/../hf"}, "not a resolved absolute path"},
		{"a relative target", CacheRelocation{Subdir: "hf", Target: "opt/hf"}, "not a resolved absolute path"},
		{"the root", CacheRelocation{Subdir: "hf", Target: "/"}, "contains /Users, and with it every home"},
		{"the users root", CacheRelocation{Subdir: "hf", Target: "/Users"}, "contains /Users"},
		{"a firmlink spelling", CacheRelocation{Subdir: "hf", Target: "/System/Volumes/Data/Users/Shared/hf"}, "spelled through another name for /Users"},
		{"a case spelling", CacheRelocation{Subdir: "hf", Target: "/users/Shared/hf"}, "spelled through another name for /Users"},
		{"a Data-volume spelling", CacheRelocation{Subdir: "hf", Target: "/System/Volumes/Data/opt/hf"}, "spell it as /opt/hf"},
		{"the sandbox home", CacheRelocation{Subdir: "hf", Target: "/Users/_yolojail/big"}, "the sandbox account's own home"},
		{"a real home", CacheRelocation{Subdir: "hf", Target: "/Users/matt/hf-cache"}, "inside the home folder /Users/matt"},
		{"a privacy-guarded dir", CacheRelocation{Subdir: "hf", Target: "/Users/matt/Documents/hf"}, "/Users/matt/Documents, which macOS's privacy controls guard"},
		{"the state dir", CacheRelocation{Subdir: "hf", Target: "/private/var/yolo-jail/hf"}, "yolo's state directory"},
		{"an ancestor of the state dir", CacheRelocation{Subdir: "hf", Target: "/var"}, "contains yolo's state directory /var/yolo-jail"},
		{"every volume", CacheRelocation{Subdir: "hf", Target: "/Volumes"}, "is or contains /Volumes"},
		{"inside the workspace", CacheRelocation{Subdir: "hf", Target: relocWS + "/.cache-hf"}, "is inside the workspace"},
		{"containing the workspace", CacheRelocation{Subdir: "hf", Target: "/Users/Shared/yolo"}, "contains the workspace"},
		{"a case spelling of the workspace", CacheRelocation{Subdir: "hf", Target: "/Users/Shared/yolo/PROJ/hf"}, "is inside the workspace"},
		{"a context source", CacheRelocation{Subdir: "hf", Target: "/Users/Shared/ci/lib/hf"}, "the read-only source /Users/Shared/ci/lib"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SiteCacheRelocations(DarwinContextSiting(), relocWS, []CacheRelocation{tc.reloc}, []ContextLink{ro})
			if len(got) != 1 {
				t.Fatalf("SiteCacheRelocations(%+v) refused %d, want exactly 1: %+v", tc.reloc, len(got), got)
			}
			if !strings.Contains(got[0].Reason, tc.why) {
				t.Errorf("the refusal says %q, want it to say %q", got[0].Reason, tc.why)
			}
		})
	}
}

// WHAT IT ADMITS, and the two places a context source could not be: a folder under /Users/Shared
// that is NOT under the shared root (a cache is the sandbox's own bytes), and a volume under
// /Volumes (CX-D5 narrowed for this key; the launch-time probe stands in for the measurement).
func TestSiteCacheRelocationsAdmitsNeutralGroundAndAVolume(t *testing.T) {
	for _, target := range []string{
		"/Users/Shared/caches/hf",
		"/Users/Shared/yolo/caches/hf", // under the shared root, beside the workspace
		"/Volumes/Data/yolo/hf",
		"/Volumes/Data", // a whole volume
		"/opt/caches/hf",
		"/private/var/folders/xy/abc/T/hf", // already in the writable set
		"/private/tmp/hf",
	} {
		r := CacheRelocation{Subdir: "hf", Target: target}
		if got := SiteCacheRelocations(DarwinContextSiting(), relocWS, []CacheRelocation{r}, nil); len(got) > 0 {
			t.Errorf("%s was refused: %s", target, got[0].Reason)
		}
	}
}

// A PLAN DELIVERING RELOCATIONS HOLDS EVERY INVARIANT, and carries each half the launch needs.
func TestPlanWithCacheRelocationsHoldsEveryInvariant(t *testing.T) {
	plan := planWithRelocations(t, sharedReloc, volumeReloc)
	if probs := PlanInvariants(plan); len(probs) > 0 {
		t.Fatalf("a plan delivering two sited relocations violates its invariants:\n%s", strings.Join(probs, "\n"))
	}
	if len(plan.CacheRelocations) != 2 || len(plan.CacheRelocationPreflight) != 6 || len(plan.CacheRelocationProbes) != 2 {
		t.Fatalf("plan carries %d relocations, %d preflight probes and %d write probes; want 2, 6, 2",
			len(plan.CacheRelocations), len(plan.CacheRelocationPreflight), len(plan.CacheRelocationProbes))
	}
	wire, ok := argvEnvValue(plan.BootstrapArgv, entrypoint.DarwinCacheRelocationsEnv)
	if !ok {
		t.Fatalf("the bootstrap is not told the relocations (%s):\n%v", entrypoint.DarwinCacheRelocationsEnv, plan.BootstrapArgv)
	}
	got, err := entrypoint.ParseDarwinCacheRelocations(wire)
	if err != nil || got["huggingface"] != sharedReloc.Target || got["ms-playwright"] != volumeReloc.Target || len(got) != 2 {
		t.Errorf("the bootstrap's relocation wire is %q (%v), want each subdir to its resolved target", wire, err)
	}
	// The write probe runs as the sandbox account, under THIS session's profile, on a file the
	// session names, handed to the shell as an argument.
	p := plan.CacheRelocationProbes[0].Argv
	if p[0] != "sudo" || p[1] != "--user="+SandboxUser || !containsArgPair(p, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) ||
		p[len(p)-1] != CacheRelocationProbeFile(sharedReloc.Target, plan.SessionID) {
		t.Errorf("the write probe is not a confined write by the sandbox account on the session's file: %v", p)
	}
}

// NO RELOCATION, NO CHANGE: the profile is byte-identical to one built without the key, the
// bootstrap is told nothing, and nothing is probed.
func TestAPlanWithNoRelocationIsUnchanged(t *testing.T) {
	plan := planWithRelocations(t)
	if want := SeatbeltProfile(relocWS, SandboxHome(), nil, HomeReadonly{}); plan.Seatbelt != want {
		t.Errorf("a launch with no relocation got a different profile")
	}
	if _, ok := argvEnvValue(plan.BootstrapArgv, entrypoint.DarwinCacheRelocationsEnv); ok {
		t.Errorf("a launch with no relocation names %s to the bootstrap", entrypoint.DarwinCacheRelocationsEnv)
	}
	if len(plan.CacheRelocationPreflight)+len(plan.CacheRelocationProbes) > 0 {
		t.Errorf("a launch with no relocation probes something")
	}
	if strings.Contains(plan.Seatbelt, "cache-relocation") {
		t.Errorf("a launch with no relocation carries a relocation rule:\n%s", plan.Seatbelt)
	}
}

// THE PROFILE OPENS EACH TARGET WHERE IT MUST: the read allow after the /Volumes and /Users read
// denies it re-opens and before the keychain denies, which still win; the write allow before
// every write deny; and the directory entries on the way to a /Volumes or /Users/Shared target
// as literals, never as a subpath that would re-open their siblings.
func TestTheProfileOpensEachRelocationTargetAfterTheDeniesItReopens(t *testing.T) {
	p := planWithRelocations(t, sharedReloc, volumeReloc).Seatbelt
	at := func(id string) int {
		i := strings.Index(p, ";; #seatbelt-test-id:"+id+"#")
		if i < 0 {
			t.Fatalf("the profile carries no %s rule:\n%s", id, p)
		}
		return i
	}
	read, write := at("cache-relocation-read-allow"), at("cache-relocation-write-allow")
	for _, deny := range []string{"volumes-read-deny", "users-read-deny"} {
		if at(deny) > read {
			t.Errorf("the relocations' read allow comes before %s, so it re-opens nothing", deny)
		}
	}
	for _, later := range []string{"library-keychains-deny", "system-keychains-deny"} {
		if at(later) < read {
			t.Errorf("%s comes before the relocations' read allow, which could then re-open it", later)
		}
	}
	if at("workspace-write-allow") > write {
		t.Errorf("the relocations' write allow comes before the writable-set allow")
	}
	readBlock := profileBlock(p, "cache-relocation-read-allow")
	writeBlock := profileBlock(p, "cache-relocation-write-allow")
	for _, target := range []string{sharedReloc.Target, volumeReloc.Target} {
		clause := "(subpath " + sbplStr(target) + ")"
		if !strings.Contains(readBlock, clause) || !strings.Contains(writeBlock, clause) {
			t.Errorf("%s is not opened read and write:\nread:\n%s\nwrite:\n%s", target, readBlock, writeBlock)
		}
	}
	for _, lit := range []string{"/Volumes", "/Volumes/Data", "/Volumes/Data/yolo", "/Users/Shared/caches"} {
		if !strings.Contains(readBlock, "(literal "+sbplStr(lit)+")") {
			t.Errorf("the directory entry %s on the way to a target is not granted:\n%s", lit, readBlock)
		}
	}
	for _, sub := range []string{"/Volumes", "/Volumes/Data", "/Users/Shared"} {
		if strings.Contains(readBlock, "(subpath "+sbplStr(sub)+")") {
			t.Errorf("the relocations' read allow re-opens all of %s:\n%s", sub, readBlock)
		}
	}
}

// EACH HALF PlanInvariants CHECKS fails the plan that lacks it.
func TestPlanInvariantsFailWithoutEachRelocationHalf(t *testing.T) {
	good := planWithRelocations(t, sharedReloc)
	clause := "(subpath " + sbplStr(sharedReloc.Target) + ")"
	// dropClause replaces the target's clause in the block id marks, and only there.
	dropClause := func(profile, id string) string {
		marker := ";; #seatbelt-test-id:" + id + "#\n"
		i := strings.Index(profile, marker)
		if i < 0 {
			t.Fatalf("no %s block to cut", id)
		}
		head, rest := profile[:i+len(marker)], profile[i+len(marker):]
		return head + strings.Replace(rest, clause, `(literal "/elsewhere")`, 1)
	}
	for name, tc := range map[string]struct {
		mutate func(*RunPlan)
		want   string
	}{
		"no read allow": {func(p *RunPlan) {
			p.Seatbelt = dropClause(p.Seatbelt, "cache-relocation-read-allow")
		}, "has no read allow"},
		"no write allow": {func(p *RunPlan) {
			p.Seatbelt = dropClause(p.Seatbelt, "cache-relocation-write-allow")
		}, "has no write allow"},
		"the read allow before the denies": {func(p *RunPlan) {
			block := ";; #seatbelt-test-id:cache-relocation-read-allow#\n"
			p.Seatbelt = strings.Replace(p.Seatbelt, block, "", 1)
			p.Seatbelt = block + p.Seatbelt
		}, "comes before the volumes-read-deny"},
		"no preflight":   {func(p *RunPlan) { p.CacheRelocationPreflight = nil }, "not checked by the DAC preflight"},
		"no write probe": {func(p *RunPlan) { p.CacheRelocationProbes = nil }, "not probed by a write under the session's"},
		"no bootstrap link": {func(p *RunPlan) {
			var argv []string
			for _, w := range p.BootstrapArgv {
				if !strings.HasPrefix(w, entrypoint.DarwinCacheRelocationsEnv+"=") {
					argv = append(argv, w)
				}
			}
			p.BootstrapArgv = argv
		}, "is not named to the bootstrap"},
		"an unsited target": {func(p *RunPlan) {
			p.CacheRelocations = []CacheRelocation{{Subdir: "hf", Target: "/Users/matt/hf"}}
		}, "cannot be delivered on this backend"},
	} {
		t.Run(name, func(t *testing.T) {
			p := good
			p.BootstrapArgv = append([]string(nil), good.BootstrapArgv...)
			tc.mutate(&p)
			probs := strings.Join(PlanInvariants(p), "\n")
			if !strings.Contains(probs, tc.want) {
				t.Errorf("PlanInvariants did not say %q:\n%s", tc.want, probs)
			}
		})
	}
}

// THE DRY RUN NAMES EACH LINK AND TARGET, and the probes in the privileged commands.
func TestTheDryRunPlanNamesEachRelocation(t *testing.T) {
	plan := planWithRelocations(t, sharedReloc)
	var buf bytes.Buffer
	PrintPlan(&buf, plan, nil)
	out := buf.String()
	for _, want := range []string{
		"cache:       " + SandboxHome() + "/.cache/huggingface → " + sharedReloc.Target,
		"sudo --user=" + SandboxUser + " " + testBin + " -w " + sharedReloc.Target,
		shquote.JoinDisplay(plan.CacheRelocationProbes[0].Argv),
		"#seatbelt-test-id:cache-relocation-write-allow#",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not show %q:\n%s", want, out)
		}
	}
}

// THE LAUNCH DISCLOSES EACH RELOCATION, from the caller's record and never from the merged
// config: a `cache_relocations` key in opts.Config with no record (what a workspace-scope entry
// would be) says nothing, and the retired "NOT implemented" warning is gone either way.
func TestTheLaunchDisclosesRelocationsFromTheRecordAlone(t *testing.T) {
	render := func(cfg string, relocs ...CacheRelocation) string {
		o := newOpts(relocWS)
		o.DryRun = true
		o.Config = jsonx.NewOrderedMap()
		if cfg != "" {
			m := jsonx.NewOrderedMap()
			m.Set("huggingface", cfg)
			o.Config.Set("cache_relocations", m)
		}
		o.HostCtx = HostContext{Relocations: relocs}
		var buf bytes.Buffer
		d := mockDeps(nil)
		d.Out = &buf
		if rc := RunMacosUser(d, o); rc != 0 {
			t.Fatalf("dry run rc = %d:\n%s", rc, buf.String())
		}
		return buf.String()
	}
	out := render("/Users/Shared/caches/hf", sharedReloc)
	for _, want := range []string{"Cache relocation: ~/.cache/huggingface → " + sharedReloc.Target,
		"Only ~/.cache moves", "~/Library/Caches"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch does not disclose %q:\n%s", want, out)
		}
	}
	for _, out := range []string{out, render("/Users/Shared/caches/hf")} {
		if strings.Contains(out, "NOT implemented") {
			t.Errorf("the retired warning is still printed:\n%s", out)
		}
	}
	if out := render("/Users/Shared/caches/hf"); strings.Contains(out, "Cache relocation:") {
		t.Errorf("a relocation the run pipeline did not deliver (only in the merged config) is "+
			"disclosed as if it were:\n%s", out)
	}
}

// relocLaunch runs one live launch carrying sharedReloc, with failing reporting whether a probe
// argv fails, and returns the record, the output and the status.
func relocLaunch(t *testing.T, failing func(argv []string) bool) ([]string, string, int) {
	t.Helper()
	var rec []string
	d := mockDeps(&rec)
	run := d.Run
	d.Run = func(argv []string) int {
		got := run(argv)
		if failing != nil && failing(argv) {
			return 1
		}
		return got
	}
	materialize := d.MaterializeDarwin
	d.MaterializeDarwin = func(root string, pkgs []any) (*Darwin, bool, error) {
		rec = append(rec, "materialize:"+root)
		return materialize(root, pkgs)
	}
	o := newOpts(relocWS)
	o.HostCtx = HostContext{Relocations: []CacheRelocation{sharedReloc}}
	var buf bytes.Buffer
	d.Out = &buf
	status := RunMacosUser(d, o)
	return rec, buf.String(), status
}

// THE LAUNCH ASKS BOTH: the DAC preflight before the nix build, and the write under the session
// profile once the profile is installed and before anything is staged. Deleting either step from
// RunMacosUser fails this.
func TestTheLaunchProbesEachRelocationTargetBeforeTheAgent(t *testing.T) {
	rec, out, rc := relocLaunch(t, nil)
	if rc != 42 {
		t.Fatalf("rc = %d, want the agent's\n%s", rc, out)
	}
	idx := func(match func(string) bool) int { return probeRecIndex(rec, match) }
	preflight := idx(func(r string) bool { return r == "run:sudo --user="+SandboxUser+" "+testBin+" -w "+sharedReloc.Target })
	install := idx(func(r string) bool { return strings.HasPrefix(r, "install:") && strings.HasSuffix(r, ".sb") })
	write := idx(func(r string) bool {
		return strings.HasPrefix(r, "run:sudo --user="+SandboxUser+" /usr/bin/env -i") &&
			strings.Contains(r, "/usr/bin/sandbox-exec -f ") &&
			strings.Contains(r, sharedReloc.Target+"/.yolo-relocation-probe-")
	})
	stage := idx(func(r string) bool { return strings.HasPrefix(r, "run:sudo "+mkdirBin+" -p ") })
	agent := idx(func(r string) bool { return strings.HasPrefix(r, "proxy:") })
	materialize := idx(func(r string) bool { return strings.HasPrefix(r, "materialize:") })
	if preflight < 0 || materialize < 0 || install < 0 || write < 0 || stage < 0 || agent < 0 ||
		!(preflight < materialize && materialize < install && install < write && write < stage && stage < agent) {
		t.Fatalf("want preflight < nix build < profile install < write probe < stage < agent; got "+
			"%d %d %d %d %d %d:\n%s", preflight, materialize, install, write, stage, agent, strings.Join(rec, "\n"))
	}
}

// A TARGET THE SANDBOX CANNOT USE REFUSES THE LAUNCH, naming the grant, another folder and
// podman, and the agent never starts — whichever probe found it.
func TestARelocationTheSandboxCannotUseRefusesTheLaunch(t *testing.T) {
	for name, failing := range map[string]func([]string) bool{
		"the DAC preflight": func(argv []string) bool {
			return len(argv) == 5 && argv[2] == testBin && argv[3] == "-w"
		},
		"the write under the profile": func(argv []string) bool {
			return containsArg(argv, "/usr/bin/sandbox-exec") && strings.Contains(argv[len(argv)-1], ".yolo-relocation-probe-")
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec, out, rc := relocLaunch(t, failing)
			if rc != 1 {
				t.Fatalf("rc = %d, want 1\n%s", rc, out)
			}
			if probeRecIndex(rec, func(r string) bool { return strings.HasPrefix(r, "proxy:") }) >= 0 {
				t.Errorf("the agent ran after a relocation probe failed")
			}
			for _, want := range []string{"cannot use every cache_relocations target",
				"yolo macos-fix-permissions " + sharedReloc.Target, "`runtime: \"podman\"`",
				"~/.cache/huggingface → " + sharedReloc.Target} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, out)
				}
			}
		})
	}
}

// THE GRANT a created target gets is the shared root's own inheriting pair, as the invoking user.
func TestCacheRelocationACECommandsAreTheSharedRootsPair(t *testing.T) {
	aces := WorkspaceACLAces(SandboxGroup)
	got := CacheRelocationACECommands("/Volumes/Data/hf")
	want := [][]string{{chmodBin, "+a", aces["dir"], "/Volumes/Data/hf"}, {chmodBin, "+a", aces["file_inherit"], "/Volumes/Data/hf"}}
	if len(got) != 2 || strings.Join(got[0], " ") != strings.Join(want[0], " ") || strings.Join(got[1], " ") != strings.Join(want[1], " ") {
		t.Errorf("CacheRelocationACECommands = %v, want %v", got, want)
	}
	for _, argv := range got {
		if argv[0] == "sudo" {
			t.Errorf("the owner's grant runs under sudo: %v", argv)
		}
	}
}
