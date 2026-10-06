package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestANextLaunchRefreshRunsBehindTheProgramInTheJail is the jail-side cell of the pre-launch
// refresh's background arm (docs/design/program-delivery.md OQ-PD30, OQ-PD31): with
// `agent_updates` giving a pack "next-launch", the launcher starts the pack's declared refresh as a
// detached job through the jail's own `yolo internal no-terminal --detach`, execs the program
// without waiting, and the job finishes behind it — its act detached and under the bound, its
// output in the job's log. The unit tier (internal/entrypoint/prelaunchrefreshtiming_test.go)
// runs the same launcher through a stand-in yolo; this is the jail's real yolo, launcher, PATH
// and init, so what it pins is that the real binary takes the flag and that the job outlives
// the exec in a real container.
func TestANextLaunchRefreshRunsBehindTheProgramInTheJail(t *testing.T) {
	requireJail(t)
	const (
		packName = "local-refresh-fixture"
		bin      = "yolo-refresh-fixture"
		sentinel = "refresh-fixture-ran"
	)
	pack := t.TempDir()
	// The installed tool's `refresh-ext` is the refresh. It records how it was run (its stdin,
	// whether it leads a session, its parent's argv), takes long enough that a launch that waited
	// for it would see it done, and then marks itself done.
	installer := `#!/bin/bash
set -euo pipefail
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/` + bin + `" <<'TOOL'
#!/bin/bash
if [ "${1:-}" = refresh-ext ]; then
  printf 'stdin=%s session-leader=%s\n' "$(readlink /proc/$$/fd/0)" \
    "$([ "$(cut -d' ' -f6 /proc/$$/stat)" = "$$" ] && echo yes || echo no)" > "$HOME/.local/refresh-run"
  printf 'parent=%s\n' "$(tr '\0' ' ' < /proc/$PPID/cmdline)" >> "$HOME/.local/refresh-run"
  echo "REFRESH-OUTPUT"
  sleep 3
  : > "$HOME/.local/refresh-done"
  exit 0
fi
echo "` + sentinel + `"
TOOL
chmod +x "$HOME/.local/bin/` + bin + `"
`
	if err := os.WriteFile(filepath.Join(pack, "install.sh"), []byte(installer), 0o644); err != nil {
		t.Fatal(err)
	}
	// The refresh's lock lives in its store, which the launcher never creates: the script below
	// makes it, under ~/.local because that is writable in a jail's home.
	manifest := `{
  "name": "` + packName + `",
  "description": "background pre-launch refresh, from the pack's own tree",
  "contributes": [
    {"kind": "program", "bin": "` + bin + `", "via": "installer",
     "url": "file:///ctx/packs/` + packName + `/install.sh",
     "refresh": {"argv": ["refresh-ext"], "lock": ".local/refresh-fixture-store/.yolo-update.lock"}}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+packName+`"}], `+
		`"agent_updates": {"`+packName+`": "next-launch"}}`)

	script := strings.Join([]string{
		`mkdir -p "$HOME/.local/refresh-fixture-store"`,
		`echo "=== LAUNCH ==="`,
		bin,
		// Right after the launch returned: a refresh run in front would be done by now.
		`[ -e "$HOME/.local/refresh-done" ] && echo "DONE-AT-RETURN" || echo "RUNNING-AT-RETURN"`,
		// The job must finish before this script ends, or the jail's exit would end it.
		`for _ in $(seq 1 300); do [ -e "$HOME/.local/refresh-done" ] && break; sleep 0.1; done`,
		`for _ in $(seq 1 100); do grep -q 'background refresh ended' "$HOME/.local/state/yolo/refresh/` + bin + `.log" 2>/dev/null && break; sleep 0.1; done`,
		`echo "=== HOW ==="`,
		`cat "$HOME/.local/refresh-run"`,
		`echo "=== LOG ==="`,
		`cat "$HOME/.local/state/yolo/refresh/` + bin + `.log"`,
	}, "; ")
	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("refresh fixture failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	launch := section(r.stdout, "=== LAUNCH ===", "=== HOW ===")
	if !strings.Contains(launch, sentinel) {
		t.Errorf("the program did not run: %q\nstderr: %s", launch, r.stderr)
	}
	if !strings.Contains(launch, "RUNNING-AT-RETURN") {
		t.Errorf("the launch returned with the refresh already done: it ran in front:\n%s\nstderr: %s", launch, r.stderr)
	}
	if strings.Contains(launch, "REFRESH-OUTPUT") || strings.Contains(r.stderr, "REFRESH-OUTPUT") {
		t.Errorf("the background refresh wrote into the launch:\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "refreshing in the background") ||
		!strings.Contains(r.stderr, ".local/state/yolo/refresh/"+bin+".log") {
		t.Errorf("the launch must say the refresh runs in the background, naming its log:\n%s", r.stderr)
	}
	how := section(r.stdout, "=== HOW ===", "=== LOG ===")
	for _, want := range []string{"stdin=/dev/null", "session-leader=yes", "internal no-terminal",
		"--timeout=60", "--kill-after=5", "refresh-ext"} {
		if !strings.Contains(how, want) {
			t.Errorf("the background refresh's act must run detached, under the bound (want %q), got %q", want, how)
		}
	}
	jobLog := section(r.stdout, "=== LOG ===", "")
	for _, want := range []string{"REFRESH-OUTPUT", "background refresh ended (status 0)"} {
		if !strings.Contains(jobLog, want) {
			t.Errorf("the job's log lacks %q:\n%s", want, jobLog)
		}
	}
	if strings.Contains(r.stdout+r.stderr, "cannot run this refresh in the background") {
		t.Errorf("the jail's own yolo lacked the detach, so the launcher fell back:\n%s%s", r.stdout, r.stderr)
	}
}
