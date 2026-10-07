package main

// judge.go is the half of tap-install-check that decides. Every function here takes
// what the machine answered and returns a verdict, so the rules are unit-tested on
// Linux against fixtures and against the real `yolo check` report, while main.go only
// runs the commands that produce the inputs.

import (
	"debug/elf"
	"debug/macho"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// releaseVersion is the shape `just release` accepts (X.Y.Z or X.Y.Z-pre), with the
// tag's leading "v" already removed. An expectation that does not fit is refused
// rather than compared, so a trigger that hands over a branch name instead of a tag
// fails naming the value rather than as a confusing version mismatch.
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
var mainReleaseTitle = regexp.MustCompile(`^Release v([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?) @ ([0-9a-f]{40}) / request ([1-9][0-9]{0,19})$`)
var mainHomebrewTitle = regexp.MustCompile(`^Homebrew-only v([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)$`)

// normalizeExpect turns a tag or version ("v0.11.0", "0.11.0") into the version the
// formula carries, or "" for no expectation.
func normalizeExpect(raw string) (string, error) {
	v := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if v == "" {
		return "", nil
	}
	if !releaseVersion.MatchString(v) {
		return "", fmt.Errorf("%q is not a release version (want X.Y.Z or X.Y.Z-pre, with or without a leading v)", raw)
	}
	return v, nil
}

// expectFromGitHubEvent reads the version a run is meant to verify out of the
// triggering event, so the workflow passes no expression into a shell.
//
//   - workflow_run: the successful Release run that finished. A tag push or
//     legacy tag-scoped dispatch carries its version in head_branch. The new
//     trusted-main Release dispatch carries it in the anchored display_title;
//     a Homebrew-only backfill has its own explicit title and no expectation.
//     A main ref without one of those exact titles is refused, never treated as
//     a versionless normal release.
//   - workflow_dispatch: the optional `version` input.
//   - anything else (the weekly schedule): no expectation; the tap is checked
//     against itself.
func expectFromGitHubEvent(eventName string, payload []byte) (string, error) {
	switch eventName {
	case "workflow_run":
		var ev struct {
			WorkflowRun *struct {
				Event        string `json:"event"`
				HeadBranch   string `json:"head_branch"`
				Conclusion   string `json:"conclusion"`
				Name         string `json:"name"`
				DisplayTitle string `json:"display_title"`
			} `json:"workflow_run"`
		}
		if err := json.Unmarshal(payload, &ev); err != nil {
			return "", fmt.Errorf("reading the workflow_run payload: %w", err)
		}
		if ev.WorkflowRun == nil {
			return "", errors.New("the workflow_run payload has no workflow_run object")
		}
		run := ev.WorkflowRun
		if run.Conclusion != "success" {
			return "", fmt.Errorf("the triggering %q run concluded %q; only a successful release has a formula to verify", run.Name, run.Conclusion)
		}
		if run.Event == "workflow_dispatch" {
			if strings.HasPrefix(run.HeadBranch, "v") {
				return normalizeRunTag(run.Name, run.HeadBranch)
			}
			if run.HeadBranch != "main" {
				return "", fmt.Errorf("the triggering %q dispatch ran on unexpected ref %q", run.Name, run.HeadBranch)
			}
			if match := mainReleaseTitle.FindStringSubmatch(run.DisplayTitle); match != nil {
				return normalizeRunTag(run.Name, "v"+match[1])
			}
			if match := mainHomebrewTitle.FindStringSubmatch(run.DisplayTitle); match != nil {
				return "", nil
			}
			return "", fmt.Errorf("the main-scoped %q dispatch has missing or malformed display_title %q; normal releases must carry their exact version", run.Name, run.DisplayTitle)
		}
		if run.Event != "push" {
			return "", fmt.Errorf("the triggering %q run came from unsupported event %q", run.Name, run.Event)
		}
		if run.HeadBranch == "" {
			return "", fmt.Errorf("the triggering %q run was a push but names no tag, so there is no release to hold the tap to", run.Name)
		}
		return normalizeRunTag(run.Name, run.HeadBranch)
	case "workflow_dispatch":
		var ev struct {
			Inputs map[string]any `json:"inputs"`
		}
		if err := json.Unmarshal(payload, &ev); err != nil {
			return "", fmt.Errorf("reading the workflow_dispatch payload: %w", err)
		}
		s, _ := ev.Inputs["version"].(string)
		return normalizeExpect(s)
	default:
		return "", nil
	}
}

func normalizeRunTag(runName, raw string) (string, error) {
	version, err := normalizeExpect(raw)
	if err != nil {
		return "", fmt.Errorf("the triggering %q run's release ref: %w", runName, err)
	}
	if version == "" {
		return "", fmt.Errorf("the triggering %q run's release ref is empty", runName)
	}
	return version, nil
}

// brewFormula is the part of `brew info --json=v2 <formula>` this tool reads.
type brewFormula struct {
	FullName string `json:"full_name"`
	Versions struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	Revision  int     `json:"revision"`
	LinkedKeg *string `json:"linked_keg"`
}

// tapFacts is what the tap's formula says, checked for internal consistency.
type tapFacts struct {
	// Version is the formula's stable version: what the formula stamps into
	// `yolo --version` (release.yml passes #{version} to -ldflags).
	Version string
	// Keg is the linked keg's directory name — Version, plus `_<revision>` when the
	// formula carries a revision. It names the Cellar directory.
	Keg string
}

// parseBrewInfo reads `brew info --json=v2 <formula>` and requires that the
// formula asked for is the one answered, that it is installed and linked, and that
// the linked keg is the formula's current version — so every later assertion is
// about the install the tap describes now, not a leftover.
func parseBrewInfo(data []byte, formula string) (tapFacts, error) {
	var doc struct {
		Formulae []brewFormula `json:"formulae"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return tapFacts{}, fmt.Errorf("brew info --json=v2 did not parse: %w", err)
	}
	if len(doc.Formulae) != 1 {
		return tapFacts{}, fmt.Errorf("brew info answered %d formulae for %s, want exactly 1", len(doc.Formulae), formula)
	}
	f := doc.Formulae[0]
	if f.FullName != formula {
		return tapFacts{}, fmt.Errorf("brew resolved %s to %q, a different formula", formula, f.FullName)
	}
	if f.Versions.Stable == "" {
		return tapFacts{}, fmt.Errorf("%s has no stable version", formula)
	}
	keg := f.Versions.Stable
	if f.Revision > 0 {
		keg += "_" + strconv.Itoa(f.Revision)
	}
	if f.LinkedKeg == nil || *f.LinkedKeg == "" {
		return tapFacts{}, fmt.Errorf("%s %s is not installed and linked (linked_keg is empty)", formula, keg)
	}
	if *f.LinkedKeg != keg {
		return tapFacts{}, fmt.Errorf("%s: the linked keg is %s but the formula is at %s", formula, *f.LinkedKeg, keg)
	}
	return tapFacts{Version: f.Versions.Stable, Keg: keg}, nil
}

// versionLinePrefix is what `yolo --version` prints before the version, and what the
// formula's own test block asserts (release.yml: `assert_match "yolo-jail #{version}"`).
const versionLinePrefix = "yolo-jail "

// checkVersionLine requires `yolo --version` to print exactly one line naming want.
// Exact, not a substring: a from-source describe such as 0.11.0+3.gabc1234 would
// contain the version and still be a different binary.
func checkVersionLine(stdout, want string) error {
	got := strings.TrimSpace(stdout)
	if got != versionLinePrefix+want {
		return fmt.Errorf("`yolo --version` printed %q, want %q", got, versionLinePrefix+want)
	}
	return nil
}

// checkReport is the part of `yolo check --format json` this tool reads — the
// document internal/cli/check/jsonreport.go defines. The pin test decodes the real
// Check's output through this type, so a renamed field fails there, not here.
type checkReport struct {
	Version  string `json:"version"`
	Failed   int    `json:"failed"`
	Findings []struct {
		Section string `json:"section"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"findings"`
}

// flakeFinding is the graded line `yolo check` prints once it has resolved the flake
// a launch would build from (internal/cli/check/check.go), and besideBinary the phrase
// reporoot.FromBesideBinary.Describe() renders for a bundle shipped next to the
// executable — the Homebrew layout. Both are pinned against the real code by
// TestTheJudgeAcceptsWhatCheckReportsOnABareMac, for this tree, and by
// TestTheJudgeAcceptsWhatTheLastReleaseReportsOnABareMac, for the release the tap
// carries.
//
// ⚠ THIS JOB RUNS THE PUBLISHED VERSION, NOT THIS TREE. If the wording changes here,
// keep accepting the old wording until no version the tap can still carry prints it.
// The second pin is what fails if the old wording is dropped too soon.
var flakeFinding = regexp.MustCompile(`^flake\.nix found: (.+) \(via (.+)\)$`)

const besideBinary = "flake bundle beside the binary"

// expectedFailure names a FAIL a machine with no container runtime and no Nix is
// expected to report. The runner has neither on purpose: this job proves the
// install, not a launch. An empty prefix accepts any message in the section.
type expectedFailure struct{ section, messagePrefix string }

// bareMacFailures is every FAIL `yolo check` may report on a stock macOS runner. A
// FAIL anywhere else is a finding about the installed binary on a fresh Mac — its
// storage, its config handling, its flake resolution — and fails the job.
//
// The same rule as flakeFinding's: when this tree rewords or downgrades one of these
// FAILs, the entry stays while the release the tap carries still prints it, and goes
// once neither prints it. release_test.go enforces both halves.
var bareMacFailures = []expectedFailure{
	{"Container Runtime", ""},        // no podman, no Apple Container
	{"Nix", ""},                      // no nix on PATH
	{"macOS Platform", "Nix store:"}, // and so no /nix volume
}

func expectedOnABareMac(section, message string) bool {
	for _, e := range bareMacFailures {
		if section == e.section && strings.HasPrefix(message, e.messagePrefix) {
			return true
		}
	}
	return false
}

// judgeCheck reads one `yolo check --no-build --format json` run and returns the
// directory of the flake bundle it resolved.
//
// It asserts on the document, never on the exit code alone: on a runner with no
// container runtime the command exits 1 by design, so the exit code says only that
// SOMETHING failed. What must hold is that the report parsed, that it agrees with the
// exit code, that it names the installed version, that every FAIL is one a bare Mac
// is expected to report, and that the flake it found is the bundle beside the binary
// and inside this install's keg.
func judgeCheck(stdout []byte, rc int, wantVersion, kegDir string) (string, []error) {
	var rep checkReport
	if err := json.Unmarshal(stdout, &rep); err != nil {
		return "", []error{fmt.Errorf("`yolo check --format json` (exit %d) did not print a JSON report: %w", rc, err)}
	}
	var errs []error
	switch {
	case rc != 0 && rc != 1:
		errs = append(errs, fmt.Errorf("`yolo check` exited %d; it exits 0 or 1, so this is a crash or a usage error", rc))
	case (rc == 1) != (rep.Failed > 0):
		errs = append(errs, fmt.Errorf("`yolo check` exited %d with %d failed findings; the exit code and the report disagree", rc, rep.Failed))
	}
	if rep.Version != wantVersion {
		errs = append(errs, fmt.Errorf("`yolo check` reports version %q, want %q", rep.Version, wantVersion))
	}

	var flakePath, via string
	flakeLines := 0
	for _, f := range rep.Findings {
		if f.Status == "fail" && !expectedOnABareMac(f.Section, f.Message) {
			errs = append(errs, fmt.Errorf("unexpected FAIL in %q: %s", f.Section, f.Message))
		}
		if m := flakeFinding.FindStringSubmatch(f.Message); m != nil && f.Status == "pass" {
			flakeLines++
			flakePath, via = m[1], m[2]
		}
	}
	switch {
	case flakeLines == 0:
		return "", append(errs, errors.New("`yolo check` graded no \"flake.nix found\" line: the installed binary did not find its flake bundle"))
	case flakeLines > 1:
		return "", append(errs, fmt.Errorf("`yolo check` graded %d \"flake.nix found\" lines, want exactly 1", flakeLines))
	}
	if via != besideBinary {
		errs = append(errs, fmt.Errorf("the flake was selected via %q, want %q: this install's own bundle was not the one found", via, besideBinary))
	}
	if filepath.Base(flakePath) != "flake.nix" {
		return "", append(errs, fmt.Errorf("the flake finding names %q, which is not a flake.nix", flakePath))
	}
	bundle := filepath.Dir(flakePath)
	if err := within(bundle, kegDir); err != nil {
		errs = append(errs, fmt.Errorf("the flake bundle is not this install's: %w", err))
	}
	return bundle, errs
}

// within requires path to resolve inside dir. Both sides are resolved, because
// Homebrew reaches a keg through symlinks (bin/yolo, opt/, share/) and so does the
// macOS temp dir a test builds its fixture in.
func within(path, dir string) error {
	rp, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rd, rp)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s resolves to %s, outside %s", path, rp, rd)
	}
	return nil
}

// requiredPrefixBinaries are the files a launch cannot start a jail without: the
// container's pid1 for each arch a Mac can run a Linux VM on
// (internal/cli/run/jailprefix.go names it in the container argv).
var requiredPrefixBinaries = []string{
	"bin/linux-amd64/yolo-entrypoint",
	"bin/linux-arm64/yolo-entrypoint",
}

// platformDirs maps each bin/<os>-<arch> directory scripts/stage-source-bundle.sh
// writes to the executable format its files must be. A binary of the wrong format
// in one of them is a jail that fails at exec, which nothing short of a launch on
// that arch would otherwise show.
var platformDirs = map[string]func(string) error{
	"linux-amd64":  elfOf(elf.EM_X86_64),
	"linux-arm64":  elfOf(elf.EM_AARCH64),
	"darwin-amd64": machoOf(macho.CpuAmd64),
	"darwin-arm64": machoOf(macho.CpuArm64),
}

// checkBundle requires the flake bundle beside the binary to be what
// scripts/stage-source-bundle.sh stages: flake.nix, flake.lock, and prebuilt
// executables of the right format under bin/<os>-<arch>/, the Linux pid1 for both
// arches among them. Returns every problem and the directories it did not recognize,
// which the caller reports rather than counting as checked.
func checkBundle(dir string) (unknown []string, errs []error) {
	for _, f := range []string{"flake.nix", "flake.lock"} {
		if info, err := os.Stat(filepath.Join(dir, f)); err != nil || !info.Mode().IsRegular() {
			errs = append(errs, fmt.Errorf("the bundle has no %s: %s", f, filepath.Join(dir, f)))
		}
	}
	for _, rel := range requiredPrefixBinaries {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			errs = append(errs, fmt.Errorf("the bundle has no %s", rel))
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "bin"))
	if err != nil {
		return nil, append(errs, fmt.Errorf("the bundle has no bin/ directory: %w", err))
	}
	for _, e := range entries {
		want, ok := platformDirs[e.Name()]
		if !ok || !e.IsDir() {
			unknown = append(unknown, "bin/"+e.Name())
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, "bin", e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, f := range files {
			p := filepath.Join(dir, "bin", e.Name(), f.Name())
			info, err := os.Stat(p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
				errs = append(errs, fmt.Errorf("bin/%s/%s is not an executable file (%s)", e.Name(), f.Name(), info.Mode()))
				continue
			}
			if err := want(p); err != nil {
				errs = append(errs, fmt.Errorf("bin/%s/%s: %w", e.Name(), f.Name(), err))
			}
		}
	}
	sort.Strings(unknown)
	return unknown, errs
}

func elfOf(machine elf.Machine) func(string) error {
	return func(p string) error {
		f, err := elf.Open(p)
		if err != nil {
			return fmt.Errorf("not an ELF executable: %w", err)
		}
		defer f.Close()
		if f.Class != elf.ELFCLASS64 || f.Machine != machine {
			return fmt.Errorf("is %v %v, want ELFCLASS64 %v", f.Class, f.Machine, machine)
		}
		return nil
	}
}

func machoOf(cpu macho.Cpu) func(string) error {
	return func(p string) error {
		f, err := macho.Open(p)
		if err != nil {
			return fmt.Errorf("not a Mach-O executable: %w", err)
		}
		defer f.Close()
		if f.Cpu != cpu {
			return fmt.Errorf("is %v, want %v", f.Cpu, cpu)
		}
		return nil
	}
}
