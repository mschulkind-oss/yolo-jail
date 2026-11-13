package run

// programoverrides.go is every notch's reading of a DEV OVERRIDE (docs/design/pack-buildable-programs.md
// §5, a term the design coins): `programs.overrides.<bin>: {"path": …}` in the USER config
// (config.ProgramOverrides, the one reader; a workspace config cannot set it) makes yolo BUILD that
// program from the user's checkout at `path`, in place of its pack's `from` tree, at each fresh launch
// (packTreePins), each `yolo host` floor resolution (the cli's floorPackTree) and each `yolo pack
// build` (packBuildOne).
//
// THE CHECKOUT IS A PACK'S TREE, AND NOTHING ELSE CHANGES (OQ-BG3, ruled 2026-10-23): the same sealed
// build act, the pack's own `build` and `run`, keyed by the tree digest of the checkout as it sits on
// disk at that moment (its working tree, uncommitted edits included, `.git` skipped: BG-D20), and
// cached like any build, so an unchanged checkout is reused and an edited one is rebuilt at the next
// fresh launch. There is no live mount, no build at run time and no pickup in a running jail: a jail
// runs what its own launch built, as a fork or a pi extension does. A checkout that cannot be read, or
// a build that fails, is the pack-tree program's own refusal, through the readiness act.
//
// It applies to a program a pack builds from its own tree (`via: "pack"`) and to nothing else
// (BG-D19): an override naming any other program is refused (NonPackTreeOverrideLines), naming the
// fork pack that delivers a checkout of it.

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/nofollow"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// overrideUserConfigKey is the key and file every override line names.
func overrideUserConfigKey(bin string) string {
	return "programs.overrides." + bin + " in " + userConfigTilde()
}

// userConfigTilde is the user config file that exists (paths.UserConfigDisplayPath), written
// ~/… under the home, as the override lines have always spelled it (LC-D12).
func userConfigTilde() string {
	p := paths.UserConfigDisplayPath()
	if home := strings.TrimRight(paths.Home(), "/"); home != "" && strings.HasPrefix(p, home+"/") {
		return "~/" + strings.TrimPrefix(p, home+"/")
	}
	return p
}

// ProgramCheckout applies the user's override of f, a pack-tree program, when overrides names its
// bin: f with Checkout set to the path, or why the override cannot apply, naming the path and the key.
// f comes back unchanged with no reason when there is no override. The checks, the same on every
// notch: the path is resolved once, following only TRUSTED symlinks — owned by root, in a directory
// only root can write, such as macOS's /var → /private/var or Fedora Atomic's /home → /var/home — and
// refusing any other, naming the resolved path to write instead (nofollow.Resolve, BG-D22); it is an
// existing directory; and it is not at the credential boundary (paths.WorkspaceScopeBreach: the home,
// the user config directory, yolo's state directory, or anything containing one), which yolo never
// reads into a build. Checkout is the resolved path.
//
// Why only root's links: a checkout may sit where an agent can write, and a link it placed is one it
// could re-point between this check and the read, at ~/.ssh say. The read below (internal/nofollow,
// through PackTreeDigest and the build's copy) reaches the resolved path the same way, a component at
// a time, so a link swapped in after this check refuses the read instead of being followed.
func ProgramCheckout(f packload.Fork, overrides map[string]config.ProgramOverride) (packload.Fork, string) {
	ov, ok := overrides[f.Bin]
	if !ok || !f.IsPackTree() {
		return f, ""
	}
	key := " (" + overrideUserConfigKey(f.Bin) + ")"
	dir, err := nofollow.Resolve(ov.Path)
	var untrusted *nofollow.UntrustedLinkError
	switch {
	case errors.As(err, &untrusted):
		to := "the directory it points at"
		if real, err := filepath.EvalSymlinks(ov.Path); err == nil {
			to = real
		}
		return f, ov.Path + " passes through " + untrusted.Path + ", a symlink " + untrusted.Why + ", and yolo reads a " +
			"checkout only through symlinks root placed — set the path to " + to + key
	case err != nil:
		return f, ov.Path + " does not exist or cannot be read — fix the path, or remove the setting to build pack " +
			f.Pack + "'s own " + f.From + key
	case !isDir(dir):
		return f, ov.Path + " is not a directory — point it at your checkout of " + f.Bin + key
	}
	if breach := paths.WorkspaceScopeBreach(dir); breach != nil {
		return f, breach.WhatFor("the override path") + ", the credential boundary yolo never builds from — " +
			"move the checkout elsewhere" + key
	}
	f.Checkout = dir
	return f, ""
}

