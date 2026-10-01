package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The macos-user delivery of context mounts (docs/design/context-mounts.md §4 steps 4-5),
// through the PLAN: the siting, the profile's rules in their order, the links in the context
// dir, and the DAC preflight. Every assertion here is on what the plan builder emits for a
// HostContext carrying links, never on a direct call of the renderer, so deleting a call site
// in BuildRunPlan fails a test here. What only a Mac can say — whether the kernel enforces it —
// is integration/macosuserseatbelt_test.go's and integration/macosusercontextmounts_test.go's.

const ctxWorkspace = "/Users/Shared/yolo/proj"

// roLink and rwLink are one link of each mode, sited where v1 delivers them: a read-only source
// on neutral ground outside the shared root, a read-write one under it.
var (
	roLink = ContextLink{Dest: "/ctx/lib", Source: "/Users/Shared/ci/lib", Dir: true}
	rwLink = ContextLink{Dest: "/ctx/data", Source: "/Users/Shared/yolo/datasets", RW: true, Dir: true}
	optRO  = ContextLink{Dest: "/ctx/opt/tools", Source: "/opt/tools", Dir: true}
)

func planWithLinks(t *testing.T, links ...ContextLink) RunPlan {
	t.Helper()
	return BuildRunPlan(ctxWorkspace, jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", hostStaged, HomeOverlay{},
		HostContext{Links: links}, jsonx.NewOrderedMap(), nil, nil)
}

// THE SITING ADMITS WHAT v1 DELIVERS, and nothing in the plan objects to it.
func TestPlanWithDeliverableContextLinksHoldsEveryInvariant(t *testing.T) {
	plan := planWithLinks(t, roLink, rwLink, optRO)
	if probs := PlanInvariants(plan); len(probs) > 0 {
		t.Fatalf("a plan delivering three sited links violates its invariants:\n%s",
			strings.Join(probs, "\n"))
	}
	if refused := SiteContextLinks(DarwinContextSiting(), ctxWorkspace, plan.ContextLinks, nil); len(refused) > 0 {
		t.Errorf("the siting refuses sources v1 delivers: %+v", refused)
	}
}

