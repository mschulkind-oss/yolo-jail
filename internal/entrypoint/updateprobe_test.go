package entrypoint

// updateprobe_test.go pins a pack's `update_probes` entry (packdecl.Install.UpdateProbeURL): a failed update verb is judged
// by the host the verb downloads from, not by the installer's. claude's `claude install` downloads from
// downloads.claude.ai; with that host failing and claude.ai's installer answering, a launch was refused as if
// a newer version were waiting, when it could not have checked at all.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestAFailedVerbIsProbedAtItsOwnHostNotTheInstallers(t *testing.T) {
	for _, tc := range []struct {
		name        string
		probe       string
		wantRefused bool
	}{
		{"its download host is down", silentHost(t), false},
		{"its download host answers", answeringHost(t), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The installer's host ANSWERS in both cases: only the probe's own host decides.
			p := newUpdateProbeAt(t, []string{"install"}, true, true, answeringHost(t))
			launcher := nativeAgentLauncher("probe",
				&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: answeringHost(t),
					UpdateVerb: []string{"install"}, UpdateProbeURL: tc.probe},
				p.stamps, filepath.Join(p.home, "ws", ".yolo", "receipts.jsonl"), "", true, launcherServers{}, nil)
			if err := os.WriteFile(p.script, []byte(launcher), 0o755); err != nil {
				t.Fatal(err)
			}
			body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n" +
				"if [ \"${1:-}\" = install ]; then exit 1; fi\n"
			if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			out, rc := p.run(t)
			if tc.wantRefused {
				if rc != UpdateRefusedStatus || !strings.Contains(out, "Its upstream answered") {
					t.Errorf("an answering download host must refuse (rc=%d):\n%s", rc, out)
				}
				return
			}
			if rc != 0 || !strings.Contains(out, "could not be reached to check for a newer version — running what is installed") {
				t.Errorf("a download host that is down must run the installed version, saying so (rc=%d):\n%s", rc, out)
			}
		})
	}
}

// THE SHIPPED claude PACK PROBES ITS UPDATER'S HOST, which the launcher above reads: without this declaration a
// down downloads.claude.ai refuses every launch while claude.ai answers.
func TestTheClaudePackProbesTheHostClaudeInstallDownloadsFrom(t *testing.T) {
	for _, p := range packload.Embedded() {
		if p.Name != "claude" {
			continue
		}
		for _, in := range p.Decl.InstallContributions() {
			if in.Bin != "claude" {
				continue
			}
			if !strings.HasPrefix(in.UpdateProbeURL, "https://downloads.claude.ai/") {
				t.Errorf("claude's update probe = %q, want the downloads.claude.ai host `claude install` fetches from",
					in.UpdateProbeURL)
			}
			return
		}
	}
	t.Fatal("the claude pack no longer ships a claude program; move or drop this pin")
}
