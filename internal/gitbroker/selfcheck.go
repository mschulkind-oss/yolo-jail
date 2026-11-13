package gitbroker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
)

// selfCheckTimeout bounds each version probe the self-check runs.
const selfCheckTimeout = 10 * time.Second

// SelfCheck is the loophole's `doctor_cmd` (`yolo internal daemon git-broker --self-check`,
// docs/reference/git-broker.md): it reports the host ssh and git the broker would run, found on
// the PATH it inherits and checked by the placement rule as a broker checks them. It fails only
// when there is no ssh it would run, since without one the broker reaches no remote; a host
// with no git still serves SSH remotes, so that is reported without failing.
func SelfCheck(w io.Writer) int {
	// `yolo check` runs this from the workspace, as a launch spawns the daemon, so the cwd is
	// checked as the workspace.
	cwd, _ := os.Getwd()
	pathEnv := os.Getenv("PATH")
	refuse := func(program string) func(string) string { return brokercore.PlacementRefusal(program, cwd) }

	ssh, err := lookPath("ssh", pathEnv)
	if err != nil {
		fmt.Fprintln(w, Name+": no ssh on the host's PATH, so the broker reaches no remote; install OpenSSH on the host")
		return 1
	}
	if why := refuse("ssh")(ssh); why != "" {
		fmt.Fprintln(w, Name+": "+why)
		return 1
	}
	version, err := probeVersion(ssh, "-V")
	if err != nil {
		fmt.Fprintln(w, Name+": host ssh "+ssh+" does not run (`ssh -V` failed: "+err.Error()+
			"), so the broker reaches no remote; repair or reinstall OpenSSH on the host, or put a working ssh first on the PATH yolo runs with")
		return 1
	}
	fmt.Fprintln(w, Name+": host ssh "+ssh+" ("+version+")")

	git, err := lookPath("git", pathEnv)
	switch {
	case err != nil:
		fmt.Fprintln(w, Name+": no git on the host's PATH; SSH remotes still work, and HTTPS remotes need git's credential helper: install git on the host")
	case refuse("git")(git) != "":
		fmt.Fprintln(w, Name+": "+refuse("git")(git))
	default:
		if version, err := probeVersion(git, "--version"); err != nil {
			fmt.Fprintln(w, Name+": host git "+git+" does not run (`git --version` failed: "+err.Error()+
				"); SSH remotes still work, and HTTPS remotes need it: repair or reinstall git on the host")
		} else {
			fmt.Fprintln(w, Name+": host git "+git+" ("+version+")")
		}
	}
	return 0
}

// probeVersion runs path with arg in an env of PATH and HOME alone, as the broker's own runs
// start from nothing, and returns the first line it printed. A run that fails is an error
// carrying that line, if any: the program does not run as the broker would run it.
func probeVersion(path, arg string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), selfCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, arg)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	out, err := cmd.CombinedOutput()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	switch {
	case err != nil && line != "":
		return "", fmt.Errorf("%w: %s", err, line)
	case err != nil:
		return "", err
	case line == "":
		return "no version printed", nil
	default:
		return line, nil
	}
}