// EVERY SITING RULE, one source at a time, each refusal naming why (SiteContextLinks' list).
func TestSiteContextLinksRefusesEachUndeliverableSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		link ContextLink
		why  string
	}{
		{"outside the context dir", ContextLink{Dest: "/opt/elsewhere", Source: "/opt/x"}, "is not under /ctx"},
		{"the context dir itself", ContextLink{Dest: "/ctx", Source: "/opt/x"}, "is not under /ctx"},
		{"an unresolved spelling", ContextLink{Dest: "/ctx/x", Source: "/opt/a/../x"}, "not a resolved absolute path"},
		{"a relative source", ContextLink{Dest: "/ctx/x", Source: "opt/x"}, "not a resolved absolute path"},
		{"the root", ContextLink{Dest: "/ctx/x", Source: "/"}, "contains /Users, and with it every home"},
		{"the users root", ContextLink{Dest: "/ctx/x", Source: "/Users"}, "contains /Users"},
		{"the data volume", ContextLink{Dest: "/ctx/x", Source: "/System/Volumes/Data"}, "contains /System/Volumes/Data/Users"},
		{"a firmlink spelling", ContextLink{Dest: "/ctx/x", Source: "/System/Volumes/Data/Users/Shared/x"}, "spelled through another name for /Users"},
		{"a case spelling", ContextLink{Dest: "/ctx/x", Source: "/users/Shared/x"}, "spelled through another name for /Users"},
		{"the sandbox home", ContextLink{Dest: "/ctx/x", Source: "/Users/_yolojail/notes"}, "the sandbox account's own home"},
		{"a real home", ContextLink{Dest: "/ctx/x", Source: "/Users/matt/code/lib"}, "inside the home folder /Users/matt"},
		{"a privacy-guarded dir", ContextLink{Dest: "/ctx/x", Source: "/Users/matt/Documents/notes"}, "/Users/matt/Documents, which macOS's privacy controls guard"},
		{"the state dir", ContextLink{Dest: "/ctx/x", Source: "/private/var/yolo-jail/ctx"}, "yolo's state directory"},
		{"an ancestor of the state dir", ContextLink{Dest: "/ctx/x", Source: "/var"}, "contains yolo's state directory /var/yolo-jail"},
		{"another volume", ContextLink{Dest: "/ctx/x", Source: "/Volumes/Backup/photos"}, "on another volume"},
		{"a read-write source off the shared root", ContextLink{Dest: "/ctx/x", Source: "/opt/scratch", RW: true}, "must sit under /Users/Shared/yolo"},
		{"a read-write source that IS the shared root", ContextLink{Dest: "/ctx/x", Source: "/Users/Shared/yolo", RW: true}, "must sit under /Users/Shared/yolo"},
		{"a source inside the workspace", ContextLink{Dest: "/ctx/x", Source: ctxWorkspace + "/vendor"}, "is inside the workspace"},
		{"a source containing the workspace", ContextLink{Dest: "/ctx/x", Source: "/Users/Shared/yolo"}, "contains the workspace"},
		{"a read-write source inside the workspace", ContextLink{Dest: "/ctx/x", Source: ctxWorkspace + "/out", RW: true}, "is inside the workspace"},
		{"a read-only source in /tmp", ContextLink{Dest: "/ctx/x", Source: "/private/tmp/x"}, "inside /private/tmp, which the sandbox may write"},
		{"a read-only source containing /dev", ContextLink{Dest: "/ctx/x", Source: "/dev"}, "is /dev, which the sandbox may write"},
		{"a read-only source that is /tmp", ContextLink{Dest: "/ctx/x", Source: "/private/tmp"}, "is /private/tmp, which the sandbox may write"},
		// /private holds /private/tmp too; the state dir inside it is named first.
		{"/private", ContextLink{Dest: "/ctx/x", Source: "/private"}, "contains yolo's state directory /private/var/yolo-jail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SiteContextLinks(DarwinContextSiting(), ctxWorkspace, []ContextLink{tc.link}, nil)
			if len(got) != 1 {
				t.Fatalf("SiteContextLinks(%+v) refused %d, want exactly 1: %+v", tc.link, len(got), got)
			}
			if !strings.Contains(got[0].Reason, tc.why) {
				t.Errorf("the refusal says %q, want it to say %q", got[0].Reason, tc.why)
			}
		})
	}
}

// ONE PATH, ONE MODE, AND ONE NAME PER LINK: a read-only source overlapping a read-write one, two
// links at nested jail paths, and a link at a path yolo's own staging uses each refuse, once.
func TestSiteContextLinksRefusesLinksThatCollide(t *testing.T) {
	inRW := ContextLink{Dest: "/ctx/raw", Source: rwLink.Source + "/raw", Dir: true}
	nested := ContextLink{Dest: "/ctx/lib/extra", Source: "/opt/extra", Dir: true}
	for _, tc := range []struct {
		name     string
		links    []ContextLink
		occupied []string
		why      string
	}{
		{"read-only inside read-write", []ContextLink{rwLink, inRW}, nil, "the read-write source " + rwLink.Source},
		{"read-write inside read-only", []ContextLink{{Dest: "/ctx/a", Source: "/Users/Shared/yolo/a", Dir: true},
			{Dest: "/ctx/b", Source: "/Users/Shared/yolo/a/b", RW: true, Dir: true}}, nil, "contains the read-write source"},
		{"nested jail paths", []ContextLink{roLink, nested}, nil, "a link cannot hold another link"},
		{"one jail path twice", []ContextLink{roLink, {Dest: "/ctx/lib", Source: "/opt/other", Dir: true}}, nil, "is /ctx/lib"},
		{"a reserved name", []ContextLink{{Dest: "/ctx/host-user", Source: "/opt/x", Dir: true}}, ContextOccupied(nil), "yolo's own staging uses"},
		{"around a composed file", []ContextLink{{Dest: "/ctx/host-claude", Source: "/opt/x", Dir: true}},
			ContextOccupied([]string{"/ctx/host-claude/settings.json"}), "contains /ctx/host-claude/settings.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SiteContextLinks(DarwinContextSiting(), ctxWorkspace, tc.links, tc.occupied)
			if len(got) != 1 {
				t.Fatalf("refused %d, want exactly one refusal for the pair: %+v", len(got), got)
			}
			if !strings.Contains(got[0].Reason, tc.why) {
				t.Errorf("the refusal says %q, want %q", got[0].Reason, tc.why)
			}
		})
	}
}

