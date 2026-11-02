package brokerstore

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// record.go copies each request's answer to where its workspace's jail can read it
// (internal/approvalwait): every write of a request record publishes it, so a `yolo wait r-<id>`
// wakes when the host answers, and one started after a restart answers from the copy. It also
// holds the refusal of a retry after an Allow once whose window closed unspent.

// publish writes q's jail-readable record when this store publishes them (PublishRecords). A
// failure never fails the transaction: the record is a wake-up hint, and the store stays the
// authority. It is reported through PublishFailed when set.
func (s *Store) publish(q Request) {
	if !s.PublishRecords {
		return
	}
	rec := approvalwait.Record{ID: q.ID, Source: s.SourceName(), State: q.State,
		Command: termsafe.Visible(shquote.JoinDisplay(summaryArgv(q.Argv))), Filed: q.Filed,
		Expires: q.Expires(), Decided: q.Decided, Used: q.Spent, Lapsed: q.Lapsed}
	if err := approvalwait.Publish(q.Workspace, rec); err != nil && s.PublishFailed != nil {
		s.PublishFailed(q, err)
	}
}

// requestOf is the request a write("requests", …) carries.
func requestOf(v any) (Request, bool) {
	switch q := v.(type) {
	case Request:
		return q, true
	case *Request:
		return *q, q != nil
	}
	return Request{}, false
}

// LapsedAllowError is a retry of a command the host allowed once whose Allow once expired
// before any command spent it. It is returned once per such request, and the request is
// marked, so the command run again after this refusal files a new request: the human asks
// for it deliberately rather than a retry filing one unnoticed.
type LapsedAllowError struct{ Request string }

func (e *LapsedAllowError) Error() string {
	return fmt.Sprintf("request %s was allowed, but its window closed before the command ran", e.Request)
}

// IsLapsedAllow reports whether err is a *LapsedAllowError, and its request.
func IsLapsedAllow(err error) (string, bool) {
	var lapsed *LapsedAllowError
	if errors.As(err, &lapsed) {
		return lapsed.Request, true
	}
	return "", false
}

// lapsedIn marks and returns the request of input's workspace, set and command that the host
// allowed once and whose every grant expired unspent, if it has not been reported yet.
func (s *Store) lapsedIn(r *records, input Request) (Request, bool, error) {
	for i := range r.requests {
		q := &r.requests[i]
		if q.State != "allowed" || q.Lapsed || q.Spent || q.Set != input.Set || q.Digest != input.Digest ||
			!SameWorkspace(q.Workspace, input.Workspace) {
			continue
		}
		grants, lapsed := 0, true
		for _, g := range r.grants {
			if g.Request != q.ID {
				continue
			}
			grants++
			if g.Digest == "" || g.EndedBy() != EndExpired || g.Uses != g.Issued {
				lapsed = false
			}
		}
		if grants == 0 || !lapsed {
			continue
		}
		q.Lapsed = true
		if err := s.write("requests", q.ID, q); err != nil {
			return Request{}, false, err
		}
		return *q, true, nil
	}
	return Request{}, false, nil
}
