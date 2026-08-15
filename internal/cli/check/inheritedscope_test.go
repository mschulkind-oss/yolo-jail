package check

// inheritedscope_test.go is OQ-LP9 R9's "one integration case per false-error class this
// exists to kill". It drives the WHOLE `yolo check` command over an in-jail user config and
// asserts what the user SEES — not what a filter returned.
//
// Why at the command level rather than only over FilterInherit: the false errors were never
// produced by the filter, they were produced by check's own sections evaluating a host
// referent. A unit test over the census can only prove a key is absent from a map; only
// running the command proves the section that used to complain has nothing left to complain
// about.
//
// WHAT MEASUREMENT SHOWED, and it corrects the design doc's framing (recorded rather than
// quietly worked around). OQ-LP9 names `cache_relocations` as THE false-error class. It is
// the WRONG EXAMPLE for an in-jail `yolo check`: measured 2026-08-14, both
// `cache_relocations` and `host_files` are already silent in-jail, because each was
// hand-patched with a `!inJail()` argument threaded into its shape checker
// (validate.go:1044, hostfiles.go:913) after exactly this bug was reported. What is STILL
// live in-jail — measured with a real in-jail `yolo check --no-build` over a host config — is:
//
//	gpu          FOUR fails: nvidia-smi / nvidia-ctk / runc "not found", "No CDI spec"
//	mounts       one WARN per entry: "host path does not exist and will be skipped"
//	env_sources  "env_sources file not found, skipping: ~/.acme.env"
//
// So the class is real and worse than one key; what the doc got wrong is which key
// illustrates it. That strengthens the ruling's own argument: the two keys anyone had
// noticed were patched one at a time, and the three nobody noticed stayed broken. A census
// fixes the pattern; a guard fixes an incident.
//
// The tests below therefore cover the LIVE classes, and keep cache_relocations as a
// regression on the property the design actually asks for (absence, not a guard).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// writeUserConfig puts a config VERBATIM at the in-jail user-scope path for a scratch HOME.
// Used by the CONTROLS, which need the unfiltered host key present.
func writeUserConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeInheritedUserConfig runs a HOST config through the real OQ-LP9 filter and writes the
// result where an in-jail reader finds it — i.e. it reproduces what a launch delivers.
//
// This is what makes the silence tests an INTEGRATION case rather than two disconnected
// halves, and it was a real gap: the first draft hand-wrote the post-filter config, so
// mutating FilterInherit to pass everything through left the check tests green. Driving the
// filter here joins them — the fixture is a host config with the offending key PRESENT, and
// only the filter's own behaviour makes it absent in the jail.
func writeInheritedUserConfig(t *testing.T, home, hostConfig string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "host.jsonc")
	if err := os.WriteFile(src, []byte(hostConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := config.LoadJSONCFile(src, "host config fixture", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	rendered, unknown, err := config.RenderInherit(parsed, config.InheritPreflight, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) > 0 {
		t.Fatalf("fixture uses config keys the census does not classify: %v", unknown)
	}
	writeUserConfig(t, home, rendered)
}

// maskTempPaths replaces every /tmp/... path in check's output with a token, so a substring
// search over the output cannot match the TEST'S OWN NAME.
//
// This matters here and nowhere else in the package, and it is a genuine trap rather than
// fussiness: Go names a t.TempDir() after the subtest, so a subtest called
// "cache_relocations" puts that exact word into every storage-path, config-path and
// repo-root line check prints. Both subtests failed on their own temp paths before this
// existed. normHome() cannot do the job — os.UserHomeDir() reads a cached value that
// t.Setenv does not move, and the repo-root temp dir is not under HOME at all.
func maskTempPaths(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], os.TempDir()+"/") {
			b.WriteString("<tmp>")
			for i < len(s) && s[i] != ' ' && s[i] != '\n' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// inJailOptions is baseOptions plus everything a check run needs to get PAST the
// accumulated-fail gate and actually reach the sections these tests are about.
//
// The gate is why the fixture is this specific. `check` short-circuits on ANY failure before
// the entrypoint dry-run and the GPU/KVM sections (deliberately — it must not do a surprise
// nix build on an unhealthy host), so a fixture with no runtime and no repo root never
// reaches them. A test asserting "check did not mention nvidia" under that fixture would
// pass for the wrong reason forever — which is exactly what the first draft of this file did
// until the control below caught it.
func inJailOptions(t *testing.T, out *bytes.Buffer) Options {
	t.Helper()
	opts := baseOptions(t, out)
	opts.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9-test"
		}
		return ""
	}
	opts.LookPath = func(name string) (string, bool) {
		if name == "podman" || name == "nix" {
			return "/usr/bin/" + name, true
		}
		return "", false
	}
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
		"podman info":      {Stdout: "host: {}", Ran: true, RC: 0},
		"nix --version":    {Stdout: "nix (Nix) 2.24.0", Ran: true, RC: 0},
	})
	// The real filesystem, so a user config that EXISTS is reported as parsed.
	opts.PathExists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	// A repo root, so the run clears the accumulated-fail gate and reaches the later
	// sections. No flake.nix in it — that is only a warning.
	repo := t.TempDir()
	opts.RepoRoot = func() (string, bool) { return repo, true }
	return opts
}

