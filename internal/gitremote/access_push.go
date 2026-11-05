package gitremote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

// prepareAccessProposal accepts only the small explicit push subset that PreparePush
// can prove with Git's own remote-helper dry run. Resolving a named push URL is local;
// the preflight checks its effective rewrite route before contacting any remote.
func prepareAccessProposal(ctx context.Context, args []string) (gitproto.PushProposal, error) {
	refuse := func() (gitproto.PushProposal, error) {
		return gitproto.PushProposal{}, fmt.Errorf("only `git push <remote> <source>:<refs/heads/name>` (or an explicit tag/delete) can be predicted; run an explicit push for the broker's live decision")
	}
	if len(args) < 4 || args[0] != "git" || args[1] != "push" {
		return refuse()
	}
	// The first positional argument must be the remote. Push options may precede it.
	i := 2
	var options []string
	for i < len(args) {
		a := args[i]
		if a == "-o" || a == "--push-option" {
			if i+1 >= len(args) {
				return refuse()
			}
			options = append(options, a, args[i+1])
			i += 2
		} else if strings.HasPrefix(a, "--push-option=") || (strings.HasPrefix(a, "-o") && len(a) > 2) {
			options = append(options, a)
			i++
		} else {
			break
		}
	}
	if i >= len(args)-1 || strings.HasPrefix(args[i], "-") {
		return refuse()
	}
	remote := args[i]
	pushArgs := append(append([]string{}, options...), args[i+1:]...)
	for _, spec := range args[i+1:] {
		if strings.HasPrefix(spec, "-") {
			return refuse()
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return gitproto.PushProposal{}, fmt.Errorf("cannot find git working directory; run `pwd` then retry: %w", err)
	}
	// A named remote's push URL may differ from its fetch URL. Git itself resolves
	// includes, insteadOf and pushInsteadOf; PreparePush checks it cannot reroute.
	if !strings.HasPrefix(remote, "yolo::") {
		out, e := gitOutput(ctx, dir, "remote", "get-url", "--push", "--all", remote)
		if e != nil {
			var termination *GitGroupTerminationError
			if errors.As(e, &termination) {
				return gitproto.PushProposal{}, e
			}
			return refuse()
		}
		urls := bytes.Split(bytes.TrimSuffix(out, []byte{10}), []byte{10})
		if len(urls) != 1 || len(urls[0]) == 0 {
			return gitproto.PushProposal{}, fmt.Errorf("git push preflight: named remote has multiple effective push URLs; use a single helper-routed destination or push from the host")
		}
		remote = string(urls[0])
	}
	if !strings.HasPrefix(remote, "yolo::") {
		return refuse()
	}
	selected, err := preparePushContext(ctx, remote, pushArgs, dir)
	if err != nil {
		return gitproto.PushProposal{}, err
	}
	return gitproto.PushProposal{Service: "git-receive-pack", URL: strings.TrimPrefix(remote, "yolo::"), Commands: selected.Commands, PushOptions: append([]string{}, selected.PushOptions...)}, nil
}
