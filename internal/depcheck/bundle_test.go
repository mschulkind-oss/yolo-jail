package depcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bundle_test.go pins the step after the bundle file: the ONE command that installs it.
// `yolo check-deps` used to write the file and say "install with the command for your
// manager", though this package had just picked the manager
// (docs/reference/happy-path-principle.md, rule 7). And it pins the case a bundle cannot
// hold: a hint that is a package plus a step, such as Debian's fd-find, whose `fd` is not
// on PATH until it is linked there.

// stubManagers writes a recording stub for every program a remedy or bundle command may
// start, and returns a PATH that finds them ahead of the system's own `cat`. A stub records
// its name and argv and runs NOTHING: `sudo` included, so no test can install a package
// or write outside its temp dir (AGENTS.md's rule against running install hints).
func stubManagers(t *testing.T) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(t.TempDir(), "calls")
	for _, name := range []string{"sudo", "apt", "dnf", "pacman", "brew", "nix", "ln"} {
		stub := "#!/bin/sh\nprintf '%s %s\\n' \"${0##*/}\" \"$*\" >> " + shellQuote(log) + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("no cat on PATH to expand the bundle with")
	}
	return dir + string(os.PathListSeparator) + filepath.Dir(cat), log
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// runShell runs a printed command line the way a user pastes it, with the stub PATH.
func runShell(t *testing.T, line, path, log string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cmd := exec.Command(sh, "-c", line)
	cmd.Env = []string{"PATH=" + path}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("`%s` failed: %v\n%s", line, err, out)
	}
	b, _ := os.ReadFile(log)
	return string(b)
}

// TestBundleInstallInstallsTheBundle writes each manager's bundle where `check-deps` would,
// under a home whose path has a space in it, and runs the command BundleInstall names for it
// through a shell. The package manager must receive exactly the bundle's packages. That is
// rule 4 for this hint: it is not enough that the command's text looks right.
func TestBundleInstallInstallsTheBundle(t *testing.T) {
	for _, tc := range []struct {
		mgr   string
		hints []map[string]string
		// want is the one call the manager's stub must record (the file path for brew).
		want string
	}{
		{"apt", []map[string]string{{"apt": "pkg-a"}, {"apt": "pkg-b"}}, "sudo apt install -y pkg-a pkg-b"},
		{"dnf", []map[string]string{{"dnf": "pkg-a"}, {"dnf": "pkg-b"}}, "sudo dnf install -y pkg-a pkg-b"},
		{"pacman", []map[string]string{{"pacman": "pkg-a"}, {"pacman": "pkg-b"}},
			"sudo pacman -S --noconfirm pkg-a pkg-b"},
		{"nix", []map[string]string{{"nix": "pkg-a"}, {"nix": "pkg-b"}},
			"nix profile install nixpkgs#pkg-a nixpkgs#pkg-b"},
		{"brew", []map[string]string{{"brew": "pkg-a"}, {"brew-cask": "pkg-b"}}, "brew bundle --file="},
	} {
		t.Run(tc.mgr, func(t *testing.T) {
			var reqs []Requirement
			for i, h := range tc.hints {
				reqs = append(reqs, Requirement{Bin: "bin" + string(rune('a'+i)), Hints: h})
			}
			results := Check(reqs, only(tc.mgr))
			name, body := Manifest(results)
			if name == "" {
				t.Fatalf("no bundle for %s", tc.mgr)
			}
			p := filepath.Join(t.TempDir(), "home dir", ".config", "yolo", name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			line := BundleInstall(results, p)
			if line == "" {
				t.Fatalf("BundleInstall named no command for a %s bundle", tc.mgr)
			}
			path, log := stubManagers(t)
			got := runShell(t, line, path, log)
			if !strings.Contains(got, tc.want) {
				t.Errorf("`%s` ran %q, want a call %q", line, got, tc.want)
			}
			if tc.mgr == "brew" && !strings.Contains(got, p) {
				t.Errorf("`%s` did not hand brew the bundle at %s: %q", line, p, got)
			}
		})
	}
}

// TestBundleInstallIsEmptyWithNoBundle: no file, so no command to name.
func TestBundleInstallIsEmptyWithNoBundle(t *testing.T) {
	if got := BundleInstall([]Result{{Bin: "have", Present: true, Manager: "apt"}}, "/x"); got != "" {
		t.Errorf("BundleInstall with nothing missing = %q, want \"\"", got)
	}
}

// debianFd is the shape of the guardrails pack's apt hint for fd: the package, then the step
// that puts the `fd` it installs on PATH (Debian installs it as /usr/lib/cargo/bin/fd, with
// /usr/bin/fdfind beside it, because /usr/bin/fd is fdclone's).
const debianFd = "fd-find && sudo ln -sf /usr/lib/cargo/bin/fd /usr/local/bin/fd"

// TestAHintWithAStepIsPrintedWholeAndLeftOutOfTheBundle: the printed remedy is the whole
// command, so pasting it leaves the binary on PATH; the bundle file, which is a list of
// package tokens, leaves the dep out rather than listing a token that installs the package
// and not the binary; and Unbundled names it, so the caller can print its command next to
// the bundle's.
func TestAHintWithAStepIsPrintedWholeAndLeftOutOfTheBundle(t *testing.T) {
	results := Check([]Requirement{
		{Bin: "fd", Hints: map[string]string{"apt": debianFd}},
		{Bin: "rg", Hints: map[string]string{"apt": "ripgrep"}},
	}, only("apt"))
	byBin := map[string]Result{}
	for _, r := range results {
		byBin[r.Bin] = r
	}
	if got, want := byBin["fd"].Remedy, "sudo apt install -y "+debianFd; got != want {
		t.Errorf("fd remedy = %q, want %q", got, want)
	}
	name, body := Manifest(results)
	if name != "apt-packages.txt" || body != "ripgrep\n" {
		t.Errorf("bundle = %q %q, want apt-packages.txt holding ripgrep alone", name, body)
	}
	left := Unbundled(results)
	if len(left) != 1 || left[0].Bin != "fd" {
		t.Errorf("Unbundled = %+v, want fd alone", left)
	}

	// And the printed remedy, pasted, runs both halves in order.
	path, log := stubManagers(t)
	got := runShell(t, byBin["fd"].Remedy, path, log)
	if want := "sudo apt install -y fd-find\nsudo ln -sf /usr/lib/cargo/bin/fd /usr/local/bin/fd\n"; got != want {
		t.Errorf("the fd remedy ran %q, want %q", got, want)
	}
}

// TestUnbundledNamesASelfInstallWithNoFallback: a pack's own installer has never been
// bundleable, and Unbundled says so; one with a manager fallback is in the bundle through it.
func TestUnbundledNamesASelfInstallWithNoFallback(t *testing.T) {
	results := Check([]Requirement{
		{Bin: "solo", SelfInstall: "npm install -g solo-pkg"},
		{Bin: "both", SelfInstall: "npm install -g both-pkg", Hints: map[string]string{"apt": "both-apt"}},
	}, only("apt"))
	left := Unbundled(results)
	if len(left) != 1 || left[0].Bin != "solo" {
		t.Errorf("Unbundled = %+v, want solo alone", left)
	}
	if _, body := Manifest(results); body != "both-apt\n" {
		t.Errorf("bundle body = %q, want both-apt through its fallback", body)
	}
}
