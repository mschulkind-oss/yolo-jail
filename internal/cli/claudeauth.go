package cli

// claudeauth.go is `yolo claude-auth`: the host operator's verbs for the machine's Claude login,
// the twin of `yolo openai-auth` (docs/design/claude-login-without-interception.md, OQ-CL2).
//
// One of them changes what every jail has: `logout` signs the whole machine out, which is why
// it is a host verb and why a jail's own /logout no longer does it. The rest are instruments —
// `status`, `inspect` and `refresh` — written for the credential-view measures
// (docs/plans/runbooks/claude-credential-view-measures.md), and none of them ever prints a
// token: field names, expiries and fingerprints only.
//
// The verb acts on the broker's store directly, under the refresh lock the daemon takes, rather
// than through the daemon's socket: the store is files the daemon re-reads before every
// decision, so there is no daemon state to ask and nothing a socket would add.

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
)

const claudeAuthUsage = `Usage: yolo claude-auth <status|inspect|refresh|logout>

Manage this machine's Claude subscription login: the ONE login the host broker
holds and that every workspace and every jail on it borrows. Nothing here prints a
token; fields, expiries and FINGERPRINTS only.

Subcommands:
  status              The machine's login (field names, expiry, fingerprints), the
                      shared file interception jails read, and every workspace's
                      credential view, including any a jail's /logout signed out.
  inspect <file>      The same facts for one credentials file, a view included.
                      A symbolic link is named and not followed.
  refresh             Refresh the machine's login now and rewrite every view.
                      Spends the current refresh token; for the measures runbook.
  logout              Sign the MACHINE out: every workspace and every jail loses the
                      login until someone runs /login in a jail again.

refresh flags:
  --view-delay <d>    Hold the rewritten views back for <d> (e.g. 2s, 10s) after
                      the refresh, to measure how Claude rides out the window.

  --help, -h          Show this help.

Examples:
  yolo claude-auth status
  yolo claude-auth inspect ~/code/proj/.yolo/home/claude/.credentials.json
  yolo claude-auth logout

refresh and logout act on the host's broker, so they refuse inside a jail. A
jail's own /logout signs out only that workspace, until its next launch.`

// runClaudeAuth is `yolo claude-auth`. args is the rewritten argv[1:], so args[0] is the verb.
func runClaudeAuth(args []string) int {
	if answerHelp("claude-auth", args, os.Stdout) {
		return 0
	}
	return claudeAuthMain(args[1:], config.InJail(), os.Stdout, os.Stderr)
}

// claudeAuthMain is the verb's body with its three inputs injected, so a test drives the whole
// verb against a temp HOME.
func claudeAuthMain(args []string, inJail bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, claudeAuthUsage)
		return 2
	}
	oauthbroker.ConfigureStore()
	switch args[0] {
	case "status":
		if err := oauthbroker.DescribeStore(stdout); err != nil {
			fmt.Fprintln(stderr, "yolo claude-auth status:", err)
			return 1
		}
		return 0
	case "inspect":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: yolo claude-auth inspect <file>")
			return 2
		}
		if err := oauthbroker.DescribeCredentialsFile(stdout, args[1]); err != nil {
			fmt.Fprintln(stderr, "yolo claude-auth inspect:", err)
			return 1
		}
		return 0
	case "refresh":
		if inJail {
			fmt.Fprintln(stderr, "yolo claude-auth refresh: run it on the host. In a jail it "+
				"would refresh this jail's own nested broker, not the machine's login.")
			return 2
		}
		fs := flag.NewFlagSet("claude-auth refresh", flag.ContinueOnError)
		fs.SetOutput(stderr)
		delay := fs.Duration("view-delay", 0, "hold the rewritten views back this long")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		oauth, err := oauthbroker.ForceRefresh(*delay)
		if err != nil {
			fmt.Fprintln(stderr, "yolo claude-auth refresh:", err)
			return 1
		}
		fmt.Fprintln(stdout, "Refreshed the machine's Claude login and rewrote every view.")
		oauthbroker.DescribeOAuth(stdout, "  ", oauth)
		return 0
	case "logout":
		if inJail {
			fmt.Fprintln(stderr, "yolo claude-auth logout: run it on the host. In a jail, "+
				"Claude's own /logout signs out this workspace until its next launch.")
			return 2
		}
		fmt.Fprintln(stderr, "Logging out removes the MACHINE-WIDE Claude login: every "+
			"workspace and every jail lose it until someone runs /login in a jail again.")
		res, err := oauthbroker.SignOut()
		if err != nil {
			fmt.Fprintln(stderr, "yolo claude-auth logout:", err)
			return 1
		}
		if !res.HadLogin {
			fmt.Fprintln(stdout, "This machine was already signed out of Claude.")
		} else {
			fmt.Fprintf(stdout, "Signed this machine out of Claude (%d credential view%s cleared).\n",
				res.Views, map[bool]string{true: "", false: "s"}[res.Views == 1])
		}
		return 0
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, claudeAuthUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "yolo claude-auth: unknown command %q\n\n%s\n", args[0], claudeAuthUsage)
		return 2
	}
}
