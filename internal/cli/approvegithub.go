package cli

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// approvegithub.go is `yolo approve <id> --for <duration>` for a GitHub write request
// (docs/design/boundary-broker.md §6.4, BB-D79, BB-D80, BB-D92): a working-set record on the
// request's resource in place of a command grant, allowing the request's kind of write on its
// resource until the clock passes the duration. It is a `w-<id>` entry `yolo approve --revoke`
// ends, like git's. The answer that added a PR or issue for good is removed
// (github-write-scopes.md OQ-GW2).

// approveGitHubPerResource answers GitHub request id with --for through s, the GitHub store,
// whose Entries hook (ghbroker.RequestEntries) names the resource and refuses an answer that
// does not fit the request's kind.
func approveGitHubPerResource(s *brokerstore.Store, id string, d brokerstore.Decision, out, errs io.Writer) int {
	if d.Session {
		fmt.Fprintf(errs, "yolo approve: --for session is not available for GitHub writes: no grant is scoped to a session. "+
			"Next: run `yolo approve %s --for 15m`.\n", termsafe.Visible(id))
		return approveExitPolicy
	}
	entries, err := s.DecideWorking(id, d)
	if err != nil {
		return approveFailure(err, errs)
	}
	approveTimedOutcome(out, id, githubEntryName(entries[0]), entries)
	return 0
}
