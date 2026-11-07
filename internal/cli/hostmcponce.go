package cli

// hostmcponce.go is the one-sign-in tier at `yolo host` (docs/design/mcp-auth-sharing.md, OQ-MA10):
// NOT SERVED, and the launch says so. Every other backend adopts a sign_in = "once" server's
// sign-in from the machine-wide Claude store a jail bridges (CL-D22) and strips its refresh token
// there. At the host, Claude keeps its MCP sign-ins in the user's own ~/.claude (the macOS
// Keychain on a Mac), which OQ-NC7 ruled yolo never writes, so taking it over would rewrite the
// one store yolo promised to leave alone. Until OQ-MA10 is ruled, the launch names the server,
// says it signs in on its own here, and names the next step.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// hostOnceSignInNote prints the line when the launched program's own pack keeps MCP sign-ins
// (an [[mcp_auth]] declaration) and the user's config sets a server to sign in once.
func hostOnceSignInNote(launch *hostComposition, errw io.Writer) {
	if config.InJail() {
		return
	}
	pack := hostInstallingPack(launch.packs, launch.agent)
	if pack == nil || pack.Decl == nil || pack.Decl.MCPAuth == nil {
		return
	}
	names := run.OnceServerNames(launch.cfg)
	if len(names) == 0 {
		return
	}
	fmt.Fprintf(errw, "yolo host: MCP one sign-in is not kept at yolo host: %s signs in on its own here, "+
		"because %s keeps its MCP sign-ins in your own ~/.claude, which yolo does not write (OQ-MA10). "+
		"To use the one sign-in every jail shares, run %s in a jail with `yolo -- %s`\n",
		strings.Join(names, ", "), launch.agent, launch.agent, launch.agent)
}
