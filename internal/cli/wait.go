package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/hostrun"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// wait.go is `yolo wait`, the ONE waiter an agent starts after handing the human something to
// do on the host: an approval request a broker refused naming (`r-<id>`, whose outcome the
// host writes under the workspace's .yolo/approvals, so a wait started in a later jail still
// reads it), or a `yolo host-run` line's label (the mailbox, internal/hostrun). With no
// argument it lists what is still waiting on the agent. `yolo host-run wait --name LABEL`
// stays as the label form's alias. MAIN ROUTES IT BEFORE THE FRONT DOOR (cli.go): an agent
// reads its output and its exit status, and the banner or an update notice would be noise in
// both.

const waitUsage = `Usage: yolo wait [-C DIR] [--timeout DURATION] [r-<id> | LABEL]

In a jail, wait for the human's answer to something handed to the host, and
exit with what it was. Start it in the background, in the same turn as you
relay the human's commands, so that its exit wakes you.

  yolo wait r-<id>   An approval request a git or gh refusal named. Waits until
                     the host answers it, up to the request's own expiry.
  yolo wait LABEL    A ` + "`yolo host-run --name LABEL`" + ` line: waits for its result and
                     prints it (the same as ` + "`yolo host-run wait --name LABEL`" + `).
  yolo wait          Lists this workspace's requests that are pending, or were
                     answered and not acted on yet, and exits 0.

Exit status for a request:
  0   allowed: run the refused command again, once.
  75  still unanswered at the timeout: give the human the commands again and
      stop; once they say they have answered, run ` + "`yolo wait r-<id>`" + ` again.
  72  allowed, but already used or its window closed.
  73  another ` + "`yolo wait`" + ` already waits on this request.
  77  denied.   78  expired unanswered.   79  closed when its broker stopped.
  64  usage.    69  no record of the request in this workspace.
Every status but 0 and 75 means: report what it printed, and do not retry.

For a label, the status is host-run's: 0 once the result arrived, whatever the
command's own exit; 69 when the host refused or stopped it; 75 at the timeout.

Flags:
  -C DIR              Find the workspace from DIR instead of this directory.
  --timeout DURATION  How long to wait (default: the request's expiry; 100m
                      for a label). Below your tool's own timeout when run in
                      the foreground.
  --help, -h          Show this help.

Examples:
  yolo wait r-0123456789abcdef0123456789abcdef   # in the background, after relaying
  yolo wait merge-42                            # a host-run line labeled merge-42
  yolo wait                                     # what is still waiting on you`

func runWaitVerb(args []string) int { return waitMain(args[1:]) }

// waitMain is `yolo wait` after the verb.
func waitMain(args []string) int {
	if answerHelp("wait", args, os.Stdout) {
		return 0
	}
	var a hostRunJailArgs
	target := ""
	fail := func(format string, v ...any) int {
		fmt.Fprintf(os.Stderr, "yolo wait: "+format+"; see `yolo wait --help`.\n", v...)
		return approvalwait.ExitUsage
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, val, glued := strings.Cut(arg, "=")
		switch name {
		case "-C", "--timeout":
			if !glued {
				if i+1 >= len(args) {
					return fail("%s needs a value", name)
				}
				i++
				val = args[i]
			}
			if name == "-C" {
				a.dir = val
				continue
			}
			d, err := time.ParseDuration(val)
			if err != nil || d <= 0 {
				return fail("the timeout %s is not a duration such as 30m", shquote.QuoteDisplay(val))
			}
			a.timeout = d
		default:
			if target != "" || strings.HasPrefix(arg, "-") {
				return fail("yolo wait does not take %s", shquote.QuoteDisplay(arg))
			}
			target = arg
		}
	}
	if target != "" && !approvalwait.LooksLikeID(target) {
		if err := hostrun.CheckLabel(target); err != nil {
			return fail("%s is neither a request id (r-<id>) nor a host-run label: %v", shquote.QuoteDisplay(target), err)
		}
		a.label = target
	}
	o, ok := hostRunJailOpts(a)
	if !ok {
		return approvalwait.ExitUsage
	}
	switch {
	case a.label != "":
		return hostrun.Wait(o)
	case target != "":
		return approvalwait.Wait(approvalwait.Opts{Workspace: o.Workspace, ID: target, Timeout: a.timeout})
	}
	code := approvalwait.List(approvalwait.Opts{Workspace: o.Workspace})
	fmt.Fprintln(os.Stdout, "A `yolo host-run --name LABEL` line handed to the human: `yolo wait LABEL` waits for its result, and `yolo host-run show` prints the newest one.")
	return code
}
