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
	// whenever Remedy already IS the manager's command. FallbackFlavor is its hint key,
	// carried rather than re-derived because Manifest needs the brew formula/cask verb and
	// recovering that from a command string would be a second, guessing implementation.
	Fallback       string
	FallbackFlavor string
	// NoTerminal is whether Remedy must run with no controlling terminal and a /dev/null stdin:
	// it is the pack's own installer script (Requirement.SelfInstallNoTerminal). False for a
	// package-manager remedy, and for a binary with no remedy.
	NoTerminal bool
	// Unpublished is Requirement.Unpublished, carried onto a binary that is ABSENT, and ""
	// for a present one. Such a result is not missing (Missing leaves it out) and has no
	// remedy: the reason is the whole of what a report says about it.
	Unpublished string
}

// Lookup resolves a binary on one PATH, exec.LookPath's shape. Every probe here takes it from
// its CALLER (docs/design/host-launch-environment.md §3): at the host that is the launch PATH's
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
// REMEDY PRECEDENCE: the declaring pack's OWN installer first, then the detected package
// manager's hint. See selfInstallFlavor for why that order — in short, a tool with a
// first-party installer has a first-party updater, and a distro package silently pins it to
// whatever that repo has. When both exist the manager's command is kept as Fallback rather
// than discarded, so a user who prefers their package manager still sees the token.
func Check(reqs []Requirement, look Lookup) []Result {
	look = lookupOrDefault(look)
	mgr := DetectManager(look)
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
		case r.SelfInstall != "":
			res.Remedy, res.Flavor = r.SelfInstall, selfInstallFlavor
			res.NoTerminal = r.SelfInstallNoTerminal
			if pkg, flavor, ok := hintFor(r.Hints, mgr); ok {
				res.Fallback, res.FallbackFlavor = installCmd(flavor, pkg), flavor
			}
		default:
			if pkg, flavor, ok := hintFor(r.Hints, mgr); ok {
				res.Remedy, res.Flavor = installCmd(flavor, pkg), flavor
			}
		}
		out = append(out, res)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bin < out[j].Bin })
	return out
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
		return "nix profile install nixpkgs#" + pkg
	default:
		return ""
	}
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
// A dep whose remedy is the PACK'S OWN installer contributes its Fallback token if it has
// one, and otherwise nothing: a bundle file is a list of package-manager tokens, and there
// is no way to spell `curl … | sh` in one. Splicing the URL in as a token would produce a
// Brewfile that fails on a line the user cannot fix; the printed remedy already names the
// command, so nothing is lost by leaving it out of the bundle.
func Manifest(results []Result) (filename, body string) {
	mgr := ""
	var pkgs, casks []string
	for _, r := range results {
		if r.Present {
			continue
		}
		remedy, flavor := r.Remedy, r.Flavor
		if flavor == selfInstallFlavor {
			// Only the manager fallback can go in a bundle. No fallback → no line.
			if r.Fallback == "" {
				continue
			}
			remedy, flavor = r.Fallback, r.FallbackFlavor
		}
		if remedy == "" {
			continue
		}
		mgr = r.Manager
		// The package name is the last token of the remedy for brew/nix; for apt/dnf/
		// pacman it is also the trailing token. Recover it rather than re-plumbing.
		fields := strings.Fields(remedy)
		if flavor == brewCaskHint {
			casks = append(casks, fields[len(fields)-1])
		} else {
			pkgs = append(pkgs, fields[len(fields)-1])
		}
	}
	if len(pkgs)+len(casks) == 0 {
		return "", ""
	}
	sort.Strings(pkgs)
	sort.Strings(casks)
	switch mgr {
	case "brew":
		var b strings.Builder
		for _, p := range pkgs {
			b.WriteString("brew \"" + p + "\"\n")
		}
		// Casks trail the formulae, matching `brew bundle dump`'s grouping.
		for _, c := range casks {
			b.WriteString("cask \"" + c + "\"\n")
		}
		return "Brewfile", b.String()
	default:
		// Non-brew managers have no cask concept, so a brew-cask hint cannot be selected
		// for them (hintKeys) — casks is empty here by construction.
		return mgr + "-packages.txt", strings.Join(pkgs, "\n") + "\n"
	}
}
