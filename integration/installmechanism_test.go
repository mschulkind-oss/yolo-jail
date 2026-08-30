package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Install-MECHANISM tests: the two ways a pack can put a program in a jail, each
// exercised once, from bytes this repository chooses.
//
// They exist because the agent-install matrix was answering a question it could not
// answer honestly (docs/design/agent-install-in-ci.md). Nine agent-CLI installs per run,
// eight of them the same npm code path with a different package string, every one
// resolving an unpinned `@latest` — so a green main could go red with no commit, and did:
// on 2026-08-20 codex's linux-arm64 tarball was published 37 minutes after the parent that
// `@latest` pointed at, and three tests went red for a defect in nobody's repository.
//
// The split these tests implement (§6.1, §6.1.1): the every-push gate proves the
// MECHANISM from pinned bytes, and "do the six vendors' current releases still install?"
// moves to a weekly job plus a `packs/**`-triggered one, where a failure is caused by
// something a commit or a calendar can explain.
//
// Both fixtures are LOCAL (`file://`) packs, which matters twice over: a local pack's
// origin permits an `installerUrl` at all (config.PackEntry.MayGrantHostFiles is true for
// anything not fetched, so packload's HonoredInstalls grants it), and the pack tree is
// copied whole into /ctx/packs/<name>/ with exec bits preserved, so a pack can carry its
// own installer script.

// pinnedNpmPackage is the npm specimen for the mechanism cell, and every part of the
// choice is load-bearing:
//
//   - PINNED to an exact version, which is the whole point — `cowsay@1.6.0` is the same
//     bytes tomorrow, so this cell cannot go red without a commit.
//   - PURE JS, no platform optionalDependencies. The 2026-08-20 outage was a missing
//     per-arch tarball silently skipped by npm; a package with no such deps cannot express
//     that failure at all.
//   - SMALL (≈480 KiB unpacked) and long-stable (1.6.0 published 2024-01-26). A mechanism
//     test wants the smallest thing that exercises the path, not a 100 MB agent CLI.
//   - Its bin is NOT baked into the image. That is a real trap: ~/.yolo/bin/launch is
//     ordered LAST on PATH, after /bin, so a fixture naming a baked binary (fzf is the
//     documented case — see TestPackProgramDoesNotShadowABakedBinary) would resolve to the
//     image's copy and never exercise the launcher at all. The test would pass while
//     installing nothing.
const (
	pinnedNpmPackage = "cowsay@1.6.0"
	pinnedNpmBin     = "cowsay"
	pinnedNpmVersion = "1.6.0"
)

// TestPinnedNpmProgramInstallsTheDeclaredVersion is the npm mechanism cell.
//
// It asserts the thing the shipped packs cannot currently assert: that the version on disk
// is the version the repository DECLARED. A pack carrying no selector gets `@latest`
// appended (npmspec.go), so today's matrix can only assert "something installed" — which is
// why an upstream publish race read as a repo failure.
func TestPinnedNpmProgramInstallsTheDeclaredVersion(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	manifest := `{
  "name": "pinned-npm-fixture",
  "description": "npm install mechanism, pinned",
  "contributes": [
    {"kind": "program", "bin": "` + pinnedNpmBin + `", "via": "npm", "package": "` + pinnedNpmPackage + `"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["file://`+pack+`"]}`)

	// The installed version is read from node_modules rather than from a --version flag:
	// the flag is the package's business, the directory is npm's, and only the second is
	// evidence about what was INSTALLED.
	script := strings.Join([]string{
		`echo "=== RESOLVE ==="`,
		`command -v ` + pinnedNpmBin,
		`echo "=== RUN ==="`,
		pinnedNpmBin + ` mechanism`,
		`echo "=== VERSION ==="`,
		`jq -r .version "$NPM_CONFIG_PREFIX/lib/node_modules/` + pinnedNpmBin + `/package.json"`,
	}, "; ")

	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("pinned npm fixture failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}

	// Resolved through the LAUNCHER, not something already on PATH. Without this the test
	// would pass for a bin the image happens to bake, having installed nothing.
	if got := strings.TrimSpace(section(r.stdout, "=== RESOLVE ===", "=== RUN ===")); !strings.Contains(got, ".yolo/bin/launch") {
		t.Errorf("%s resolved to %q, want a path under ~/.yolo/bin/launch — a bin resolved "+
			"elsewhere means the launcher never ran and nothing was installed", pinnedNpmBin, got)
	}
	if got := section(r.stdout, "=== RUN ===", "=== VERSION ==="); !strings.Contains(got, "mechanism") {
		t.Errorf("the installed program did not run: %q", got)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== VERSION ===", "")); got != pinnedNpmVersion {
		t.Errorf("installed version = %q, want exactly %q — the DECLARATION must choose the "+
			"bytes, not the registry's `latest` dist-tag", got, pinnedNpmVersion)
	}
}