// THE PROFILE FROM THE PLAN BUILDER carries each context rule with its test id, every source
// named in the block its mode needs, in §3.4's order — judged per operation class, since
// last-match-wins is decided among the rules matching ONE operation.
func TestPlanSeatbeltCarriesTheContextRulesInOrder(t *testing.T) {
	sb := planWithLinks(t, roLink, rwLink).Seatbelt

	at := func(id string) int {
		i := strings.Index(sb, "#seatbelt-test-id:"+id+"#")
		if i < 0 {
			t.Fatalf("the plan's profile carries no #seatbelt-test-id:%s#:\n%s", id, sb)
		}
		return i
	}
	for _, c := range []struct{ id, src string }{
		{"context-read-allow", roLink.Source}, {"context-read-allow", rwLink.Source},
		{"context-write-allow", rwLink.Source}, {"context-readonly-deny", roLink.Source},
	} {
		if !strings.Contains(profileBlock(sb, c.id), `(subpath "`+c.src+`")`) {
			t.Errorf("block %s does not name %s:\n%s", c.id, c.src, profileBlock(sb, c.id))
		}
	}
	if strings.Contains(profileBlock(sb, "context-write-allow"), roLink.Source) {
		t.Errorf("a read-only source is in the write allow:\n%s", sb)
	}
	if strings.Contains(profileBlock(sb, "context-readonly-deny"), rwLink.Source) {
		t.Errorf("a read-write source is in the write deny:\n%s", sb)
	}
	// Writes: the rw allow after the root deny and the writable set, the ro deny after every
	// write allow, and nothing re-allows writes after it.
	if !(at("write-outside-deny") < at("workspace-write-allow") && at("workspace-write-allow") < at("context-write-allow") &&
		at("context-write-allow") < at("context-readonly-deny")) {
		t.Errorf("the write rules are out of order:\n%s", sb)
	}
	if strings.Contains(sb[at("context-readonly-deny"):], "(allow file-write") {
		t.Errorf("a write allow follows the read-only context deny, so it may not win:\n%s", sb)
	}
	// Reads: the source allow after the /Users deny it re-opens, before every keychain deny.
	if !(at("users-read-deny") < at("context-read-allow") && at("context-read-allow") < at("library-keychains-deny") &&
		at("context-read-allow") < at("system-keychains-deny")) {
		t.Errorf("the read allow is not between the /Users deny and the keychain denies:\n%s", sb)
	}
	// The traversal the workspace needed, for a source under /Users/Shared/: its intermediate
	// directory as a literal, never a subpath that would re-open its siblings.
	if !strings.Contains(profileBlock(sb, "workspace-read-allow"), `(literal "/Users/Shared/ci")`) {
		t.Errorf("no ancestor literal for %s:\n%s", roLink.Source, sb)
	}
	if strings.Contains(sb, `(subpath "/Users/Shared/ci")`) {
		t.Errorf("an ancestor of a source is granted as a subpath, re-opening its siblings:\n%s", sb)
	}
}

