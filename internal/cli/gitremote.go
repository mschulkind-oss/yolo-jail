package cli

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/gitremote"
)

// gitremote.go is `yolo git-remote`, the jail side of the git-broker
// (docs/reference/git-broker.md): git's remote helper `git-remote-yolo`, which the jail's
// git runs for every remote rewritten to `yolo::<url>`.
//
// MAIN ROUTES IT BEFORE THE FRONT DOOR'S OWN WORK (cli.go), as it does `gh`: this verb's
// stdout is git's remote-helper protocol, so the startup banner, the update notice or a
// global flag taken out of the argv would each corrupt the conversation git is having with
// it. The registry row is here so `yolo --help` lists it and `yolo git-remote --help`
// answers; the dispatch never reaches it through dispatchNative.

const gitRemoteUsage = `Usage: yolo git-remote [--] <remote> <url>

In a jail: git's remote helper for the git-broker, which reaches the host's
remotes with the host's own SSH key or credential helper on this jail's behalf.
The jail holds no git credential. git runs it itself, as git-remote-yolo, for a
remote whose URL starts with ` + "`yolo::`" + `; with the ` + "`git`" + ` pack selected and its
loophole on, the jail's git rewrites ssh://, https:// and git@ remotes that way.
It is not a command to type.

It offers git the ` + "`connect`" + ` capability only, and carries fetch
(git-upload-pack) and push (git-receive-pack). The broker decides what reaches
the remote: only the workspace's approved remote URLs, and a push only to refs
it may write.

Exit codes: the broker's own, plus 64 a command or service this helper does not
carry, 69 no broker endpoint in this jail.

Flags:
  --help, -h    Show this help (as the first argument, before any ` + "`--`" + `).

Examples:
  git fetch origin        # through the broker, as the agent types it
  yolo git-remote -- origin ssh://git@github.com/o/r
                          # what git runs for a yolo:: remote; it reads git's commands on stdin
  yolo audit              # on the host: what the broker ran`

// runGitRemoteVerb is the registry handler, which like every handler receives the verb
// itself first. Main calls runGitRemote directly with the arguments after it.
func runGitRemoteVerb(args []string) int { return runGitRemote(args[1:]) }

// runGitRemote runs the helper. Only a FIRST argument of --help or -h is this verb's own help.
func runGitRemote(args []string) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		os.Stdout.WriteString(gitRemoteUsage + "\n")
		return 0
	}
	return gitremote.Run(args, gitremote.Env{
		Getenv: os.Getenv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
	})
}
