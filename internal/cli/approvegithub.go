package cli

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// approvegithub.go is `yolo approve <id> --for <duration>` and `--add` for a GitHub write
// request (docs/design/boundary-broker.md §6.4, BB-D79, BB-D80, BB-D92): a working-set record
// on the request's resource in place of a command grant. --add puts the PR or issue the write
// targets into the workspace's working set, until the PR is merged or the entry is revoked;
// --for allows the request's kind of write on its resource until the clock passes the
// duration. Both are `w-<id>` entries `yolo approve --revoke` ends, like git's.

// approveGitHubPerResource answers GitHub request id with --add or --for through s, the GitHub
// store, whose Entries hook (ghbroker.RequestEntries) names the resource and refuses an answer
// that does not fit the request's kind.
func approveGitHubPerResource(s *brokerstore.Store, id string, d brokerstore.Decision, add bool, out, errs io.Writer) int {
	if d.Session {
		fmt.Fprintf(errs, "yolo approve: --for session is not available for GitHub writes: no grant is scoped to a session. "+
			"Next: run `yolo approve %s --for 15m`, or `yolo approve %s --add` for a PR or issue.\n", termsafe.Visible(id), termsafe.Visible(id))
		return approveExitPolicy
	}
	d.Add = add
	entries, err := s.DecideWorking(id, d)
	if err != nil {
		return approveFailure(err, errs)
	}
	if add {
		approveGitHubAddOutcome(out, id, entries[0])
		return 0
	}
	approveTimedOutcome(out, id, githubEntryName(entries[0]), entries)
	return 0
}
