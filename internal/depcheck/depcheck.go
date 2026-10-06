// Package depcheck is the shared host-dependency checker (env-manager plan Phase 6,
// OQ-8). Below the jail notch yolo bakes no image, so a pack's `packages`/`program`
// dependency becomes a question about the host — is the binary present, and if not what
// installs it. The design's rule: ONE checker, used by `check`, by `apply`, and by a
// project's own doctor, over ONE declared list (the pack's install_hints) — so nobody
// re-implements "is psql present." OQ-8 chose a declared schema with a reference checker
// over it (this package) rather than a Go-package-only boundary, so a third-party doctor
// can read the same hints and probe with its own code.
//
// This package is deliberately dependency-light — it takes plain requirements (bin +
// per-manager hints) and returns plain results, so both the CLI and a standalone
// `yolo check-deps` call it without dragging config/entrypoint in.
package depcheck

import (
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// Requirement is one binary a host must provide, with the per-manager package names
// that install it. Mirrors packdecl.DepRequirement but kept local so this package has
// no packdecl dependency (the caller adapts).
type Requirement struct {
	// Hints maps a hint key to the package name. Most keys are a manager name
	// ("brew"|"apt"|"dnf"|"pacman"|"nix"); "brew-cask" is the one INSTALLER-FLAVOR key —
	// see brewCaskHint.
	Bin   string
	Hints map[string]string
	// SelfInstall is the command the declaring PACK carries for this binary — the tool's
	// own first-party installer (`npm install -g <pkg>`, or an installer URL's
	// download-check-run command). PREFERRED over a package-manager hint when present; see
	// selfInstallFlavor for why.
	SelfInstall string
	// SelfInstallNoTerminal says SelfInstall runs a vendor installer script, which a caller
	// that runs the remedy must run with no controlling terminal and a /dev/null stdin
	// (docs/design/provisioner-sets.md PS-D1). Carried onto Result.NoTerminal only when the
	// remedy chosen IS SelfInstall: a package-manager hint keeps the terminal, because its
	// `sudo` asks for a password there.
	SelfInstallNoTerminal bool
	// Unpublished is why the declaring program's vendor publishes NO BUILD for this host,
	// "" when it does (packdecl.DepRequirement.UnpublishedReason, the one installable-program
	// predicate a jail's launcher generation asks too). A binary with a reason is still probed
	// — one the user built themselves is present — but a missing one gets no remedy, no
	// Brewfile line and no place in Missing: nothing is missing that anything could install.
	Unpublished string
	// Prefer is the USER'S PROVISIONER ORDER for this binary: provisioner names (Pack, or a
	// manager in managers), most preferred first, from the user-scope `provisioners` key
	// (docs/design/provisioner-sets.md PS-D11). nil is yolo's default order alone. It RE-RANKS and
	// never removes: Check walks it first and yolo's default order after it, so a binary the
	// user's list has no recipe for still gets the remedy it gets today (resolutionOrder).
	Prefer []string
}

// Pack is the PROVISIONER NAME for the declaring pack's own recipe (Requirement.SelfInstall):
// the npm package, or the vendor's installer through the download-check-run command. It is the
// word the user's `provisioners` order spells it with, beside the manager names, and the one
// yolo's default order puts first (OQ-PS6's answer).
const Pack = "pack"

// Provisioners is every provisioner name an order may rank, in the order a message lists them:
// the pack's own recipe, then every package manager this package knows. A name outside it is a
// provisioner this yolo cannot drive, which the config refuses rather than skips.
func Provisioners() []string { return append([]string{Pack}, managers...) }

// HasRecipe reports whether r carries a recipe for provisioner: its own installer for Pack, an
// install hint for a manager (brew's cask flavor counting for brew, as hintFor reads it). The
// config's per-package order is refused for a provisioner no selected pack ships a recipe for
// (docs/design/provisioner-sets.md PS-D3), and asks it here so the refusal and the resolver read
// one rule.
func HasRecipe(r Requirement, provisioner string) bool {
	if provisioner == Pack {
		return r.SelfInstall != ""
	}
	_, _, ok := hintFor(r.Hints, provisioner)
	return ok
}

// IsProvisioner reports whether name is one of Provisioners.
func IsProvisioner(name string) bool {
	for _, p := range Provisioners() {
		if p == name {
			return true
		}
	}
	return false
}

// brewCaskHint is the hint key for a Homebrew CASK (an app bundle / prebuilt binary
// distribution) as opposed to a formula. Brew is the only manager with this split, and it
// is not cosmetic: a Brewfile's two verbs are different commands —
//
//	brew "postgresql@16"   # a FORMULA
//	cask "claude-code"     # a CASK
//
// `brew bundle` on a `brew "<cask-token>"` line fails looking for a formula that does not
// exist. The printed one-liner hid this for a while because bare `brew install <token>`
// falls back to a cask when no formula matches, so only the generated BUNDLE was broken.
//
// It is a hint KEY rather than a per-hint struct because install_hints' whole virtue is one
// line per manager; a nested object would rewrite every existing hint for one flag. Nothing
// else grows a variant — apt/dnf/pacman/nix have no equivalent split.
//
// DetectManager still returns plain "brew": the flavor is a property of the PACKAGE, not of
// the host, so the manager stays "brew" and the lookup consults "brew-cask" first.
const brewCaskHint = "brew-cask"

// selfInstallFlavor is the Flavor recorded when the remedy came from the PACK'S OWN
// installer rather than from a host package manager.
//
// It is not a manager name, and that is what makes it useful downstream: Manifest cannot put
// `curl … | sh` in a Brewfile, so the flavor is what tells it to leave this dep out of the
// bundle while the printed remedy still names the command.
//
// WHY IT WINS over a package-manager hint. Every tool that ships its own installer ships its
// own UPDATER, and routing the user through a distro package instead hands them whatever that
// repo has: measured 2026-08-02, nixpkgs was current for claude-code/codex/pi-coding-agent
// and github-copilot-cli was 16 releases behind (1.0.61 vs 1.0.77), with nothing in the
// output to say which. Preferring the first-party installer removes the staleness question
// entirely instead of trying to label it.
const selfInstallFlavor = "self"

// hintKeys returns the hint keys to try, in order, for a detected manager. Only brew has
// more than one: its cask flavor is preferred because a pack that declares both means "this
// is a cask, and here is the formula fallback" — a formula with the same token is the
// wrong-package trap (brew's `copilot` formula is AWS's deprecated ECS CLI, nothing to do
// with the `copilot-cli` cask).
func hintKeys(mgr string) []string {
	if mgr == "brew" {
		return []string{brewCaskHint, "brew"}
	}
	return []string{mgr}
}

// hintFor picks the package name and the install FLAVOR for a detected manager, or ok=false
// when no hint covers it — always so when no manager was detected (mgr ""). flavor is the key
// installCmd/Manifest switch on — "brew-cask" where the pack declared a cask, otherwise the
// manager itself.
func hintFor(hints map[string]string, mgr string) (pkg, flavor string, ok bool) {
	if mgr == "" {
		return "", "", false
	}
	for _, k := range hintKeys(mgr) {
		if p, found := hints[k]; found {
			return p, k, true
		}
	}
	return "", "", false
}

// Result is the probe outcome for one Requirement.
type Result struct {
	Bin     string
	Present bool
	Path    string // resolved path when present
	// Remedy is the install command for the detected host package manager, or "" when
	// no hint covers it (reported as unprobeable-remedy, never as satisfied).
	Remedy string
	// Manager is the detected manager the remedy is for, "" when the lookup found none (NoManager
	// says so in words).
	Manager string
	// Hinted is whether the requirement declares any install hint. NoManager is the reason a
	// missing binary has no remedy only when it is: a binary with no hint has none on any host.
	Hinted bool
	// Flavor is the hint KEY the remedy came from — the same as Manager except for
	// brewCaskHint and selfInstallFlavor. Carried per-result rather than per-manifest because
	// one brew host can need both verbs: a Brewfile mixing `brew "postgresql@16"` and
	// `cask "claude-code"` is the normal case, so "which verb" cannot be a property of the
	// detected manager.
	Flavor string
	// Fallback is the package-manager remedy for a dep whose primary Remedy came from the
	// pack's own installer — reported as an alternative rather than dropped, since a user
	// who would rather go through their package manager should still see the token. Empty
	// whenever Remedy already IS the manager's command. It is printed and never bundled:
	// Manifest leaves such a dep out (planBundle says why).
	Fallback string
	// NoTerminal is whether Remedy must run with no controlling terminal and a /dev/null stdin:
	// it is the pack's own installer script (Requirement.SelfInstallNoTerminal). False for a
	// package-manager remedy, and for a binary with no remedy.
	NoTerminal bool
	// Unpublished is Requirement.Unpublished, carried onto a binary that is ABSENT, and ""
	// for a present one. Such a result is not missing (Missing leaves it out) and has no
	// remedy: the reason is the whole of what a report says about it.
	Unpublished string
	// Via is the provisioner Remedy came from: Pack, or the manager whose command it is. "" when
	// there is no remedy.
	Via string
	// FallbackVia is the provisioner Fallback came from, "" when there is none: a manager when Via
	// is Pack (the default case), and Pack when the user's order put a manager first.
	FallbackVia string
	// Ranked is whether the USER'S ORDER chose Via (Requirement.Prefer): yolo's default order
	// alone would have chosen another remedy, or none. A report says so beside the command, since
	// it is not the one the pack leads with.
	Ranked bool
}

// AltLabel is the words a report puts before Fallback: "via brew", or "the pack's own
// installer" when the user's order ranked a manager above it. "" when there is no Fallback.
func (r Result) AltLabel() string {
	switch {
	case r.Fallback == "":
		return ""
	case r.FallbackVia == Pack:
		return "the pack's own installer"
	case r.FallbackVia != "":
		return "via " + r.FallbackVia
	}
	return "via " + r.Manager
}

// Lookup resolves a binary on one PATH, exec.LookPath's shape. Every probe here takes it from
// its CALLER (docs/reference/host-agent-environment.md, one resolver): at the host that is the launch PATH's
// one resolver (internal/hostpath), so the dependency probe, the package-manager guess and the
// re-probe after an install all read the PATH the launch does, `host_path` included. A nil
// Lookup is LookPath.
type Lookup func(bin string) (string, error)

// LookPath is the probe used when a caller passes no Lookup — a TEST SEAM, so a check in this
// package's tests does not depend on the machine's real PATH. No production caller passes nil.
var LookPath = exec.LookPath

// lookupOrDefault is look, or LookPath when look is nil.
func lookupOrDefault(look Lookup) Lookup {
	if look != nil {
		return look
	}
	return func(bin string) (string, error) { return LookPath(bin) }
}

// managers is every host package manager this package knows, in the order detectManager probes
// them (macOS asks brew first). nix is LAST and PROBED like the rest: it used to be returned by
// elimination, unlooked-for, so a host with no manager was told to run `nix profile install` while
// `yolo check` reported nix missing (docs/design/provisioner-sets.md, OQ-PS9's answer).
var managers = []string{"apt", "dnf", "pacman", "brew", "nix"}

// NoManager is why a missing binary has no package-manager remedy when the lookup found none of
// managers (Result.Manager ""): the words both reports print, so `yolo check-deps` and
// `yolo host apply` cannot disagree about it.
var NoManager = "no package manager yolo knows is on this PATH (" +
	strings.Join(managers[:len(managers)-1], ", ") + " or " + managers[len(managers)-1] + ")"

// DetectManager returns the host package manager to prefer for remedies, found through look, or ""
// when the lookup finds none of them: a remedy never names a manager this PATH lacks. Overridable in
// tests. Order: on macOS prefer brew; then apt/dnf/pacman/brew in turn; nix last.
//
// Through the CALLER'S lookup, not a bare exec.LookPath: a launcher whose PATH lacks
// /opt/homebrew/bin has no `brew` on the PATH the dependency probe reads either, and naming
// Homebrew's remedy there while the probe could not see Homebrew's folder would be two answers
// about one PATH.
var DetectManager = detectManager

func detectManager(look Lookup) string {
	look = lookupOrDefault(look)
	order := managers
	if runtime.GOOS == "darwin" {
		order = append([]string{"brew"}, managers...)
	}
	for _, m := range order {
		if _, err := look(m); err == nil {
			return m
		}
	}
	return ""
}

// Check probes every requirement through look and returns the results in Bin order. It
// never installs anything — it reports (BACKLOG's "detect vs. apply" split); the caller
// decides whether to offer to run the remedies. The package manager a remedy names is found
// through the same look.
//
// REMEDY PRECEDENCE: the user's order first (Requirement.Prefer), then yolo's default — the
// declaring pack's OWN installer, then the detected package manager's hint. The first
// provisioner that is on this PATH and has a recipe for the binary wins (resolutionOrder). See
// selfInstallFlavor for why the default leads with the pack's installer — in short, a tool with
// a first-party installer has a first-party updater, and a distro package silently pins it to
// whatever that repo has; a user who ranks a manager above it accepts that cadence knowingly
// (docs/design/provisioner-sets.md §8.2). The runner-up between the pack's installer and a
// manager is kept as Fallback rather than discarded, so the user still sees the other token.
func Check(reqs []Requirement, look Lookup) []Result {
	look = lookupOrDefault(look)
	mgr := DetectManager(look)
	onPath := managerProbe(look, mgr)
	var out []Result
	for _, r := range reqs {
		res := Result{Bin: r.Bin, Manager: mgr, Hinted: len(r.Hints) > 0}
		switch {
		case presentAt(look, r.Bin, &res):
			// probed present; nothing to remedy
		case r.Unpublished != "":
			// Absent, and no vendor build exists for this host: there is no remedy to offer,
			// not a hint the pack forgot. Asked AFTER the probe, the jail's order — a binary
			// the host already has is present whatever the vendor publishes.
			res.Unpublished = r.Unpublished
		default:
			resolveRemedy(r, mgr, onPath, &res)
		}
		out = append(out, res)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bin < out[j].Bin })
	return out
}

