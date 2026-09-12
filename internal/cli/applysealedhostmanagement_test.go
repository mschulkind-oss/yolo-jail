package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplySealedRefusesAnUnsetHostManagement is §4.3 item 3: an environment whose
// host-ownership contract is unstated is not sealed.
//
// IT PINS THE CALL SITE. Deleting the `config.HostManagementDeclared()` branch from
// applySealed leaves every other sealed test green — the clean-workspace case would simply
// seal, which is what it asserted before this branch existed. So the shape here is:
// everything else is clean, the key is the only thing outstanding, and the refusal must
// still fire and must name the key.
//
// ⚠ The message must say UNSET, not "undeclared". §4.3's note reserves *undeclared* for the
// input-closure tier — a value inside an agent's config file that nothing names, whose remedy
// is to promote it. This is a yolo config key with no value at all, whose remedy is to write
// it, and the two appear in the same refusal list four lines apart.
func TestApplySealedRefusesAnUnsetHostManagement(t *testing.T) {
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude"]}`)
	writeFile(t, filepath.Join(repo, ".yolo", "keep"), "x")
	userCfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")

	// (1) No user config at all: the key is unset, and that alone refuses.
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("an unset host_management should refuse (rc 1), got %d: %s%s",
			rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "host_management") ||
		!strings.Contains(out.String(), "unset") {
		t.Errorf("the refusal must name the key and say it is UNSET:\n%s", out.String())
	}
	if strings.Contains(out.String(), "yolo-jail.local.jsonc") ||
		strings.Contains(out.String(), "captured in-jail edit") {
		t.Errorf("nothing else is outstanding; only host_management may refuse here:\n%s",
			out.String())
	}

	// (2) A user config that does not carry the key: still unset, still refused. The
	// distinction matters — a file exists, so "there is no config" is not the reason.
	writeFile(t, userCfg, `{"packs":["claude"]}`)
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("a user config without the key should still refuse (rc 1), got %d: %s%s",
			rc, out.String(), errw.String())
	}

	// (3) Any of the three values DECLARES it, including the one that equals the default.
	// Writing "assert" changes no behavior anywhere else, which is exactly why --sealed is
	// the only place the unset state can bite.
	for _, v := range []string{"none", "assert", "own"} {
		writeFile(t, userCfg, `{"host_management":"`+v+`"}`)
		out.Reset()
		errw.Reset()
		if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("host_management %q is declared; want sealed (rc 0), got %d: %s%s",
				v, rc, out.String(), errw.String())
		}
	}

	// (4) An unreadable user config cannot PROVE a declaration, so it refuses too.
	if err := os.WriteFile(userCfg, []byte(`{"host_management": "own`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("an unreadable user config should refuse (rc 1), got %d: %s%s",
			rc, out.String(), errw.String())
	}
}