// TestInstallerProgramRunsThePacksOwnScript is the `installer` (curl-piped) mechanism cell,
// and it is fully HERMETIC — no registry, no network at all.
//
// That is better than the design doc predicted. §6.1 assumed a pinned `installer` fixture
// would need "an installer URL the test controls" and worried about a local HTTP server;
// two shipped facts make the server unnecessary. The native launcher downloads with
// `curl -fsSL <url> -o <file>` before running it, and curl in the jail image supports the
// FILE protocol (verified 2026-08-21, curl 8.21.0). And a pack's tree is copied whole into
// /ctx/packs/<name>/, so the script can ride along inside the pack.
//
// So the mechanism yolo treats as its sharpest — packdecl calls installerUrl "a URL whose
// contents run as a shell script" — gets a test that reaches a real container while
// depending on nothing outside this repository. It is also the coverage §2.3 counted as
// thinnest: eight npm installs per run against one installer install, with `agy` having no
// cell at all.
func TestInstallerProgramRunsThePacksOwnScript(t *testing.T) {
	requireJail(t)

	const (
		packName = "local-installer-fixture"
		bin      = "yolo-fixture-tool"
		sentinel = "installer-mechanism-ok"
	)

	pack := t.TempDir()
	// The launcher's contract: an installer's job is to leave an executable at
	// $HOME/.local/bin/<bin>, which is the REAL_BIN the launcher then execs.
	installer := `#!/bin/bash
set -euo pipefail
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/` + bin + `" <<'TOOL'
#!/bin/bash
echo "` + sentinel + `"
TOOL
chmod +x "$HOME/.local/bin/` + bin + `"
`
	// 0644, NOT 0755, and the difference is a whole gate. A CONFIGURED pack may not
	// self-grant an exec bit: staging refuses the file outright and tells the user to opt in
	// with `allow_exec: true` on the config entry, because "a pack comes from someone else's
	// repo, so shipping an executable is the user's call, not the author's." The bit is also
	// pointless here — the launcher curls the script to a temp file and runs `bash <file>` —
	// so setting it for realism bought nothing and pulled in an unrelated opt-in axis.
	if err := os.WriteFile(filepath.Join(pack, "install.sh"), []byte(installer), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "` + packName + `",
  "description": "native installer mechanism, from the pack's own tree",
  "contributes": [
    {"kind": "program", "bin": "` + bin + `", "via": "installer",
     "url": "file:///ctx/packs/` + packName + `/install.sh"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{}`)
	// The entry names itself EXPLICITLY, and that is required rather than tidy: a pack's
	// staged directory is named by defaultPackName, which takes the SOURCE URL's last path
	// segment and never reads the manifest's `name`. Under t.TempDir() that segment is a
	// counter, so the bare form staged this pack at /ctx/packs/001/ and the installerUrl
	// below pointed at a path that did not exist.
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+packName+`"}]}`)

	script := strings.Join([]string{
		`echo "=== RESOLVE ==="`,
		`command -v ` + bin,
		`echo "=== RUN ==="`,
		bin,
	}, "; ")

	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("installer fixture failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== RESOLVE ===", "=== RUN ===")); !strings.Contains(got, ".yolo/bin/launch") {
		t.Errorf("%s resolved to %q, want a path under ~/.yolo/bin/launch", bin, got)
	}
	if got := section(r.stdout, "=== RUN ===", ""); !strings.Contains(got, sentinel) {
		t.Errorf("the installed program did not run: %q", got)
	}
}
