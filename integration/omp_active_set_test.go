package integration

// omp_active_set_test.go is the integration tier of oh-omp holding an ACTIVE SET
// (docs/design/active-provider-sets.md AP-D18; the active set, a term that doc coins, is the
// ordered list of profiles one agent runs on for one launch): `-p oh-omp=zai,openrouter` reaches
// omp's own files through a real launch — config resolution, the -p grammar, pack staging, the
// credential gate and the jail's boot render. Until omp's pack declared provider_sets, the same
// launch was refused. The unit pin is internal/entrypoint/ompactiveset_test.go (the render); only
// a launch proves the list crosses into the jail and each entry's key reaches omp alone. No agent
// runs: selecting a pack renders its files and installs nothing, and the jailed command is a
// shell.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
)

func TestOmpRunsOnEveryProviderOfItsSet(t *testing.T) {
	requireJail(t)
	// Both providers' keys, since the credential pre-flight demands every entry's (AP-D3),
	// hydrated through env_sources, the channel the credential gate delivers into an agent's own
	// env file. The shell's own copies are blanked, so the bare-shell assertion reads what yolo
	// delivered.
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["omp", "zai", "openrouter"], "env_sources": [`+
		`{"ZAI_API_KEY": "integration-probe-not-a-real-key", "OPENROUTER_API_KEY": "integration-probe-not-a-real-key"}]}`)
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "oh-omp=zai,openrouter", "--", "bash", "-lc",
		`printf 'shell zai=%s router=%s\n' "${ZAI_API_KEY:+set}" "${OPENROUTER_API_KEY:+set}"; `+
			`f=~/.config/yolo-agent-env/oh-omp.sh; if [ -r "$f" ]; then . "$f"; fi; `+
			`printf 'omp zai=%s router=%s\n' "${ZAI_API_KEY:+set}" "${OPENROUTER_API_KEY:+set}"`))
	if r.rc != 0 {
		t.Fatalf("the set launch failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Active set for oh-omp: zai, openrouter") {
		t.Errorf("the launch must name oh-omp's set in order:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "omp zai=set router=set\n") {
		t.Errorf("oh-omp's own environment must carry both entries' keys:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "shell zai= router=\n") {
		t.Errorf("a bare shell must carry neither of the set's keys:\n%s", r.combined())
	}

	// Each entry is a catalog row naming its own key's variable, never a value.
	decoded, err := (codec.YAML{}).Decode(renderedSurface(t, dir, "oh-omp", "agent", "models.yml"))
	if err != nil {
		t.Fatalf("omp's models.yml is not YAML: %v", err)
	}
	m, _ := decoded.(map[string]any)
	rows, _ := m["providers"].(map[string]any)
	for name, key := range map[string]string{"zai": "ZAI_API_KEY", "openrouter": "OPENROUTER_API_KEY"} {
		row, _ := rows[name].(map[string]any)
		if row == nil || row["apiKey"] != key {
			t.Errorf("models.yml %s row = %v, want apiKey %s", name, row, key)
		}
	}
}

// A CARRIED ENTRY AFTER THE FIRST IS REFUSED (AP-D18): oh-omp has no Bedrock client, so plain
// `bedrock` reaches it only through the wire bridge's route for oh-omp, whose upstream is the first
// entry's provider. Before the refusal, `-p oh-omp=zai,bedrock` started, printed bedrock as live in
// the set, and rendered no bedrock row, so the session ran on zai alone. claude is selected because
// its pack brings the wire bridge in; the launch refuses before any container starts.
func TestOmpRefusesACarriedEntryAfterItsFirst(t *testing.T) {
	requireJail(t)
	t.Setenv("ZAI_API_KEY", "")
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "omp", "zai"], "providers": {"bedrock": {"region": "us-east-1"}}, `+
		`"env_sources": [{"ZAI_API_KEY": "integration-probe-not-a-real-key"}]}`)
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "oh-omp=zai,bedrock", "--", "true"))
	if r.rc == 0 {
		t.Fatalf("a carried entry after the first must refuse the launch:\n%s", r.combined())
	}
	for _, want := range []string{`profile "bedrock" (entry 2 of oh-omp's profiles: zai, bedrock)`,
		"-p oh-omp=bedrock,zai"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, r.combined())
		}
	}
}
