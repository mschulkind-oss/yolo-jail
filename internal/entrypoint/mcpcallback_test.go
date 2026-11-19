package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedMCPCallbackPublishedPortLocalnetUsesExactTCPRule(t *testing.T) {
	for _, owner := range []bool{true, false} {
		name := "nonowner"
		if owner {
			name = "owner"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			log := filepath.Join(root, "iptables-argv")
			stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$YOLO_TEST_CALLBACK_IPTABLES_LOG\"\n"
			if err := os.WriteFile(filepath.Join(root, "iptables"), []byte(stub), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", root)
			t.Setenv("YOLO_TEST_CALLBACK_IPTABLES_LOG", log)
			e := siblingEnv(t, nil)
			if owner {
				e.Vars["YOLO_PUBLISHED_PORTS"] = `["3118/tcp"]`
			}
			setupPublishedPortLocalnet(e)
			body, err := os.ReadFile(log)
			if !owner {
				if !os.IsNotExist(err) {
					t.Fatal("nonowner attempted DNAT", err)
				}
				return
			}
			if err != nil || string(body) != "-t nat -A PREROUTING -p tcp --dport 3118 -j DNAT --to-destination 127.0.0.1:3118\n" {
				t.Fatalf("wrong actual DNAT argv: %s %v", body, err)
			}
		})
	}
}

func TestManagedMCPCallbackLocalnetFailureOffersFullPasteWithoutRefusal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "iptables"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	e := siblingEnv(t, map[string]string{"YOLO_PUBLISHED_PORTS": `["3118/tcp"]`})
	var warnings bytes.Buffer
	e.Stderr = &warnings
	setupPublishedPortLocalnet(e)
	if !strings.Contains(warnings.String(), "full callback URL") || !strings.Contains(warnings.String(), "yolo check") {
		t.Fatal(warnings.String())
	}
}
