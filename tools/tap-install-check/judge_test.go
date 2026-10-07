package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/tools/tap-install-check/baremac"
)

// resolvedTempDir is t.TempDir with its symlinks resolved where it is minted: on
// macOS the temp root is /var/folders/…, a symlink to /private/var/folders/…, and
// judgeCheck compares resolved paths.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// fakeKeg lays out the part of a Homebrew keg the checker reads: share/yolo-jail with
// a flake.nix. Returns the keg and the bundle directory.
func fakeKeg(t *testing.T) (keg, bundle string) {
	t.Helper()
	keg = resolvedTempDir(t)
	bundle = filepath.Join(keg, "share", "yolo-jail")
	writeFile(t, filepath.Join(bundle, "flake.nix"), "{}", 0o644)
	return keg, bundle
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// bareMacCheck runs the REAL `yolo check --no-build --format json` body, this tree's,
// under the conditions of a stock macOS runner after `brew install` (package baremac
// defines them, once, for this tree and for the last release alike).
func bareMacCheck(t *testing.T, version, bundle string) ([]byte, int) {
	t.Helper()
	t.Setenv("HOME", resolvedTempDir(t))
	var out bytes.Buffer
	rc := baremac.Check(baremac.Inputs{Version: version, Bundle: bundle, Workspace: resolvedTempDir(t)}, &out)
	return out.Bytes(), rc
}

// requireTheJudgeAccepts is the forward half of the pin between this checker and the
// code it reads: a report the real Check produced under a bare Mac's conditions must be
// one the judge accepts — its "flake.nix found … (via flake bundle beside the binary)"
// line, its JSON field names, and its FAIL set.
func requireTheJudgeAccepts(t *testing.T, whose string, out []byte, rc int, version, keg, bundle string) {
	t.Helper()
	got, errs := judgeCheck(out, rc, version, keg)
	for _, err := range errs {
		t.Errorf("the judge refused %s report: %v", whose, err)
	}
	if got != bundle {
		t.Errorf("judgeCheck returned bundle %q from %s report, want %q", got, whose, bundle)
	}
	if rc != 1 {
		t.Errorf("%s check exited %d on a bare Mac, want 1 (it has no runtime and no Nix)", whose, rc)
	}
	rep := decodeReport(t, out)
	if len(rep.Findings) == 0 || rep.Failed == 0 {
		t.Errorf("%s report graded %d findings with %d FAILs; the fixture no longer exercises the allowlist",
			whose, len(rep.Findings), rep.Failed)
	}
}

func decodeReport(t *testing.T, out []byte) checkReport {
	t.Helper()
	var rep checkReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("the report is not the document checkReport reads: %v\n%s", err, out)
	}
	return rep
}

// TestTheJudgeAcceptsWhatCheckReportsOnABareMac pins the judge to THIS tree's Check. It
// fails if check.go stops grading the flake line, if reporoot renames the Homebrew
// source, or if a fresh Mac starts failing a section the runner's missing runtime and
// Nix do not explain.
//
// It is only half the pin. The workflow runs the version the TAP carries, not this
// tree, so TestTheJudgeAcceptsWhatTheLastReleaseReportsOnABareMac (release_test.go)
// holds the judge to that release's Check as well, and is where the allowlist is held
// to the code in the other direction: an entry is stale only once neither this tree
// nor the release the tap carries prints it.
func TestTheJudgeAcceptsWhatCheckReportsOnABareMac(t *testing.T) {
	keg, bundle := fakeKeg(t)
	out, rc := bareMacCheck(t, "0.11.0", bundle)
	requireTheJudgeAccepts(t, "this tree's", out, rc, "0.11.0", keg, bundle)
}

