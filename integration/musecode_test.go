package integration

// musecode_test.go is the owner's report reproduced end to end in a real jail: with the
// muse-code pack selected, `muse` must start with the workspace trusted and in Muse's own yolo
// mode, and `muse exec …` (what the muse-planner wrapper and Muse's own skills run) must reach
// Muse with no root option in front of it — Muse refuses `muse --yolo exec` outright.
//
// The vendor binary is ~344 MiB and the question is yolo's half, so a fake `muse` stands at the
// path the vendor installer fills and logs the argv the jail's carrier hands it.

import (
	"strings"
	"testing"
)

func TestMuseCodeJailTrustsTheWorkspaceAndRunsMuseInYoloMode(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	// agent_updates false bakes the update branch out of the launcher, so the fake is run as
	// installed and no vendor installer is reached.
	packHome(t, `{"packs": ["muse-code"], "agent_updates": false}`)

	script := strings.Join([]string{
		`set -e`,
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/sh\nfor a in "$@"; do printf "arg=%%s\\n" "$a"; done\necho end\n' > "$HOME/.local/bin/muse"`,
		`chmod +x "$HOME/.local/bin/muse"`,
		`echo "=== TRUST ==="`,
		`cat "$HOME/.config/muse/trust.json"`,
		`echo "=== BARE ==="`,
		`bash -c 'muse'`,
		`echo "=== EXEC ==="`,
		`bash -c 'muse exec --prompt-file p'`,
		`echo "=== RESUME ==="`,
		`bash -c 'muse resume --last'`,
		`echo "=== ALIAS ==="`,
		`grep -c "alias muse=" "$HOME/.bashrc" || true`,
		`echo "=== END ==="`,
	}, "\n")
	r := runYolo(t, dir, script, withoutFixtureProgramInstall())
	if r.rc != 0 {
		t.Fatalf("probe script failed: rc %d\n%s", r.rc, r.combined())
	}

	trust := section(r.stdout, "=== TRUST ===", "=== BARE ===")
	if !strings.Contains(trust, `"/workspace"`) || !strings.Contains(trust, `"decision": "trusted"`) ||
		!strings.Contains(trust, `"schema_version": 1`+"\n") {
		t.Errorf("~/.config/muse/trust.json does not trust /workspace in the shape Muse reads, so "+
			"the trust dialog parks the launch:\n%s", trust)
	}
	for _, c := range []struct{ name, start, end, want string }{
		{"bare `muse`", "=== BARE ===", "=== EXEC ===", "arg=--yolo\nend\n"},
		{"`muse exec`", "=== EXEC ===", "=== RESUME ===", "arg=exec\narg=--prompt-file\narg=p\nend\n"},
		{"`muse resume`", "=== RESUME ===", "=== ALIAS ===", "arg=--yolo\narg=resume\narg=--last\nend\n"},
	} {
		got := strings.TrimLeft(section(r.stdout, c.start, c.end), "\n")
		if got != c.want {
			t.Errorf("%s in the jail ran muse with\n%s\nwant\n%s\n%s", c.name, got, c.want, r.combined())
		}
	}
	if alias := strings.TrimSpace(section(r.stdout, "=== ALIAS ===", "=== END ===")); alias != "0" {
		t.Errorf("the jail's .bashrc aliases muse (%s line(s)) — a fixed prefix that turns `muse "+
			"exec` at the prompt into `muse --yolo exec`, which Muse refuses", alias)
	}
}