// NO CONTEXT MOUNT, NO CHANGE: the profile of a launch that delivers none is the one every
// launch got before this existed.
func TestAPlanWithNoContextLinksKeepsItsProfile(t *testing.T) {
	plan := planWithLinks(t)
	if want := SeatbeltProfile(ctxWorkspace, SandboxHome(), nil, HomeReadonly{}); plan.Seatbelt != want {
		t.Errorf("a plan with no context links changed the profile")
	}
	if strings.Contains(plan.Seatbelt, "context-") || len(plan.ContextPreflight) != 0 {
		t.Errorf("a plan with no links carries context rules or probes")
	}
}

// THE LINKS ARE STAGED IN THE CONTEXT DIR, root-owned, inside the `.new` swap — after the
// recursive chmod, so it never meets a link — with each parent made and opened explicitly,
// and before the endpoint grants, which stay last.
func TestPlanStagesEachContextLinkInsideTheSwap(t *testing.T) {
	plan := planWithLinks(t, roLink, optRO)
	tmp := plan.ContextDir + ".new"
	index := func(want ...string) int {
		for i, c := range plan.StageCommands {
			if strings.Join(c, "\x00") == strings.Join(want, "\x00") {
				return i
			}
		}
		t.Fatalf("no stage command %v in:\n%v", want, plan.StageCommands)
		return -1
	}
	chmodTree := index(chmodBin, "755", tmp)
	mkParent := index(mkdirBin, "-p", tmp+"/opt")
	openParent := index(chmodBin, "755", tmp+"/opt")
	link1 := index(lnBin, "-s", roLink.Source, tmp+"/lib")
	link2 := index(lnBin, "-s", optRO.Source, tmp+"/opt/tools")
	swap := index(mvBin, "-f", tmp, plan.ContextDir)
	if !(chmodTree < mkParent && mkParent < openParent && openParent < link1 && link1 < swap && link2 < swap) {
		t.Errorf("the context dir is staged out of order (chmod %d, mkdir %d, chmod parent %d, links %d %d, swap %d):\n%v",
			chmodTree, mkParent, openParent, link1, link2, swap, plan.StageCommands)
	}
}

