package integration

import (
	"slices"
	"strings"
	"testing"
)

// misepnpm_test.go pins, on the IMAGE'S OWN mise, the fact the host's pnpm rule rests on.
//
// The host keeps `pnpm` in MISE_DISABLE_TOOLS unless a declared `mise_tools` key IS `pnpm`
// (config.MergeMiseDisabledTools, docs/design/program-delivery.md's pnpm note under
// OQ-PD12a). That is right only because mise matches the list against a tool's KEY as
// written: `pnpm` hides a `pnpm` key and leaves `npm:pnpm` and `aqua:pnpm/pnpm` listed
// (MEASURED 2026-10-01, mise 2026.8.6). The jail withholds yolo's pnpm launcher for all three
// spellings, so a mise that matched the list against the binary a key provides would leave a
// declared `npm:pnpm` with no pnpm at all, and no unit test could say so: the unit cells
// (internal/cli/run/misepnpm_test.go) stop at the argv and the launcher directory.
//
// Nothing is installed. `mise ls --current` lists a declared tool whether or not it is
// present, so the cell reads mise's verdict without a download.

// TestTheImageMiseHidesOnlyTheBarePnpmKey launches a jail declaring no mise pnpm, so the
// list carries yolo's `pnpm`, and asks the jail's mise which of three spellings that list
// hides, under the MISE_DISABLE_TOOLS the launch argv actually gave the jail.
func TestTheImageMiseHidesOnlyTheBarePnpmKey(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)

	script := strings.Join([]string{
		`echo "=== DISABLED ==="`,
		`echo "$MISE_DISABLE_TOOLS"`,
		`echo "=== LISTED ==="`,
		`d=$(mktemp -d)`,
		`printf '[tools]\npnpm = "9.12.0"\n"npm:pnpm" = "9.12.0"\n"aqua:pnpm/pnpm" = "9.12.0"\n' > "$d/config.toml"`,
		// The scratch dir is the cwd so no project mise.toml joins the answer.
		`(cd "$d" && MISE_GLOBAL_CONFIG_FILE="$d/config.toml" mise ls --current 2>/dev/null | awk '{print $1}')`,
		`echo "=== END ==="`,
	}, "; ")

	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("probe script failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}

	disabled := strings.Split(strings.TrimSpace(section(r.stdout, "=== DISABLED ===", "=== LISTED ===")), ",")
	if !slices.Contains(disabled, "pnpm") {
		t.Fatalf("a jail declaring no mise pnpm has MISE_DISABLE_TOOLS=%q, without yolo's pnpm: "+
			"the probe below would measure nothing", strings.Join(disabled, ","))
	}

	listed := strings.Fields(section(r.stdout, "=== LISTED ===", "=== END ==="))
	if slices.Contains(listed, "pnpm") {
		t.Errorf("mise lists a bare `pnpm` key under MISE_DISABLE_TOOLS=%s, so the list hides "+
			"nothing and this probe is not measuring what it claims (listed: %v)",
			strings.Join(disabled, ","), listed)
	}
	for _, key := range []string{"npm:pnpm", "aqua:pnpm/pnpm"} {
		if !slices.Contains(listed, key) {
			t.Errorf("the image's mise hides a declared %q under MISE_DISABLE_TOOLS=%s "+
				"(listed: %v). The host lifts pnpm from that list only for a bare `pnpm` "+
				"key, and the jail writes no pnpm launcher for %q, so a jail declaring it "+
				"would have no pnpm at all: config.MergeMiseDisabledTools must match "+
				"what this mise matches", key, strings.Join(disabled, ","), listed, key)
		}
	}
}
