package openaiauthhost

// operator.go is the HUMAN's door to the machine-wide OpenAI grant: `status`, `import` and
// `logout`, run as the host user against the daemon's private socket.
//
// # Why it lives here and not in internal/cli
//
// The two mutations are served on ONE socket — the private 0600 one, by a handler
// serveSockets gives to that path alone (openaiauthdaemon's two handlers; a `Session.JailID`
// check could never do this job, because on that socket the field falls back to the client's
// own self-asserted value). So a caller needs the socket's PATH, and getting it means
// resolving the singleton's layout and making sure the daemon is even running. This package
// already does exactly that for a managed `yolo host -- codex` launch (ensureSingleton), and
// the client cannot: openaiauthhost imports openauthclient, so the dependency only runs one
// way. One resolver, two callers.
//
// # It is a HIDDEN verb today, and that is a gap rather than a decision
//
// `yolo internal openai-auth <status|import|logout>` is one registry row away from being
// `yolo openai-auth`, and openai-auth-broker-plan.md's step 11 is right that logout is a thing
// users type — a hidden path is not where a destructive, machine-wide operation should be
// discovered. What it costs is three shared tables in internal/cli (the dispatch registry, the
// `--help` list, the subcommand-usage map) plus the self-documenting-CLI standard's tests, and
// that promotion is the remaining half of this row.

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
)

// operatorUsage is the verb's own help, so a mistyped subcommand answers with the three that
// exist rather than with a client-level "unknown command".
const operatorUsage = `usage: yolo internal openai-auth <status|import|logout>

  status                    what the machine-wide grant is (fingerprints only, never tokens)
  import --from <auth.json> install a Codex login as the canonical grant, machine-wide
  logout                    delete the canonical grant, machine-wide

Every one of these runs against the host's PRIVATE credential socket, which is why they are
unavailable from inside a jail (the jail-facing door refuses them).`

// RunOperator resolves the private socket — starting the machine-wide daemon if it is not
// already up — states what a mutation is about to do, and delegates the wire work to the one
// client.
//
// THE DELEGATION IS THE POINT. It hands openauthclient.Run a getenv that answers with the
// resolved socket and with NOTHING for the jail endpoint, so the request goes through the same
// flag parsing, the same framing, the same JSON compaction and the same exit-code mapping as
// every other client call. Composing the request here instead would be a second client, and
// the first thing it would drift on is which door it dials.
func RunOperator(args []string, stdout, stderr io.Writer) int {
	return runOperator(args, ensureSingleton, stdout, stderr)
}

// runOperator is the body with the singleton resolver injected, the seam `prepare` already
// uses for the same dependency: a test must be able to exercise the whole verb — the usage
// refusals, the machine-wide notice, the delegation and the door it dials — against a socket
// it served itself, without spawning the host daemon.
func runOperator(args []string, ensure func(io.Writer) (string, error), stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, operatorUsage)
		return 2
	}
	switch args[0] {
	case "status", "import", "logout":
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, operatorUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "yolo internal openai-auth: unknown command %q\n\n%s\n", args[0], operatorUsage)
		return 2
	}
	// STATED BEFORE IT HAPPENS, not after — the same rule the launch's exec disclosure
	// follows: after the fact a line is a notification that something already happened. It is
	// a disclosure and not a prompt, because the user typed the verb; what they may not know
	// is that its scope is the whole machine rather than this terminal or this workspace.
	if line := machineWideNotice(args[0]); line != "" {
		fmt.Fprintln(stderr, line)
	}
	socket, err := ensure(stderr)
	if err != nil {
		fmt.Fprintln(stderr, "yolo internal openai-auth:", err)
		return 1
	}
	return openauthclient.Run(args, func(name string) string {
		if name == openauthclient.HostSocketEnv {
			return socket
		}
		// Deliberately not the endpoint variable, even if this process inherited one: this
		// verb is the HOST's, and a jail's door does not serve it.
		return ""
	}, stdout, stderr)
}

func machineWideNotice(action string) string {
	switch action {
	case "logout":
		return "Logging out removes the MACHINE-WIDE OpenAI grant: every workspace, every jail " +
			"and this host user lose it until someone logs in again."
	case "import":
		return "Importing replaces the MACHINE-WIDE OpenAI grant for every workspace and every " +
			"jail with the credential in the file named below."
	default:
		return ""
	}
}