// TestTheJudgeRefusesTheCheckReportsThatMeanABrokenInstall mutates the real report
// into each shape the checker exists to catch.
func TestTheJudgeRefusesTheCheckReportsThatMeanABrokenInstall(t *testing.T) {
	keg, bundle := fakeKeg(t)
	real, rc := bareMacCheck(t, "0.11.0", bundle)
	elsewhere := resolvedTempDir(t)
	writeFile(t, filepath.Join(elsewhere, "flake.nix"), "{}", 0o644)

	type finding = map[string]any
	edit := func(fn func(doc map[string]any)) []byte {
		var doc map[string]any
		if err := json.Unmarshal(real, &doc); err != nil {
			t.Fatal(err)
		}
		fn(doc)
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	findings := func(doc map[string]any) []any { return doc["findings"].([]any) }
	mapFlake := func(fn func(f finding)) func(map[string]any) {
		return func(doc map[string]any) {
			for _, f := range findings(doc) {
				f := f.(finding)
				if strings.HasPrefix(f["message"].(string), "flake.nix found: ") {
					fn(f)
				}
			}
		}
	}
	dropFlake := func(doc map[string]any) {
		var kept []any
		for _, f := range findings(doc) {
			if !strings.HasPrefix(f.(finding)["message"].(string), "flake.nix found: ") {
				kept = append(kept, f)
			}
		}
		doc["findings"] = kept
	}
	addFail := func(section, msg string) func(map[string]any) {
		return func(doc map[string]any) {
			doc["findings"] = append(findings(doc), finding{"section": section, "status": "fail", "message": msg})
			doc["failed"] = doc["failed"].(float64) + 1
		}
	}

	for _, tc := range []struct {
		name    string
		report  []byte
		rc      int
		version string
		want    string
	}{
		{"the report is not JSON", []byte("YOLO Jail Check\n  [FAIL] …"), rc, "0.11.0", "did not print a JSON report"},
		// Both crash cases name the crash, not only the exit code: the disagreement
		// check also prints "exited 2", and without the crash guard a report that
		// says nothing failed would be accepted from a command that exited 2.
		{"the command crashed", real, 2, "0.11.0", "so this is a crash or a usage error"},
		{"the command crashed after reporting no failures", edit(func(d map[string]any) { d["failed"] = 0.0 }), 2, "0.11.0", "so this is a crash or a usage error"},
		{"exit 0 over failed findings", real, 0, "0.11.0", "disagree"},
		{"exit 1 over no failed findings", edit(func(d map[string]any) { d["failed"] = 0.0 }), 1, "0.11.0", "disagree"},
		{"another version answered", real, rc, "0.12.0", `reports version "0.11.0", want "0.12.0"`},
		{"no flake line at all", edit(dropFlake), rc, "0.11.0", "did not find its flake bundle"},
		{"the flake line is only a warning", edit(mapFlake(func(f finding) { f["status"] = "warn" })), rc, "0.11.0", "did not find its flake bundle"},
		{"two flake lines", edit(func(d map[string]any) {
			for _, f := range findings(d) {
				if strings.HasPrefix(f.(finding)["message"].(string), "flake.nix found: ") {
					d["findings"] = append(findings(d), f)
					return
				}
			}
		}), rc, "0.11.0", "want exactly 1"},
		{"the flake came from YOLO_REPO_ROOT", edit(mapFlake(func(f finding) {
			f["message"] = "flake.nix found: " + filepath.Join(bundle, "flake.nix") + " (via " + reporoot.FromEnv.Describe() + ")"
		})), rc, "0.11.0", "this install's own bundle was not the one found"},
		{"the flake is outside the keg", edit(mapFlake(func(f finding) {
			f["message"] = "flake.nix found: " + filepath.Join(elsewhere, "flake.nix") + " (via " + besideBinary + ")"
		})), rc, "0.11.0", "is not this install's"},
		// Inside the keg, so only the name check stands between it and a bundle
		// directory one level too high.
		{"the flake finding names the bundle directory, not its flake.nix", edit(mapFlake(func(f finding) {
			f["message"] = "flake.nix found: " + bundle + " (via " + besideBinary + ")"
		})), rc, "0.11.0", "which is not a flake.nix"},
		{"a fresh Mac fails storage", edit(addFail("Global Storage", "Cannot create /Users/runner/.local/share/yolo-jail")), rc, "0.11.0", `unexpected FAIL in "Global Storage"`},
		{"a macOS Platform fail that is not the Nix volume", edit(addFail("macOS Platform", "Podman Machine: broken")), rc, "0.11.0", `unexpected FAIL in "macOS Platform"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := judgeCheck(tc.report, tc.rc, tc.version, keg)
			if len(errs) == 0 {
				t.Fatalf("judgeCheck accepted it")
			}
			joined := errors.Join(errs...).Error()
			if !strings.Contains(joined, tc.want) {
				t.Errorf("judgeCheck said:\n%s\nwant it to say %q", joined, tc.want)
			}
		})
	}
}

func TestParseBrewInfo(t *testing.T) {
	const formula = "mschulkind-oss/tap/yolo-jail"
	info := func(fullName, stable string, revision int, linked any) []byte {
		b, err := json.Marshal(map[string]any{
			"formulae": []any{map[string]any{
				"name":       "yolo-jail",
				"full_name":  fullName,
				"versions":   map[string]any{"stable": stable, "head": nil, "bottle": false},
				"revision":   revision,
				"linked_keg": linked,
				"installed":  []any{map[string]any{"version": stable}},
			}},
			"casks": []any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	got, err := parseBrewInfo(info(formula, "0.11.0", 0, "0.11.0"), formula)
	if err != nil || got != (tapFacts{Version: "0.11.0", Keg: "0.11.0"}) {
		t.Errorf("installed and linked: got %+v, %v", got, err)
	}
	// A formula revision names the keg, never the version the binary reports.
	got, err = parseBrewInfo(info(formula, "0.11.0", 1, "0.11.0_1"), formula)
	if err != nil || got != (tapFacts{Version: "0.11.0", Keg: "0.11.0_1"}) {
		t.Errorf("with a revision: got %+v, %v", got, err)
	}

	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"not JSON", []byte("Error: No available formula"), "did not parse"},
		{"no formula", []byte(`{"formulae":[],"casks":[]}`), "answered 0 formulae"},
		{"another tap's formula", info("homebrew/core/yolo-jail", "0.11.0", 0, "0.11.0"), "a different formula"},
		{"not installed", info(formula, "0.11.0", 0, nil), "not installed and linked"},
		{"an older keg is linked", info(formula, "0.11.0", 0, "0.10.0"), "the linked keg is 0.10.0"},
		{"no stable version", info(formula, "", 0, "0.11.0"), "no stable version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBrewInfo(tc.data, formula)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseBrewInfo = %v, want an error saying %q", err, tc.want)
			}
		})
	}
}

func TestCheckVersionLine(t *testing.T) {
	if err := checkVersionLine("yolo-jail 0.11.0\n", "0.11.0"); err != nil {
		t.Errorf("the formula's own stamp was refused: %v", err)
	}
	for _, out := range []string{
		"yolo-jail 0.11.0+3.gabc1234\n", // a from-source describe: contains the version, is not it
		"yolo-jail unknown\n",           // an unstamped build
		"yolo-jail 0.10.0\n",
		"0.11.0\n",
		"yolo-jail 0.11.0\nyolo-jail 0.11.0\n",
	} {
		if err := checkVersionLine(out, "0.11.0"); err == nil {
			t.Errorf("checkVersionLine accepted %q for 0.11.0", out)
		}
	}
}

func TestExpectFromGitHubEvent(t *testing.T) {
	run := func(event, headBranch, displayTitle, conclusion string) []byte {
		b, _ := json.Marshal(map[string]any{"workflow_run": map[string]any{
			"name": "Release", "event": event, "head_branch": headBranch,
			"display_title": displayTitle, "conclusion": conclusion,
		}})
		return b
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name, event string
		payload     []byte
		want        string
		wantErr     string
	}{
		{"legacy tag push names its tag", "workflow_run", run("push", "v0.12.0", "Release", "success"), "0.12.0", ""},
		{"legacy prerelease tag push", "workflow_run", run("push", "v0.12.0-rc.1", "Release", "success"), "0.12.0-rc.1", ""},
		{"legacy tag-scoped dispatch names its version", "workflow_run", run("workflow_dispatch", "v0.12.0", "Release", "success"), "0.12.0", ""},
		{"legacy tag-scoped prerelease dispatch", "workflow_run", run("workflow_dispatch", "v0.12.0-rc.1", "Release", "success"), "0.12.0-rc.1", ""},
		{"trusted-main prerelease dispatch", "workflow_run", run("workflow_dispatch", "main", "Release v0.12.0-rc.1 @ "+sha+" / request 123", "success"), "0.12.0-rc.1", ""},
		{"explicit Homebrew-only backfill carries no release expectation", "workflow_run", run("workflow_dispatch", "main", "Homebrew-only v0.12.0", "success"), "", ""},
		{"main branch alone cannot waive a normal release version", "workflow_run", run("workflow_dispatch", "main", "Release", "success"), "", "missing or malformed display_title"},
		{"missing main-scoped display title is refused", "workflow_run", run("workflow_dispatch", "main", "", "success"), "", "missing or malformed display_title"},
		{"malformed main-scoped release title is refused", "workflow_run", run("workflow_dispatch", "main", "Release vlatest @ "+sha+" / request 123", "success"), "", "missing or malformed display_title"},
		{"main-scoped release title with malformed SHA is refused", "workflow_run", run("workflow_dispatch", "main", "Release v0.12.0 @ short / request 123", "success"), "", "missing or malformed display_title"},
		{"main-scoped release title with zero request id is refused", "workflow_run", run("workflow_dispatch", "main", "Release v0.12.0 @ "+sha+" / request 0", "success"), "", "missing or malformed display_title"},
		{"main-scoped release title does not accept trailing data", "workflow_run", run("workflow_dispatch", "main", "Release v0.12.0 @ "+sha+" / request 123; echo unsafe", "success"), "", "missing or malformed display_title"},
		{"Homebrew-only backfill title is also anchored", "workflow_run", run("workflow_dispatch", "main", "Homebrew-only v0.12.0 extra", "success"), "", "missing or malformed display_title"},
		{"main release metadata on a non-main ref is refused", "workflow_run", run("workflow_dispatch", "release/0.12.0", "Release v0.12.0 @ "+sha+" / request 123", "success"), "", "unexpected ref"},
		{"failed main-scoped release is refused", "workflow_run", run("workflow_dispatch", "main", "Release v0.12.0 @ "+sha+" / request 123", "failure"), "", `concluded "failure"`},
		{"failed legacy tag dispatch is refused", "workflow_run", run("workflow_dispatch", "v0.12.0", "Release", "failure"), "", `concluded "failure"`},
		{"push with no tag is refused", "workflow_run", run("push", "", "Release", "success"), "", "names no tag"},
		{"branch push where a tag belongs is refused", "workflow_run", run("push", "main", "Release", "success"), "", "not a release version"},
		{"unrecognized release event is refused", "workflow_run", run("schedule", "main", "Release", "success"), "", "unsupported event"},
		{"payload without run is refused", "workflow_run", []byte(`{}`), "", "no workflow_run object"},
		{"manual tap-check dispatch naming a version", "workflow_dispatch", []byte(`{"inputs":{"version":"0.11.0"}}`), "0.11.0", ""},
		{"manual tap-check dispatch naming a tag", "workflow_dispatch", []byte(`{"inputs":{"version":"v0.11.0"}}`), "0.11.0", ""},
		{"manual tap-check dispatch naming nothing", "workflow_dispatch", []byte(`{"inputs":{"version":""}}`), "", ""},
		{"manual tap-check dispatch naming garbage", "workflow_dispatch", []byte(`{"inputs":{"version":"latest"}}`), "", "not a release version"},
		{"weekly schedule", "schedule", []byte(`{"schedule":"0 8 * * 3"}`), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expectFromGitHubEvent(tc.event, tc.payload)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got (%q, %v), want an error saying %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("got (%q, %v), want %q", got, err, tc.want)
			}
		})
	}
}

// fakeELF is the smallest file debug/elf accepts: a 64-bit little-endian header with
// no program or section headers.
func fakeELF(t *testing.T, machine elf.Machine) string {
	t.Helper()
	h := elf.Header64{
		Type: uint16(elf.ET_EXEC), Machine: uint16(machine), Version: uint32(elf.EV_CURRENT),
		Ehsize: 64, Phentsize: 56, Shentsize: 64,
	}
	copy(h.Ident[:], elf.ELFMAG)
	h.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	h.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	h.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	var b bytes.Buffer
	if err := binary.Write(&b, binary.LittleEndian, h); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// fakeMachO is the smallest file debug/macho accepts: a 64-bit header with no load
// commands.
func fakeMachO(t *testing.T, cpu macho.Cpu) string {
	t.Helper()
	var b bytes.Buffer
	if err := binary.Write(&b, binary.LittleEndian, macho.FileHeader{Magic: macho.Magic64, Cpu: cpu, Type: macho.TypeExec}); err != nil {
		t.Fatal(err)
	}
	b.Write(make([]byte, 4)) // the 64-bit header's reserved word
	return b.String()
}

func TestCheckBundle(t *testing.T) {
	good := func(t *testing.T) string {
		dir := resolvedTempDir(t)
		writeFile(t, filepath.Join(dir, "flake.nix"), "{}", 0o644)
		writeFile(t, filepath.Join(dir, "flake.lock"), "{}", 0o644)
		for _, name := range []string{"yolo", "yolo-entrypoint", "yolo-jaild"} {
			writeFile(t, filepath.Join(dir, "bin/linux-amd64", name), fakeELF(t, elf.EM_X86_64), 0o755)
			writeFile(t, filepath.Join(dir, "bin/linux-arm64", name), fakeELF(t, elf.EM_AARCH64), 0o755)
		}
		writeFile(t, filepath.Join(dir, "bin/darwin-arm64/yolo-jaild"), fakeMachO(t, macho.CpuArm64), 0o755)
		writeFile(t, filepath.Join(dir, "bin/darwin-amd64/yolo-jaild"), fakeMachO(t, macho.CpuAmd64), 0o755)
		return dir
	}

	t.Run("what stage-source-bundle.sh stages", func(t *testing.T) {
		unknown, errs := checkBundle(good(t))
		if len(errs) != 0 || len(unknown) != 0 {
			t.Errorf("a good bundle: unknown=%v errs=%v", unknown, errs)
		}
	})
	// A published version that predates the darwin guest dirs ships only the Linux ones.
	t.Run("a bundle with no darwin guest dirs", func(t *testing.T) {
		dir := good(t)
		for _, d := range []string{"bin/darwin-arm64", "bin/darwin-amd64"} {
			if err := os.RemoveAll(filepath.Join(dir, d)); err != nil {
				t.Fatal(err)
			}
		}
		if unknown, errs := checkBundle(dir); len(errs) != 0 || len(unknown) != 0 {
			t.Errorf("unknown=%v errs=%v", unknown, errs)
		}
	})
	t.Run("a directory it does not know is reported, not passed", func(t *testing.T) {
		dir := good(t)
		writeFile(t, filepath.Join(dir, "bin/linux-riscv64/yolo"), "x", 0o755)
		unknown, errs := checkBundle(dir)
		if len(errs) != 0 || len(unknown) != 1 || unknown[0] != "bin/linux-riscv64" {
			t.Errorf("unknown=%v errs=%v, want bin/linux-riscv64 reported", unknown, errs)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, dir string)
		want   string
	}{
		{"no flake.lock", func(t *testing.T, dir string) { os.Remove(filepath.Join(dir, "flake.lock")) }, "no flake.lock"},
		{"no pid1 for arm64", func(t *testing.T, dir string) {
			os.Remove(filepath.Join(dir, "bin/linux-arm64/yolo-entrypoint"))
		}, "no bin/linux-arm64/yolo-entrypoint"},
		{"an amd64 binary under linux-arm64", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "bin/linux-arm64/yolo-jaild"), fakeELF(t, elf.EM_X86_64), 0o755)
		}, "bin/linux-arm64/yolo-jaild: is ELFCLASS64 EM_X86_64"},
		{"a Mach-O under linux-amd64", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "bin/linux-amd64/yolo"), fakeMachO(t, macho.CpuAmd64), 0o755)
		}, "bin/linux-amd64/yolo: not an ELF executable"},
		{"an ELF under darwin-arm64", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "bin/darwin-arm64/yolo-jaild"), fakeELF(t, elf.EM_AARCH64), 0o755)
		}, "bin/darwin-arm64/yolo-jaild: not a Mach-O executable"},
		{"a binary that lost its exec bit", func(t *testing.T, dir string) {
			if err := os.Chmod(filepath.Join(dir, "bin/linux-amd64/yolo-entrypoint"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "bin/linux-amd64/yolo-entrypoint is not an executable file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := good(t)
			tc.mutate(t, dir)
			_, errs := checkBundle(dir)
			joined := errors.Join(errs...)
			if joined == nil || !strings.Contains(joined.Error(), tc.want) {
				t.Errorf("checkBundle = %v, want an error saying %q", joined, tc.want)
			}
		})
	}
}

// TestWithin covers the containment rule on the layout Homebrew actually produces:
// the PATH entry and the prefix's share/ are symlinks into the Cellar keg.
func TestWithin(t *testing.T) {
	root := resolvedTempDir(t)
	keg := filepath.Join(root, "Cellar", "yolo-jail", "0.11.0")
	writeFile(t, filepath.Join(keg, "bin", "yolo"), "", 0o755)
	writeFile(t, filepath.Join(root, "Cellar", "yolo-jail", "0.11.0-other", "bin", "yolo"), "", 0o755)
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin", "yolo")
	if err := os.Symlink(filepath.Join("..", "Cellar", "yolo-jail", "0.11.0", "bin", "yolo"), link); err != nil {
		t.Fatal(err)
	}
	opt := filepath.Join(root, "opt")
	if err := os.Symlink(keg, opt); err != nil {
		t.Fatal(err)
	}

	if err := within(link, filepath.Join(opt, "bin")); err != nil {
		t.Errorf("a PATH symlink into the keg, against the opt/ symlink: %v", err)
	}
	// A sibling whose name merely STARTS with the keg's is outside it.
	if err := within(filepath.Join(root, "Cellar", "yolo-jail", "0.11.0-other", "bin", "yolo"), keg); err == nil {
		t.Error("a sibling keg sharing the prefix was accepted")
	}
	if err := within(filepath.Join(root, "bin"), keg); err == nil {
		t.Error("a path outside the keg was accepted")
	}
}