// Winner is the provisioner that would supply r on look's PATH: Pack, a manager name, or ""
// when nothing in r's order has a recipe here. It is the question Check answers for a missing
// binary, asked of one that may be present: the host floor asks it to learn whether the user's
// order gives a program to a manager instead of to the pack's own recipe, which is what the floor
// installs (docs/design/provisioner-sets.md PS-D12). One resolver, so the floor and the
// dependency report cannot disagree about who supplies a binary.
func Winner(r Requirement, look Lookup) string { return Resolve(r, look).Via }

// Resolve is Winner with the winner's command: the Result Check gives r when it is missing — Via,
// Remedy and its Fallback — whether or not it is present. A host launch that runs the copy the
// user's order gives a manager reads it to name that manager's install when the copy is not
// there yet, the command `yolo check-deps` prints for it.
func Resolve(r Requirement, look Lookup) Result {
	look = lookupOrDefault(look)
	mgr := DetectManager(look)
	res := Result{Bin: r.Bin, Manager: mgr, Hinted: len(r.Hints) > 0}
	resolveRemedy(r, mgr, managerProbe(look, mgr), &res)
	return res
}

// managerProbe answers "is manager m on this PATH?", each manager probed at most once per
// Check. The detected manager is known to be.
func managerProbe(look Lookup, detected string) func(string) bool {
	seen := map[string]bool{}
	return func(m string) bool {
		if m == detected {
			return true
		}
		if ok, done := seen[m]; done {
			return ok
		}
		_, err := look(m)
		seen[m] = err == nil
		return seen[m]
	}
}