// THE LIVE FALSE-ERROR CLASSES (OQ-LP9 R1/R9). Each string below was MEASURED coming out of
// a real in-jail `yolo check --no-build` over a host config carrying the key — and none of
// the three had a guard, unlike the two keys the design doc names. Under the census the keys
// are not in the file at all, so no section can evaluate them.
func TestInJailCheckIsSilentAboutFilteredHostKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		// hostConfig is the HUMAN'S config, with the offending key present. It goes through
		// the real filter, so this test fails if the filter stops filtering.
		hostConfig string
		// forbidden are the strings the user must NOT see. Each is real output text from
		// the section that used to evaluate the key.
		forbidden []string
	}{
		{"gpu", `{"gpu": {"enabled": true}}`,
			[]string{"nvidia-smi", "nvidia-ctk", "No CDI spec"}},
		{"mounts", `{"mounts": ["/definitely-not-here:/ctx/x:ro"]}`,
			[]string{"host path does not exist and will be skipped"}},
		{"env_sources", `{"env_sources": ["~/.definitely-not-here.env"]}`,
			[]string{"env_sources file not found"}},
		// The two the doc names. Already silent in-jail via a hand-written guard, so these
		// rows are a REGRESSION on the property OQ-LP9 asks for rather than a fix: the key
		// must be absent, so that a future refactor deleting the guard (reasonably — it is
		// now redundant) cannot revive the false error.
		{"cache_relocations", `{"cache_relocations": {"npm": "/mnt/definitely-not-here/npm"}}`,
			[]string{"cache_relocations", "parent directory of the target"}},
		{"host_files", `{"host_files": [{"path": ".config/x/y", "source": "~/.config/x/y"}]}`,
			[]string{"host_files"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			// The host config goes THROUGH THE REAL FILTER and the result is what the jail
			// reads — so the key's absence is the filter's doing, not the fixture's.
			writeInheritedUserConfig(t, home, tc.hostConfig)

			var out bytes.Buffer
			Check(inJailOptions(t, &out))
			got := maskTempPaths(out.String())

			for _, forbidden := range tc.forbidden {
				if strings.Contains(got, forbidden) {
					t.Errorf("in-jail `yolo check` said %q — the point of the generated "+
						"preflight file is that %q is ABSENT, so nothing can evaluate a host "+
						"referent against a container that does not have it:\n%s",
						forbidden, tc.name, got)
				}
			}
		})
	}
}

// THE CONTROL, and it is what makes the test above mean anything. With the key PRESENT — the
// pre-OQ-LP9 raw bind, or a filter regression — the same in-jail check DOES report it. So the
// silence above is caused by the key's absence, not by the fixture being too weak to reach
// the section.
//
// Only the classes with no pre-existing guard can be controlled for this way; that is
// precisely the difference this file records, and the reason cache_relocations is not here.
func TestInJailCheckDoesReportThoseKeysWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		expect string
	}{
		{"gpu", `{"gpu": {"enabled": true}}`, "nvidia"},
		{"mounts", `{"mounts": ["/definitely-not-here:/ctx/x:ro"]}`, "host path does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			writeUserConfig(t, home, tc.config)

			var out bytes.Buffer
			Check(inJailOptions(t, &out))

			if !strings.Contains(out.String(), tc.expect) {
				t.Errorf("control failed: with %s PRESENT, in-jail check did not say %q — so "+
					"TestInJailCheckIsSilentAboutFilteredHostKeys/%s proves nothing about the "+
					"FILTER. Either the section moved or the fixture no longer reaches it:\n%s",
					tc.name, tc.expect, tc.name, out.String())
			}
		})
	}
}

// And the other half of R1, so the silence tests cannot be satisfied by an empty file: what
// the jail DOES inherit is still read. `packs` crosses, and the no-packs notice is absent
// precisely because the inherited scope named one.
func TestInJailCheckStillReadsTheKeysThatDoCross(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var withPacks, without bytes.Buffer

	writeUserConfig(t, home, `{"packs": ["claude"]}`)
	Check(inJailOptions(t, &withPacks))

	writeUserConfig(t, home, `{}`)
	Check(inJailOptions(t, &without))

	const notice = "No packs are configured"
	if !strings.Contains(without.String(), notice) {
		t.Fatalf("fixture problem: an empty user scope should produce the no-packs notice, "+
			"so this test cannot tell whether `packs` was read:\n%s", without.String())
	}
	if strings.Contains(withPacks.String(), notice) {
		t.Errorf("`packs` did not reach the in-jail check — it printed the no-packs notice "+
			"for a scope that names claude. A filter that dropped everything would pass the "+
			"silence tests above while making the jail's user scope useless:\n%s",
			withPacks.String())
	}
}