// THE DAC PREFLIGHT asks the kernel as the sandbox account, never computes from mode bits:
// read for every source, search for a directory, write for a read-write one.
func TestPlanAsksTheDACPreflightOfEverySource(t *testing.T) {
	file := ContextLink{Dest: "/ctx/notes.txt", Source: "/opt/notes.txt"}
	var got []string
	for _, p := range planWithLinks(t, roLink, rwLink, file).ContextPreflight {
		got = append(got, p.Access+" "+strings.Join(p.Argv, " "))
	}
	want := []string{
		"read sudo --user=_yolojail /bin/test -r " + roLink.Source,
		"search sudo --user=_yolojail /bin/test -x " + roLink.Source,
		"read sudo --user=_yolojail /bin/test -r " + rwLink.Source,
		"search sudo --user=_yolojail /bin/test -x " + rwLink.Source,
		"write sudo --user=_yolojail /bin/test -w " + rwLink.Source,
		"read sudo --user=_yolojail /bin/test -r " + file.Source,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("preflight =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// PlanInvariants REFUSES A PLAN WHOSE LINK IS NOT BACKED, each half on its own: no rule naming
// the source in the profile (the link alone must never be what grants), no link staged, no
// preflight, and a link the siting refuses.
func TestPlanInvariantsCatchAContextLinkThatIsNotBacked(t *testing.T) {
	good := planWithLinks(t, roLink, rwLink)
	for _, tc := range []struct {
		name   string
		mutate func(*RunPlan)
		says   string
	}{
		{"a profile without the context rules", func(p *RunPlan) {
			p.Seatbelt = SeatbeltProfile(p.Workspace, SandboxHome(), nil, HomeReadonly{})
		}, "has no read allow"},
		{"a read-write link with no write allow", func(p *RunPlan) {
			p.Seatbelt = SeatbeltProfileWithContext(p.Workspace, SandboxHome(), nil, HomeReadonly{},
				[]ContextLink{roLink, {Dest: rwLink.Dest, Source: rwLink.Source, Dir: true}})
		}, "allows no write"},
		{"no link staged", func(p *RunPlan) {
			var kept [][]string
			for _, c := range p.StageCommands {
				if c[0] != lnBin {
					kept = append(kept, c)
				}
			}
			p.StageCommands = kept
		}, "is not staged as a link"},
		{"no preflight", func(p *RunPlan) { p.ContextPreflight = nil }, "is not checked by the DAC preflight"},
		{"an undeliverable source", func(p *RunPlan) {
			p.ContextLinks = append(p.ContextLinks, ContextLink{Dest: "/ctx/home", Source: "/Users/matt/notes", Dir: true})
		}, "inside the home folder /Users/matt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := good
			plan.StageCommands = append([][]string(nil), good.StageCommands...)
			plan.ContextLinks = append([]ContextLink(nil), good.ContextLinks...)
			tc.mutate(&plan)
			probs := strings.Join(PlanInvariants(plan), "\n")
			if !strings.Contains(probs, tc.says) {
				t.Errorf("PlanInvariants did not catch %s (want %q):\n%s", tc.name, tc.says, probs)
			}
		})
	}
}

// THE PREFLIGHT RUNS BEFORE THE NIX BUILD and a refusal ends the launch there, naming the
// source and the account — a source the sandbox cannot reach would otherwise fail only when
// the agent opens it, after a build that can take half an hour.
func TestRunMacosUserRefusesBeforeTheBuildWhenTheSandboxCannotReachASource(t *testing.T) {
	var rec []string
	deps := mockDeps(&rec)
	var out strings.Builder
	deps.Out = &out
	built := false
	deps.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		built = true
		return mockDarwin(), true, nil
	}
	base := deps.Run
	deps.Run = func(argv []string) int {
		if len(argv) == 5 && argv[2] == testBin && argv[4] == roLink.Source {
			base(argv)
			return 1
		}
		return base(argv)
	}
	opts := newOpts(ctxWorkspace)
	opts.HostCtx = HostContext{Links: []ContextLink{roLink, optRO}}

	if rc := RunMacosUser(deps, opts); rc != 1 {
		t.Fatalf("RunMacosUser = %d with an unreachable source, want 1\n%s", rc, out.String())
	}
	if built {
		t.Errorf("the nix build ran before the preflight refused")
	}
	for _, want := range []string{"cannot read " + roLink.Source, SandboxUser, SharedRootDefault()} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "cannot search "+roLink.Source) {
		t.Errorf("a source already refused was asked again:\n%s", out.String())
	}
	asked := strings.Join(rec, "\n")
	if !strings.Contains(asked, "run:sudo --user=_yolojail /bin/test -r "+optRO.Source) {
		t.Errorf("the other source was not asked, so one message would not name every source to move:\n%s", asked)
	}
}

// AND A PREFLIGHT THAT PASSES costs nothing but the probes: the launch goes on to its build.
func TestRunMacosUserGoesOnWhenEverySourceIsReachable(t *testing.T) {
	var rec []string
	deps := mockDeps(&rec)
	var out strings.Builder
	deps.Out = &out
	opts := newOpts(ctxWorkspace)
	opts.HostCtx = HostContext{Links: []ContextLink{roLink}}
	RunMacosUser(deps, opts)
	asked := strings.Join(rec, "\n")
	if !strings.Contains(asked, "run:sudo --user=_yolojail /bin/test -r "+roLink.Source) ||
		!strings.Contains(asked, "proxy:") {
		t.Errorf("a reachable source either was not asked or stopped the launch:\n%s\n%s", asked, out.String())
	}
}

