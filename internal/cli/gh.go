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

Reads run under a read grant. A write a grant or this workspace's working set
covers runs unasked; any other write files a pending request and does not run.
The refusal lists the human's answers, each on its own line, for them to run in
a host terminal directly (not through ` + "`yolo host-run`" + `):

    yolo approve <id>              allow that exact command once, bound to its
                                   repository and stdin digest
    yolo approve <id> --for 15m    allow that kind of write on that PR, issue
                                   or repository for 15 minutes

Start ` + "`yolo wait <id>`" + ` in the background meanwhile: it exits 0 once the request
is allowed, and then the same command runs. A standing ` + "`read-write`" + ` entry in
` + "`brokered.github.sets`" + ` is refused; remove it. A configured ` + "`on_request`" + ` hook is
optional and its output alone is never consent. Commands that could print the credential or reach the host
(auth token, --web, api to a URL, a host file, …) are refused with exit 64, as is anything outside
the workspace's repositories. --jq and --template work as they do in gh.

A repository joins the scope through the workspace config: add it to the
` + "`repos`" + ` list under ` + "`brokered.github`" + `, run ` + "`yolo check --no-build`" + `, and ask the user to
run ` + "`yolo config apply`" + ` on the host and approve the repository scope there.

The repository is -R OWNER/REPO, or this workspace's origin remote. Standard
input is sent when the arguments read it (` + "`--body-file -`" + `).

Exit codes: gh's own, plus 64 refused or out of scope, 69 no broker or no
host login, 75 the exact write awaits host approval and did not run, 77 host
policy refused or denied the write.

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
