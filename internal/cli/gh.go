package cli

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
)

// gh.go is `yolo gh`, the jail side of the github-broker (docs/design/boundary-broker.md
// §4.3). packs/github's `intercept` contribution renders a bare `gh` in the jail as
// `exec yolo gh -- "$@"`, so this is what an agent's `gh pr view` runs.
//
// MAIN ROUTES IT BEFORE THE FRONT DOOR'S OWN WORK (cli.go), and that is the point: this
// verb's stdout and stderr are gh's, carried back from the host verbatim (OQ-C), so the
// startup banner, the update notice and the global-flag parsing every other verb gets
// would each put yolo's words — or take a flag such as gh's own `-v` — into another
// program's output. The registry row is here so `yolo --help` lists it and `yolo gh --help`
// answers; the dispatch never reaches it through dispatchNative.

const ghUsage = `Usage: yolo gh [--] <gh arguments>

In a jail: run a gh command through the github-broker, which runs the host's own
gh login on this jail's behalf and returns its output. The jail holds no GitHub
credential. With the ` + "`github`" + ` pack selected, a bare ` + "`gh`" + ` in the jail is this.

This version is read-only: commands in the read-only set (pr view, pr list,
issue view, run view, workflow list, api GET under repos/OWNER/REPO, …) run
against this workspace's own GitHub repositories; every write exits 77, "writes
need approval, which this version cannot ask for". Commands that could print the
credential or reach the host (auth token, --web, api to a URL, a host file, …)
are refused with exit 64, as is anything outside the workspace's repositories.
--jq and --template work as they do in gh.

The repository is -R OWNER/REPO, or this workspace's origin remote. Standard
input is sent when the arguments read it (` + "`--body-file -`" + `).

Exit codes: gh's own, plus 64 refused or out of scope, 69 no broker or no
host login, 77 needs an approval this version cannot ask for.

Flags:
  --help, -h    Show this help (as the first argument, before any ` + "`--`" + `).

Examples:
  gh pr view 32                       # through the broker, as the agent types it
  yolo gh -- pr list -R owner/repo    # the same verb, spelled out
  yolo audit                          # on the host: what the broker ran`

// runGHVerb is the registry handler, which like every handler receives the verb itself
// first. Main calls runGH directly with the arguments after it (see the file header).
func runGHVerb(args []string) int { return runGH(args[1:]) }

// runGH forwards one gh call.
//
// Only a FIRST argument of --help or -h is this verb's own help: anywhere later it is gh's
// (`gh pr view --help`), and `help` is a gh command of its own (`gh help formatting`).
func runGH(args []string) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		os.Stdout.WriteString(ghUsage + "\n")
		return 0
	}
	return ghbroker.Forward(args, ghbroker.ForwardEnv{
		Getenv: os.Getenv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
	})
}