// THE DRY RUN NAMES EACH LINK AT ITS STAGED PATH and prints the preflight among the privileged
// commands — never the /ctx spelling, which names nothing on macOS.
func TestPlanRenderNamesEachContextLinkAndItsPreflight(t *testing.T) {
	plan := planWithLinks(t, roLink, rwLink)
	var b strings.Builder
	PrintPlan(&b, plan, nil)
	got := b.String()
	for _, want := range []string{
		"context:     " + plan.ContextDir + "/lib → " + roLink.Source + " (read-only",
		"context:     " + plan.ContextDir + "/data → " + rwLink.Source + " (read-write",
		"sudo --user=_yolojail /bin/test -w " + rwLink.Source,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plan does not say %q:\n%s", want, got)
		}
	}
	var bare strings.Builder
	PrintPlan(&bare, planWithLinks(t), nil)
	if !strings.Contains(bare.String(), "context:     no context mounts") {
		t.Errorf("a plan with no context mounts does not say so:\n%s", bare.String())
	}
}

// THE STAGING COMMANDS WORK, run for real with `sudo` dropped and the state dir redirected —
// the part an argv assertion cannot see: that the links land at their paths pointing at their
// sources, that the recursive chmod leaves the sources alone, and that a re-stage replaces
// the links rather than nesting or keeping a dropped one. The tools are the ones the plan
// pins (/bin/ln and the rest), so this runs wherever they exist.
func TestStageContextDirCommandsMakeTheLinks(t *testing.T) {
	for _, bin := range []string{lnBin, mkdirBin, chmodBin, cpBin, mvBin, rmBin} {
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("%s is not here, so the staging commands cannot run: %v", bin, err)
		}
	}
	run := func(cmds [][]string) {
		t.Helper()
		for _, c := range cmds {
			if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("%v: %v\n%s", c, err, out)
			}
		}
	}
	sd := t.TempDir()
	src := t.TempDir()
	if err := os.Chmod(src, 0o700); err != nil {
		t.Fatal(err)
	}
	links := []ContextLink{
		{Dest: "/ctx/lib", Source: src, Dir: true},
		{Dest: "/ctx/deep/er/tools", Source: src, Dir: true},
	}
	// With a composed tree beside the links, so the recursive `chmod -R a+rX` runs too.
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "host-user"), 0o700); err != nil {
		t.Fatal(err)
	}
	run(StageContextDirCommands(tree, links, "proj", sd))
	dst := StagedCtxRoot("proj", sd)
	if fi, err := os.Stat(filepath.Join(dst, "host-user")); err != nil || fi.Mode().Perm()&0o005 != 0o005 {
		t.Errorf("the composed tree was not staged beside the links, opened to others: %v %v", fi, err)
	}
	for _, rel := range []string{"lib", "deep/er/tools"} {
		target, err := os.Readlink(filepath.Join(dst, rel))
		if err != nil || target != src {
			t.Errorf("%s is not a link to %s (%q, %v)", rel, src, target, err)
		}
	}
	for _, dir := range []string{"deep", "deep/er"} {
		if fi, err := os.Stat(filepath.Join(dst, dir)); err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("parent %s is not 0755: %v %v", dir, fi, err)
		}
	}
	if fi, _ := os.Stat(src); fi.Mode().Perm() != 0o700 {
		t.Errorf("staging changed the SOURCE's mode to %v — a chmod followed a link into the user's files", fi.Mode().Perm())
	}
	// Re-stage with one link dropped: it must be gone, the other in place, nothing nested.
	run(StageContextDirCommands("", links[:1], "proj", sd))
	if _, err := os.Lstat(filepath.Join(dst, "deep")); !os.IsNotExist(err) {
		t.Errorf("a dropped link survived the re-stage: %v", err)
	}
	if target, _ := os.Readlink(filepath.Join(dst, "lib")); target != src {
		t.Errorf("the kept link is %q after a re-stage", target)
	}
	if _, err := os.Lstat(filepath.Join(dst, "proj.new")); !os.IsNotExist(err) {
		t.Errorf("the re-stage nested the new tree inside the old one")
	}
}