// ResolvePackTreeProgram is the whole resolver for one pack-tree program on platform, every notch's
// one call (docs/design/pack-buildable-programs.md §4): the user's checkout (ProgramCheckout) when an
// override names it, built from that checkout or refused; otherwise packload.PackTreeResolve, with
// usePrebuilt the user's prebuilt opt-in (BG-D18).
func ResolvePackTreeProgram(f packload.Fork, platform string, usePrebuilt bool,
	overrides map[string]config.ProgramOverride) packload.ForkPin {
	f, why := ProgramCheckout(f, overrides)
	if why != "" {
		return packload.ForkPin{Fork: f, Reason: why}
	}
	return packload.PackTreeResolve(f, platform, usePrebuilt)
}

// CheckoutLine is a checkout build's disclosure (BG-D10, OQ-BG3's ruling): "<bin>: built from your
// checkout <dir> (programs.overrides.<bin> in ~/.config/yolo-jail/config.jsonc) — <how>", where how
// says whether this launch rebuilt it or reused the build of an unchanged checkout.
func CheckoutLine(bin, dir, how string) string {
	return bin + ": built from your checkout " + dir + " (" + overrideUserConfigKey(bin) + ") — " + how
}

// checkoutHow is CheckoutLine's how for a build at digest: reused when the store already held it.
func checkoutHow(digest string, reused bool) string {
	if reused {
		return "reused its build at tree digest " + shortDigest(digest) + ", the checkout unchanged"
	}
	return "rebuilt at tree digest " + shortDigest(digest)
}

// shortDigest is a digest's first 12 characters.
func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// noteCheckoutDeliveries says the line for each pack-tree program this fresh launch built, or reused,
// from the user's checkout: one the slot answered with a store key. One it could not deliver is said
// by the slot's own warning and refused by the readiness act, so no line names bytes that do not run.
func (o *Options) noteCheckoutDeliveries() {
	var lines []string
	for _, p := range o.forkPinned {
		f := p.Fork
		d := o.forkDelivered[f.Bin]
		if f.Checkout == "" || d.Key == "" {
			continue
		}
		lines = append(lines, CheckoutLine(f.Bin, f.Checkout, checkoutHow(p.Commit, d.Reused)))
	}
	sort.Strings(lines)
	for _, l := range lines {
		o.pr(o.Stderr).printf("%s", richtext.Escape(l))
	}
}

// handedCheckout fills h's checkout fields for bin when this launch builds bin from the user's
// checkout, so an attach to its jail names the checkout it runs (noteAttachForkBuilds).
func (o *Options) handedCheckout(bin string, h HandedFork) HandedFork {
	for _, p := range o.forkPinned {
		if f := p.Fork; f.Bin == bin && f.Checkout != "" {
			h.Checkout, h.CheckoutDigest = f.Checkout, p.Commit
		}
	}
	return h
}

// declaredProgram is what an override reads off the program it names.
type declaredProgram struct {
	pack string
	// via is the program's delivery in the manifest's word: "pack" for a program built from its
	// pack's tree, else the install's kind ("npm", "native", "source").
	via string
}