// remedyCandidate is one provisioner that can supply a binary here, with its command.
type remedyCandidate struct {
	via, remedy, flavor string
	noTerminal          bool
}

// resolutionOrder is the order one requirement's provisioners are tried in: the user's
// (Prefer), then yolo's default — the pack's own recipe, then the detected manager — each name
// once. Appending the default rather than replacing it is what makes the user's list a
// RE-RANKING: a provisioner the list leaves out is still tried after it, so naming brew does not
// leave an npm-only program with no remedy (docs/design/provisioner-sets.md PS-D11).
func resolutionOrder(prefer []string, detected string) []string {
	var out []string
	seen := map[string]bool{"": true}
	for _, v := range append(append([]string(nil), prefer...), Pack, detected) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// candidates is every provisioner in order that can supply r here: Pack when the pack carries
// its own installer, a manager when it is on this PATH and the pack hints a package for it.
func candidates(r Requirement, order []string, onPath func(string) bool) []remedyCandidate {
	var out []remedyCandidate
	for _, via := range order {
		if via == Pack {
			if r.SelfInstall != "" {
				out = append(out, remedyCandidate{via: Pack, remedy: r.SelfInstall,
					flavor: selfInstallFlavor, noTerminal: r.SelfInstallNoTerminal})
			}
			continue
		}
		if !onPath(via) {
			continue
		}
		if pkg, flavor, ok := hintFor(r.Hints, via); ok {
			out = append(out, remedyCandidate{via: via, remedy: installCmd(flavor, pkg), flavor: flavor})
		}
	}
	return out
}

// resolveRemedy fills res's remedy for a missing r: the first candidate in the resolution order,
// and as Fallback the first later one of the other sort (a manager after the pack's installer,
// the pack's installer after a manager), which is the alternative a report prints second.
// res.Manager becomes the manager whose command the remedy or its Fallback is, so a bundle and
// a report name the manager they actually use.
func resolveRemedy(r Requirement, mgr string, onPath func(string) bool, res *Result) {
	cands := candidates(r, resolutionOrder(r.Prefer, mgr), onPath)
	if len(cands) == 0 {
		return
	}
	win := cands[0]
	res.Remedy, res.Flavor, res.Via, res.NoTerminal = win.remedy, win.flavor, win.via, win.noTerminal
	if win.via != Pack {
		res.Manager = win.via
	}
	for _, c := range cands[1:] {
		if (c.via == Pack) != (win.via == Pack) {
			res.Fallback, res.FallbackVia = c.remedy, c.via
			if c.via != Pack {
				res.Manager = c.via
			}
			break
		}
	}
	if len(r.Prefer) > 0 {
		def := candidates(r, resolutionOrder(nil, mgr), onPath)
		res.Ranked = len(def) == 0 || def[0].via != win.via
	}
}

// presentAt probes for bin and records the resolved path on res, reporting whether it was
// found. Split out so Check's precedence reads as one switch rather than an if/else chain
// with a probe buried in its condition.
func presentAt(look Lookup, bin string, res *Result) bool {
	p, err := look(bin)
	if err != nil {
		return false
	}
	res.Present, res.Path = true, p
	return true
}

// Present re-probes ONE binary and reports where it resolved. It is the RE-PROBE half of an
// install: after a remedy has run, "did that actually produce the binary?" is the only question
// whose answer may be trusted, because an installer that exits 0 and delivers nothing leaves
// the environment exactly as unready as one that failed loudly
// (docs/reference/report-tiers.md's dependency rule point 5, where a still-missing binary is
// the same fatal as a declined install).
//
// Through the caller's look, the one Check was handed, so the re-probe reads the PATH the
// probe did (and the install ran with) rather than a second opinion — two probes over two PATHs
// is the drift this package exists to prevent.
func Present(bin string, look Lookup) (path string, ok bool) {
	p, err := lookupOrDefault(look)(bin)
	if err != nil {
		return "", false
	}
	return p, true
}

// installCmd builds the install command for a package, keyed by hint FLAVOR (a manager
// name, or brewCaskHint).
func installCmd(flavor, pkg string) string {
	switch flavor {
	case "brew":
		return "brew install " + pkg
	case brewCaskHint:
		// Explicit --cask even though bare `brew install <token>` would fall back to one:
		// the fallback silently prefers a same-named FORMULA when one exists, which is how
		// `copilot` (AWS ECS CLI) gets installed instead of the copilot-cli cask.
		return "brew install --cask " + pkg
	case "apt":
		return "sudo apt install -y " + pkg
	case "dnf":
		return "sudo dnf install -y " + pkg
	case "pacman":
		return "sudo pacman -S --noconfirm " + pkg
	case "nix":
		return "nix profile install " + nixInstallables(pkg)
	default:
		return ""
	}
}

// hintStepSeparator is where a hint's packages end and its one step begins, the convention
// packdecl's InstallHints states and its decoder enforces. Spelled here as well because this
// package takes no packdecl dependency (Requirement); nixInstallables is its one reader.
const hintStepSeparator = " && "

// nixInstallables spells a hint's package part as nix INSTALLABLES, `nixpkgs#<package>` for
// EACH package, and leaves a step after the separator as written. Every other manager takes
// package names as its arguments, so only nix needs this: concatenating the prefix onto the
// whole hint gave the first package alone a `nixpkgs#`, and `nix profile install` read every
// later one as a flake reference — `ripgrep` a registry lookup, `.` the current directory's
// flake. The empty hint keeps the bare prefix, which bundleToken strips to recover a token.
func nixInstallables(hint string) string {
	pkgs, step, chained := strings.Cut(hint, hintStepSeparator)
	names := strings.Fields(pkgs)
	if len(names) == 0 {
		return "nixpkgs#" + hint
	}
	out := "nixpkgs#" + strings.Join(names, " nixpkgs#")
	if chained {
		out += hintStepSeparator + step
	}
	return out
}

// Missing returns the results that are absent, remedy or not — every declared binary this host
// lacks and could have. An absent binary whose vendor publishes no build here (Unpublished) is
// not one: nothing is missing that anything could install.
func Missing(results []Result) []Result {
	var out []Result
	for _, r := range results {
		if !r.Present && r.Unpublished == "" {
			out = append(out, r)
		}
	}
	return out
}

// Manifest renders the missing remedies as the package manager's own bundle file — a
// Brewfile for brew, a plain `pkg pkg pkg` install line for the others — so the user
// tunes the host up in one step rather than running N lines. Returns ("", "") when
// nothing is missing or nothing has a remedy.
//
// A Brewfile distinguishes formulae from casks (`brew "x"` vs `cask "x"`); a package whose
// hint came from brewCaskHint gets the `cask` verb, because `brew bundle` on a `brew` line
// naming a cask token fails looking for a formula that does not exist.
//
// A dep whose remedy is the PACK'S OWN installer contributes nothing, even when it has a
// package-manager Fallback: its remedy leads with that installer because the tool's own
// updater keeps it current (selfInstallFlavor), and a bundle listing the distro package
// would install the copy the per-line advice steers the user away from. It used to list the
// Fallback token. Nor is there a way to spell `curl … | sh` in a bundle file, which is a list
// of package-manager tokens.
//
// Nor does a hint that is a package PLUS A STEP (bundleToken). Each dep left out is one
// Unbundled returns, so a caller can print its command beside the bundle's rather than let
// the bundle read as the whole install.
func Manifest(results []Result) (filename, body string) {
	b := planBundle(results)
	if len(b.pkgs)+len(b.casks) == 0 {
		return "", ""
	}
	switch b.mgr {
	case "brew":
		var sb strings.Builder
		for _, p := range b.pkgs {
			sb.WriteString("brew \"" + p + "\"\n")
		}
		// Casks trail the formulae, matching `brew bundle dump`'s grouping.
		for _, c := range b.casks {
			sb.WriteString("cask \"" + c + "\"\n")
		}
		return "Brewfile", sb.String()
	default:
		// Non-brew managers have no cask concept, so a brew-cask hint cannot be selected
		// for them (hintKeys) — casks is empty here by construction.
		return b.mgr + "-packages.txt", strings.Join(b.pkgs, "\n") + "\n"
	}
}

// BundleInstall is the ONE command that installs the bundle Manifest renders for results,
// once it is written at path, or "" when Manifest renders none. It exists because the bundle
// used to be handed over with "install with the command for your manager", though this
// package had just picked the manager (docs/reference/happy-path-principle.md, rule 7).
//
// brew reads its own file format (`brew bundle --file`). The other managers take the file's
// package list as arguments to the very install line installCmd builds for one package; nix's
// file already holds installables (`nixpkgs#<pkg>`), so its line takes them as they are. The
// path is quoted for a shell, since a home directory may hold a space.
func BundleInstall(results []Result, path string) string {
	b := planBundle(results)
	if len(b.pkgs)+len(b.casks) == 0 {
		return ""
	}
	q := shquote.QuoteDisplay(path)
	switch b.mgr {
	case "brew":
		return "brew bundle --file=" + q
	case "nix":
		return "nix profile install $(cat " + q + ")"
	default:
		return installCmd(b.mgr, "$(cat "+q+")")
	}
}

// Unbundled returns the missing results that HAVE a remedy and are not in the bundle Manifest
// renders: a pack's own installer, with or without a manager fallback, or a hint that is a
// package plus a step. Their printed remedies are still the whole command; a caller naming
// BundleInstall's command names these beside it, or running the bundle would leave them missing.
func Unbundled(results []Result) []Result {
	return planBundle(results).left
}

// bundle is what Manifest, BundleInstall and Unbundled read, decided in ONE place so the file,
// the command that installs it and the list of what it leaves out cannot disagree.
type bundle struct {
	mgr         string
	pkgs, casks []string
	left        []Result
}

func planBundle(results []Result) bundle {
	var b bundle
	for _, r := range results {
		if r.Present || r.Remedy == "" {
			continue
		}
		remedy, flavor := r.Remedy, r.Flavor
		if flavor == selfInstallFlavor {
			// The tool's own installer stays the remedy, beside the bundle (Manifest says why).
			b.left = append(b.left, r)
			continue
		}
		if flavor == "" {
			flavor = r.Manager // Flavor is the manager for every hint but a cask (Result.Flavor)
		}
		token, ok := bundleToken(flavor, remedy)
		// ONE MANAGER PER BUNDLE: a user's order can resolve two binaries to two managers (claude
		// from brew, rg from apt), and a file holds one manager's tokens. The first manager met
		// owns the file, and every other manager's command is printed on its own (Unbundled).
		if !ok || (b.mgr != "" && r.Manager != b.mgr) {
			b.left = append(b.left, r)
			continue
		}
		b.mgr = r.Manager
		if flavor == brewCaskHint {
			b.casks = append(b.casks, token)
		} else {
			b.pkgs = append(b.pkgs, token)
		}
	}
	sort.Strings(b.pkgs)
	sort.Strings(b.casks)
	return b
}

// bundleToken is the token a bundle file lists for one remedy, recovered from the install
// command installCmd built for flavor, or ok=false when the remedy cannot be one line of a
// bundle: it is empty, it is not installCmd's (a pack's own installer), or the hint it was
// built from is more than one package token.
//
// That last case is a hint that is a package PLUS A STEP, written as `<package> && <command>`
// — the guardrails pack's apt hint for fd, because Debian's fd-find puts `fd` at
// /usr/lib/cargo/bin/fd, which no PATH holds, and the step links it onto one. The printed
// remedy is then the whole command and works when pasted. A bundle line could carry only the
// package, and installing it would leave the binary missing, so the dep is left out instead
// and Unbundled names it. Before this check the bundle took the remedy's LAST token, which
// for such a hint is the link's target path, listed as a package name.
func bundleToken(flavor, remedy string) (string, bool) {
	prefix := installCmd(flavor, "")
	hint, ok := strings.CutPrefix(remedy, prefix)
	if prefix == "" || !ok || hint == "" || strings.ContainsAny(hint, " \t\r\n") {
		return "", false
	}
	// The token is the last field of the one-package command: the package itself, or for
	// nix the installable `nixpkgs#<pkg>` that `nix profile install` takes.
	fields := strings.Fields(installCmd(flavor, hint))
	return fields[len(fields)-1], true
}
