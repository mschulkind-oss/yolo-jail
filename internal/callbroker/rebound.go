package callbroker

import (
	"errors"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// reboundRequest validates the stored command before temporarily binding its
// workspace-held answer to this authenticated invocation. History is untouched.
func reboundRequest(held, invocation brokerstore.Request) (brokerstore.Request, error) {
	if !brokerstore.SameWorkspace(held.Workspace, invocation.Workspace) || held.Set != invocation.Set || held.Repo != invocation.Repo || held.Digest != invocation.Digest || held.Digest != brokerstore.CommandDigest(held) || held.ContextDigest != invocation.ContextDigest || held.ContextName != invocation.ContextName {
		return brokerstore.Request{}, errors.New("stored answer does not bind this workspace invocation; retry the original call and ask again")
	}
	held.Workspace, held.Jail, held.Start, held.LaunchID = invocation.Workspace, invocation.Jail, invocation.Start, invocation.LaunchID
	return held.WithExecutionContext(invocation.ExecutionContext()), nil
}
