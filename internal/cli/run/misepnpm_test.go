package run

// misepnpm_test.go pins the one name two delivery classes contend for: pnpm
// (docs/design/program-delivery.md §3.5, the "pnpm is a project dependency" note, and the
// OQ-PD12a ruling that "a `mise_tools` pnpm keeps its name").
//
// TWO HALVES DECIDE WHO DELIVERS PNPM, AND THEY LIVE ON OPPOSITE SIDES OF THE CONTAINER.
// The HOST writes MISE_DISABLE_TOOLS onto the argv (assembleRunCmd), and mise neither lists,
// installs nor shims a tool named there. The JAIL decides whether to write yolo's lazy
// `pnpm@latest` launcher (entrypoint.GeneratePackageManagerLaunchers), and withholds it for a
// declared mise tool. Each half was right on its own and the pair was wrong: the host hid
// pnpm from mise on every launch while the jail stood its launcher down, so a workspace that
// declared `mise_tools` pnpm got no pnpm at all.
//
// So each cell drives BOTH production call sites, the argv's own values crossing to the jail
// half the way the container env carries them, and asserts on the outcome neither half sees
// alone.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// pnpmDoors launches cfg and reports the two halves' answers: the tool keys the argv's
// MISE_DISABLE_TOOLS hides from mise, and whether the jail wrote yolo's pnpm launcher from
// the env that argv carries.
func pnpmDoors(t *testing.T, cfg *jsonx.OrderedMap) (miseHides []string, launcherWritten bool) {
	t.Helper()
	argv := assembleWithConfig(t, cfg)
	disabled, ok := envValue(argv, "MISE_DISABLE_TOOLS")
	if !ok {
		t.Fatal("the launch argv carries no MISE_DISABLE_TOOLS")
	}
	miseTools, ok := envValue(argv, "YOLO_MISE_TOOLS")
	if !ok {
		t.Fatal("the launch argv carries no YOLO_MISE_TOOLS")
	}

	home := t.TempDir()
	e := entrypoint.NewEnv(map[string]string{
		"JAIL_HOME":          home,
		"HOME":               home,
		"MISE_DATA_DIR":      filepath.Join(home, "mise"),
		"YOLO_MISE_TOOLS":    miseTools,
		"MISE_DISABLE_TOOLS": disabled,
	})
	// The generator switches this process to tolerant manifest reads, as the boot does.
	t.Cleanup(packload.OverrideSkewTolerance(false))
	// The image's bin dirs are an empty folder, not this machine's /bin and /usr/bin: a host
	// with pnpm installed system-wide would otherwise withhold the launcher for that reason.
	t.Cleanup(entrypoint.OverrideImageProbeBase(t.TempDir()))
	if err := entrypoint.GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatalf("GeneratePackageManagerLaunchers: %v", err)
	}
	_, err := os.Stat(filepath.Join(e.LaunchDir(), "pnpm"))
	return strings.Split(disabled, ","), err == nil
}

// TestADeclaredMisePnpmIsDeliveredByMise is the defect: every spelling of a declared mise
// pnpm gets no yolo launcher (OQ-PD12a), so the launch must not tell mise to hide it.
//
// mise matches MISE_DISABLE_TOOLS against a tool's KEY as written, not the binary it provides:
// MEASURED 2026-10-01 with mise 2026.8.6, `pnpm` in the list hides a `pnpm` key from
// `mise ls --current` and leaves `npm:pnpm` and `aqua:pnpm/pnpm` listed. So the bare key is
// the spelling that lost pnpm outright, and the assertion that names the declared key holds
// for every spelling. For the backend spellings the list keeps pnpm, as it always did: there
// it hides only a project's own bare `pnpm` pin, and whether it should is OQ-PD19's
// question, which a fix that lifted it on the binary name would answer for them.
func TestADeclaredMisePnpmIsDeliveredByMise(t *testing.T) {
	for _, c := range []struct {
		key       string
		listsPnpm bool
	}{
		{"pnpm", false},
		{"npm:pnpm", true},
		{"aqua:pnpm/pnpm", true},
	} {
		t.Run(c.key, func(t *testing.T) {
			tools := jsonx.NewOrderedMap()
			tools.Set(c.key, "9.12.0")
			cfg := bareConfig()
			cfg.Set("mise_tools", tools)

			hides, launcher := pnpmDoors(t, cfg)
			if launcher {
				t.Errorf("yolo wrote its pnpm@latest launcher over a declared mise_tools %q: "+
					"an agent-class launcher shadowing a project dependency (OQ-PD12a)", c.key)
			}
			if slices.Contains(hides, c.key) {
				t.Errorf("the launch hides the declared mise_tools %q from mise "+
					"(MISE_DISABLE_TOOLS=%s) while the jail withholds yolo's launcher for it, "+
					"so the jail has no pnpm at all", c.key, strings.Join(hides, ","))
			}
			if got := slices.Contains(hides, "pnpm"); got != c.listsPnpm {
				t.Errorf("for a declared mise_tools %q, MISE_DISABLE_TOOLS=%s names pnpm: %v, "+
					"want %v. The list hides only the bare key, so it is lifted for that key "+
					"alone; lifting it for another spelling would stop hiding a project's own "+
					"pnpm pin, which OQ-PD19 has not decided",
					c.key, strings.Join(hides, ","), got, c.listsPnpm)
			}
		})
	}
}

// TestAnUndeclaredPnpmIsStillYolos is the other arm, and it is what OQ-PD19 still owns: with
// no mise declaration yolo's launcher delivers pnpm and mise is kept off the name. A fix that
// dropped pnpm from MISE_DISABLE_TOOLS on every launch would pass the cell above and change
// what a project's own mise.toml pin of pnpm resolves to, which no ruling has decided.
func TestAnUndeclaredPnpmIsStillYolos(t *testing.T) {
	tools := jsonx.NewOrderedMap()
	tools.Set("node", "24")
	cfg := bareConfig()
	cfg.Set("mise_tools", tools)

	hides, launcher := pnpmDoors(t, cfg)
	if !launcher {
		t.Error("no pnpm launcher in a jail that declares no mise pnpm: the lazy install " +
			"is how such a jail gets pnpm at all")
	}
	if !slices.Contains(hides, "pnpm") {
		t.Errorf("MISE_DISABLE_TOOLS=%s no longer names pnpm in a jail where yolo's launcher "+
			"delivers it; whether a project's mise pin should win there is OQ-PD19's "+
			"question, not this fix's", strings.Join(hides, ","))
	}
}
