package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestPrivateOfficialProducerReachesRealHostPreparationWorkers(t *testing.T) {
	requireJail(t)
	privateOfficialMachineHome(t)
	dir := writeProject(t, `{}`)
	writeUserTOML(t, "packs = [\"opencode\"]\nagent_updates = false\n")
	producer := withPrivateOfficialProducer(t)
	for _, fresh := range []bool{false, true} {
		args := jailRunArgs()
		if fresh {
			args = append(args, "--update")
		}
		args = append(args, "--", "bash", "-lc",
			`printf '%s' "$YOLO_PROGRAM_CAPTURES" | jq -e '.opencode.declared == "opencode-ai" and (.opencode.compatibility | test("^[0-9a-f]{64}$"))' >/dev/null || exit 90; `+
				`command -v opencode; opencode --version`)
		r := runCommand(t, dir, args, producer, withHostSemantics())
		if r.rc != 0 || !strings.Contains(r.stdout, "/.yolo/bin/launch/opencode\n") || !strings.Contains(r.stdout, `PRIVATE_OFFICIAL opencode 0.0.0-fixture ["--version"]`) {
			t.Fatalf("real host preparation fresh=%v did not deliver the inert official fixture: rc %d\n%s", fresh, r.rc, r.combined())
		}
		if strings.Contains(r.combined(), "foreground-only installation preparation") {
			t.Fatalf("fresh=%v used foreground fallback instead of the real worker:\n%s", fresh, r.combined())
		}
	}
	// Only the actual sharedPreparationSpawn opens this detached worker log. The
	// standalone driver never substitutes that spawn or runs an in-process worker.
	log, err := os.ReadFile(filepath.Join(paths.GlobalStorage(), "logs", "background-advance.log"))
	if err != nil || !strings.Contains(string(log), "capture-run:") {
		t.Fatalf("the real self-exec preparation worker did not log its normal capture producer: %v\n%s", err, log)
	}
}
