package gitbroker

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

// DecideImportAccess gives cache readers the broker's local decision for one import URL. It
// intersects approval with current remotes/grants, then applies the same exact URL and broad
// read checks as a fetch request. HTTPS always retains the broker's anonymous-only path when
// that authenticated decision is absent; SSH/scp is refused.
func DecideImportAccess(remote string, approvedRemotes, currentRemotes []string,
	approvedGrants, currentGrants []brokerscope.Grant) (accessClass string, allowed bool, err error) {
	transport, err := transportOf(remote)
	if err != nil {
		return "", false, fmt.Errorf("git broker refused import URL: %w", err)
	}
	if err := rejectURLCredentials(remote); err != nil {
		return "", false, err
	}
	scope := brokerscope.File{
		Form: brokerscope.FormRemoteURL, Repos: intersectStrings(approvedRemotes, currentRemotes),
		Grants: intersectReadGrants(approvedGrants, currentGrants),
	}
	repo, err := gitproto.Identity(remote)
	if err != nil {
		return "", false, err
	}
	authenticated := brokerscope.HoldsRead(scope.Grants) && scopeContains(scope, remote) &&
		brokerscope.ReadCovers(scope.Grants, repo, func(g, r string) bool { return g == r })
	if authenticated {
		return ImportAccessAuthenticated, true, nil
	}
	if transport == transportHTTPS {
		return ImportAccessAnonymous, true, nil
	}
	return "", false, errors.New("the git broker refused this SSH import: it needs the exact approved remote URL and the current broad Git read grant")
}
