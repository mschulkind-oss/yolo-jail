package packload_test

// chromedevtoolspack_test.go pins the shipped chrome-devtools pack
// (docs/design/mcp-presets-removal.md §13 step 2): its `mcp` entry runs the very file its `files`
// contribution delivers, through /bin/sh, joined to whatever home the notch renders for; the
// program the entry names is the one the pack installs; and the jail-only chrome flags ride the
// AUTONOMOUS posture alone, so the guarded host notch never gets them.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs
)

func chromeDevtoolsPack(t *testing.T) *packload.Pack {
	t.Helper()
	for _, p := range packload.Embedded() {
		if p.Name == "chrome-devtools" {
			return p
		}
	}
	t.Fatal("no embedded chrome-devtools pack")
	return nil
}

func TestTheChromeDevtoolsEntryRunsTheWrapperThePackDelivers(t *testing.T) {
	p := chromeDevtoolsPack(t)
	servers := p.Decl.MCPContributions()
	if len(servers) != 1 || servers[0].Name != "chrome-devtools" || servers[0].Bin != "chrome-devtools-mcp" {
		t.Fatalf("mcp contributions = %+v", servers)
	}
	var into, from string
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles {
			into, from = c.Into, c.From
		}
	}
	if into == "" {
		t.Fatal("the pack delivers no wrapper")
	}
	if _, err := os.Stat(filepath.Join(p.Root, from)); err != nil {
		t.Errorf("the wrapper the pack delivers is not in its tree: %v", err)
	}
	for _, home := range []string{"/home/agent", "/Users/yolo-sandbox", "/home/you"} {
		table := packload.ComposeMCPServers(nil, []*packload.Pack{p}, home)
		v, _ := table.Get("chrome-devtools")
		entry, _ := v.(interface{ Get(string) (any, bool) })
		if entry == nil {
			t.Fatalf("home %s: no chrome-devtools entry in %v", home, table)
		}
		// /bin/sh, because a pack selected by its bare name carries no exec bit (embed.FS).
		if cmd, _ := entry.Get("command"); cmd != "/bin/sh" {
			t.Errorf("home %s: command = %v, want /bin/sh", home, cmd)
		}
		args, _ := entry.Get("args")
		if list, _ := args.([]any); len(list) != 1 || list[0] != home+"/"+into {
			t.Errorf("home %s: args = %v, want [%s/%s] — the entry must run the file the pack's "+
				"`files` contribution delivers", home, args, home, into)
		}
	}
	installs, _ := p.HonoredInstalls()
	if len(installs) != 1 || installs[0].Bin != "chrome-devtools-mcp" || installs[0].Kind != "npm" ||
		installs[0].Package != "chrome-devtools-mcp" || installs[0].NodeFloor == "" {
		t.Errorf("the program the entry names is not the npm chrome-devtools-mcp with a Node floor: %+v", installs)
	}
}

// Decision b (docs/design/mcp-presets-removal.md): the sandbox flags are a container's facts,
// selected by the notch's posture and never detected at run time — an MCP client scrubs the
// environment a detection would read.
func TestTheChromeDevtoolsJailFlagsAreTheAutonomousPostures(t *testing.T) {
	p := []*packload.Pack{chromeDevtoolsPack(t)}
	jail := strings.Join(packload.LaunchFlagsFor(p, true)["chrome-devtools-mcp"], " ")
	for _, want := range []string{"--headless", "--isolated", "--chrome-arg=--no-sandbox",
		"--chrome-arg=--disable-setuid-sandbox", "--chrome-arg=--disable-gpu"} {
		if !strings.Contains(jail, want) {
			t.Errorf("the jail posture's flags %q lack %s", jail, want)
		}
	}
	if host := packload.LaunchFlagsFor(p, false)["chrome-devtools-mcp"]; len(host) > 0 {
		t.Errorf("the guarded (host) posture adds %v: Chrome keeps its own sandbox at the host", host)
	}
}