// declaredPrograms is every bin a selected pack declares as a program, first pack winning, as the
// jail's launcher generation takes them.
func declaredPrograms(packs []*packload.Pack) map[string]declaredProgram {
	out := map[string]declaredProgram{}
	for _, p := range packs {
		if p == nil {
			continue
		}
		installs, _ := p.HonoredInstalls()
		for _, inst := range installs {
			if inst.Bin == "" {
				continue
			}
			if _, ok := out[inst.Bin]; ok {
				continue
			}
			via := inst.Kind
			if inst.IsPackTree() {
				via = packdecl.ViaPack
			}
			out[inst.Bin] = declaredProgram{pack: p.Name, via: via}
		}
	}
	return out
}

// NonPackTreeOverrideLines are the refusals of the user's overrides that name a program a selected
// pack declares but does not build from its own tree (BG-D19): one per such bin, in bin order, each
// naming the program, its delivery, and the next step, a fork pack whose source is the checkout. A
// launch, `yolo host` and `yolo check` all refuse on them: an override that silently did nothing
// would leave the user running bytes they did not choose.
func NonPackTreeOverrideLines(packs []*packload.Pack, overrides map[string]config.ProgramOverride) []string {
	declared := declaredPrograms(packs)
	var out []string
	for bin := range overrides {
		d, ok := declared[bin]
		if !ok || d.via == packdecl.ViaPack {
			continue
		}
		out = append(out, overrideUserConfigKey(bin)+" names "+bin+", which pack "+d.pack+" delivers via \""+
			d.via+"\"; an override builds only a program a pack builds from its own tree (via = \"pack\") — to "+
			"run your checkout of "+bin+", declare a fork pack of your own whose [[program]] has via = \"source\", "+
			"fork_of = \""+d.pack+"\" and source = \"git+file:///path/to/your/checkout?ref=<branch>\" "+
			"(docs/reference/pack-system.md, forks), select it, and remove this setting")
	}
	sort.Strings(out)
	return out
}

// UndeclaredProgramOverrideLines is `yolo check`'s information about the user's overrides that name
// a bin no selected pack declares: they do nothing in this workspace, which is not a failure,
// because the user config spans every workspace (§5). A prebuilt opt-in (BG-D18) is reported the same
// way, and also where the program it names declares no `prebuilt` at all, which it cannot opt into;
// one missing only for this launch's platform is the launch's refusal (packload.PackTreeResolve).
func UndeclaredProgramOverrideLines(packs []*packload.Pack) []string {
	declared := declaredPrograms(packs)
	var out []string
	for bin := range config.ProgramOverrides(nil) {
		if _, ok := declared[bin]; !ok {
			out = append(out, "programs.overrides."+bin+": no selected pack declares "+bin+
				", so it does nothing in this workspace")
		}
	}
	prebuilts := map[string]packload.Fork{}
	for _, f := range packload.PackTreePrograms(packs) {
		if _, seen := prebuilts[f.Bin]; !seen {
			prebuilts[f.Bin] = f
		}
	}
	for bin := range config.ProgramPrebuiltOptIns(nil) {
		f, packTree := prebuilts[bin]
		d, isDeclared := declared[bin]
		switch {
		case !isDeclared:
			out = append(out, "programs.overrides."+bin+".prebuilt: no selected pack declares "+bin+
				", so it does nothing in this workspace")
		case !packTree || len(f.Prebuilts) == 0:
			out = append(out, "programs.overrides."+bin+".prebuilt: pack "+d.pack+" declares no "+
				"prebuilt artifact for "+bin+", so it does nothing — remove the setting")
		}
	}
	sort.Strings(out)
	return out
}

// refuseNonPackTreeOverrides is a fresh launch's refusal of NonPackTreeOverrideLines, before
// anything is built or started: true when it refused, having said why.
func (o *Options) refuseNonPackTreeOverrides(packs []*packload.Pack) bool {
	if o.CapturesDir == nil || o.CapturesDir() == "" || o.Sealed {
		return false
	}
	lines := NonPackTreeOverrideLines(packs, config.ProgramOverrides(nil))
	for _, l := range lines {
		o.pr(o.Stderr).printf("[red]Refusing to launch: %s[/red]", richtext.Escape(l))
	}
	return len(lines) > 0
}
