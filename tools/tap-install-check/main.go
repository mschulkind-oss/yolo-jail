// Command tap-install-check verifies a Homebrew install of yolo from the tap, on
// the machine it runs on, with no container runtime and no agent.
//
// It is the second half of .github/workflows/tap-install.yml. That workflow runs the
// user guide's `brew install` and the formula's own `brew test` as plain steps, so a
// reader sees the user's commands verbatim; this program then asks the installed
// binary what a user on a fresh Mac would learn from it:
//
//  1. the tap's formula is installed and linked, at the version the release named
//     (when the run knows one — see expectFromGitHubEvent);
//  2. the `yolo` on PATH is that keg's binary, and `yolo --version` names the version;
//  3. `yolo check --no-build --format json`, run in an empty directory, finds the
//     flake bundle beside the binary and inside the keg, and fails only where a
//     machine with no container runtime and no Nix is expected to (judge.go);
//  4. that bundle holds a flake.nix, a flake.lock and prebuilt executables of the
//     right format for every bin/<os>-<arch>/ directory it ships.
//
// The rules live in judge.go, which takes what the machine answered and returns a
// verdict, so they are tested on Linux. This file only runs the commands.
//
// Usage:
//
//	go run ./tools/tap-install-check -formula mschulkind-oss/tap/yolo-jail [-expect-version 0.11.0]
//	go run ./tools/tap-install-check -formula mschulkind-oss/tap/yolo-jail -expect-from-github-event
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tap-install-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	formula := fs.String("formula", "", "the tap-qualified formula to verify, e.g. mschulkind-oss/tap/yolo-jail (required)")
	expect := fs.String("expect-version", "", "the version the tap must carry (a leading v is accepted); empty checks the tap against itself")
	fromEvent := fs.Bool("expect-from-github-event", false, "read the expected version from GITHUB_EVENT_NAME and GITHUB_EVENT_PATH instead")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *formula == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: tap-install-check -formula <tap/formula> [-expect-version X.Y.Z | -expect-from-github-event]")
		return 2
	}
	if *fromEvent && *expect != "" {
		fmt.Fprintln(stderr, "tap-install-check: -expect-version and -expect-from-github-event are exclusive")
		return 2
	}

	want, err := normalizeExpect(*expect)
	if *fromEvent {
		want, err = expectFromEnv()
	}
	if err != nil {
		fmt.Fprintln(stderr, "tap-install-check:", err)
		return 2
	}

	r := &report{out: stdout, annotate: os.Getenv("GITHUB_ACTIONS") == "true"}
	r.verify(*formula, want)
	return r.finish()
}

// expectFromEnv is expectFromGitHubEvent over the runner's own event.
func expectFromEnv() (string, error) {
	name := os.Getenv("GITHUB_EVENT_NAME")
	path := os.Getenv("GITHUB_EVENT_PATH")
	if name == "" || path == "" {
		return "", errors.New("-expect-from-github-event needs GITHUB_EVENT_NAME and GITHUB_EVENT_PATH, which only a GitHub Actions run sets")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the event payload: %w", err)
	}
	return expectFromGitHubEvent(name, payload)
}

// report prints each result as it is reached and remembers the failures, so one run
// names every problem instead of stopping at the first.
type report struct {
	out      io.Writer
	annotate bool
	failures int
}

func (r *report) ok(format string, a ...any) {
	fmt.Fprintf(r.out, "ok    "+format+"\n", a...)
}

func (r *report) note(format string, a ...any) {
	fmt.Fprintf(r.out, "note  "+format+"\n", a...)
}

func (r *report) fail(err error) {
	r.failures++
	fmt.Fprintf(r.out, "FAIL  %v\n", err)
	if r.annotate {
		// One line per failure in the run's summary, not only in the step log.
		fmt.Fprintf(r.out, "::error title=tap install::%s\n", strings.ReplaceAll(err.Error(), "\n", " "))
	}
}

func (r *report) finish() int {
	if r.failures > 0 {
		fmt.Fprintf(r.out, "\n%d check(s) failed.\n", r.failures)
		return 1
	}
	fmt.Fprintln(r.out, "\nThe tap install works.")
	return 0
}

func (r *report) verify(formula, want string) {
	info, _, err := capture(2*time.Minute, "", nil, "brew", "info", "--json=v2", formula)
	if err != nil {
		r.fail(err)
		return
	}
	tap, err := parseBrewInfo(info, formula)
	if err != nil {
		r.fail(err)
		return
	}
	r.ok("%s is installed and linked at %s", formula, tap.Keg)
	if want != "" {
		if tap.Version != want {
			r.fail(fmt.Errorf("the tap carries %s %s, but this run verifies release %s: the formula push did not land", formula, tap.Version, want))
		} else {
			r.ok("the tap carries release %s", want)
		}
	} else {
		r.note("no release named for this run; checking the tap's %s against itself", tap.Version)
	}

	prefix, _, err := capture(time.Minute, "", nil, "brew", "--prefix", formula)
	if err != nil {
		r.fail(err)
		return
	}
	keg, err := filepath.EvalSymlinks(strings.TrimSpace(string(prefix)))
	if err != nil {
		r.fail(fmt.Errorf("resolving the keg behind `brew --prefix %s`: %w", formula, err))
		return
	}
	if filepath.Base(keg) != tap.Keg {
		r.fail(fmt.Errorf("`brew --prefix %s` resolves to %s, not the linked keg %s", formula, keg, tap.Keg))
	}

	// The yolo a user types: found on PATH, run as found, the way a shell runs it.
	yolo, err := exec.LookPath("yolo")
	if err != nil {
		r.fail(fmt.Errorf("no yolo on PATH after the install: %w", err))
		return
	}
	if err := within(yolo, filepath.Join(keg, "bin")); err != nil {
		r.fail(fmt.Errorf("the yolo on PATH is not this install's: %w", err))
	} else {
		r.ok("yolo on PATH is %s, this keg's binary", yolo)
	}

	ver, _, err := capture(time.Minute, "", cleanEnv(), yolo, "--version")
	if err != nil {
		r.fail(err)
	} else if err := checkVersionLine(string(ver), tap.Version); err != nil {
		r.fail(err)
	} else {
		r.ok("`yolo --version` prints %q", strings.TrimSpace(string(ver)))
	}

	r.verifyCheck(yolo, tap.Version, keg)
}

// verifyCheck runs `yolo check --no-build --format json` in an empty directory —
// never the checkout, whose yolo-jail.jsonc is this repository's own development
// config, written for the tree and not for the published binary under test.
func (r *report) verifyCheck(yolo, version, keg string) {
	ws, err := os.MkdirTemp("", "tap-install-check-workspace-")
	if err != nil {
		r.fail(err)
		return
	}
	defer os.RemoveAll(ws)

	out, errOut, rc, err := captureRC(5*time.Minute, ws, cleanEnv(), yolo, "check", "--no-build", "--format", "json")
	if err != nil {
		r.fail(err)
		return
	}
	if len(bytes.TrimSpace(errOut)) > 0 {
		fmt.Fprintf(r.out, "---- yolo check stderr ----\n%s\n---------------------------\n", bytes.TrimRight(errOut, "\n"))
	}
	printFindings(r.out, out)

	bundle, errs := judgeCheck(out, rc, version, keg)
	for _, e := range errs {
		r.fail(e)
	}
	if bundle == "" {
		return
	}
	if len(errs) == 0 {
		r.ok("`yolo check` (exit %d) found the flake bundle beside the binary, at %s, and failed only where a Mac with no container runtime and no Nix does", rc, bundle)
	}

	unknown, errs := checkBundle(bundle)
	for _, e := range errs {
		r.fail(e)
	}
	for _, u := range unknown {
		r.note("%s is not a platform directory this check knows; its contents were NOT checked", u)
	}
	if len(errs) == 0 {
		r.ok("the bundle holds flake.nix, flake.lock and prebuilt executables of the right format")
	}
}

// printFindings writes the report's graded lines to the log, so a reader of a red run
// sees what a fresh Mac is told without re-running anything. Silent on a document
// that does not parse: judgeCheck reports that.
func printFindings(w io.Writer, out []byte) {
	var rep checkReport
	if err := json.Unmarshal(out, &rep); err != nil {
		return
	}
	fmt.Fprintln(w, "---- yolo check findings ----")
	for _, f := range rep.Findings {
		mark := ""
		if f.Status == "fail" && expectedOnABareMac(f.Section, f.Message) {
			mark = "  (expected: this runner has no container runtime and no Nix)"
		}
		fmt.Fprintf(w, "  [%s] %s: %s%s\n", strings.ToUpper(f.Status), f.Section, f.Message, mark)
	}
	fmt.Fprintln(w, "-----------------------------")
}

// cleanEnv is the process environment without any YOLO_* variable, so nothing the
// runner (or a developer's shell) carries can point the binary at another flake
// (YOLO_REPO_ROOT) or another runtime (YOLO_RUNTIME) than a fresh install's.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "YOLO_") {
			env = append(env, kv)
		}
	}
	return env
}

// capture runs argv and returns its stdout, treating any non-zero exit as an error.
func capture(timeout time.Duration, dir string, env []string, argv ...string) ([]byte, []byte, error) {
	out, errOut, rc, err := captureRC(timeout, dir, env, argv...)
	if err == nil && rc != 0 {
		err = fmt.Errorf("`%s` exited %d: %s", strings.Join(argv, " "), rc, strings.TrimSpace(string(errOut)))
	}
	return out, errOut, err
}

// captureRC runs argv and returns its output and exit code. err is only for a
// command that could not be run or did not finish.
func captureRC(timeout time.Duration, dir string, env []string, argv ...string) ([]byte, []byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, nil, 0, fmt.Errorf("`%s` did not finish within %s", strings.Join(argv, " "), timeout)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.Bytes(), errOut.Bytes(), exit.ExitCode(), nil
	}
	if err != nil {
		return nil, nil, 0, fmt.Errorf("running `%s`: %w", strings.Join(argv, " "), err)
	}
	return out.Bytes(), errOut.Bytes(), 0, nil
}
