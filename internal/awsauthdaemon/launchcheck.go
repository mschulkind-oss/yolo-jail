package awsauthdaemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// launchcheck.go is this daemon's answer to the LAUNCH CHECK (internal/hostservice's
// launchcheck.go coins the term and states the protocol): design §8's "profile configured, no
// SSO session ever established → the launch warns with the `aws sso login` command and
// proceeds", and the lapsed session beside it (docs/design/sso-backed-bedrock.md SSO-D1).
//
// # Where the answer comes from, cheapest first
//
//  1. THE CACHE. A credential the next fetch would be served is proof enough, so a warm cache
//     answers with nothing to say and no `aws` invocation. That is every launch but the first
//     after a spawn, a settings change or a lapse.
//  2. THE MINT THE JAIL'S FIRST FETCH WOULD OTHERWISE MAKE. A cold cache means the agent's first
//     request would mint inside the SDK's one-second budget (design R1). So the check starts
//     that mint now, or joins the one already running (the proactive minter's first, on a fresh
//     spawn), and waits for it within the launch's budget. A success leaves the cache warm for
//     that first request; a failure is classified by the one classifier the 4xx and the
//     self-check use (awsauth's classify), and its Message already names the fix.
//  3. THE LAST ATTEMPT BEFORE IT. When the mint outlasts the budget, the check answers with the
//     previous attempt's failure and its age, or with a note that it could not tell. The mint
//     keeps running either way: its result lands in the cache or in the next check.
//
// # One mint in flight, shared
//
// mintTracker is the whole coordination: the proactive minter and every launch check go through
// it, so a launch arriving during the spawn-time mint waits for that mint rather than starting a
// second. The jail's own `credentials` requests do not: their path stays exactly the broker's,
// lock-free when warm.

// mintAttempt is one mint, run through the tracker. err and at are written once, before done
// is closed.
type mintAttempt struct {
	done chan struct{}
	err  error
	at   time.Time
}

// mintTracker runs at most one mint at a time through the broker and remembers the last one to
// finish. log receives one line per failed mint, whoever began it; the daemon hands it stderr,
// which the run pipeline sends to the service log.
type mintTracker struct {
	mu       sync.Mutex
	inflight *mintAttempt
	last     *mintAttempt
	now      func() time.Time
	log      io.Writer
}

func newMintTracker(log io.Writer) *mintTracker { return &mintTracker{now: time.Now, log: log} }

// begin starts a mint through broker, or returns the one already running.
//
// The mint runs on a background context with the minter's own timeout, never the caller's: a
// launch that stops waiting must not cancel the mint whose result the jail's first request
// needs.
func (m *mintTracker) begin(broker awsauth.Broker, caller string) *mintAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inflight != nil {
		return m.inflight
	}
	a := &mintAttempt{done: make(chan struct{})}
	m.inflight = a
	go func() {
		_, err := broker.Fetch(context.Background(), caller)
		if err != nil && m.log != nil {
			fmt.Fprintf(m.log, "aws-auth: %s mint failed: %v\n", caller, err)
		}
		m.mu.Lock()
		a.err, a.at = err, m.now()
		m.inflight = nil
		m.last = a
		m.mu.Unlock()
		close(a.done)
	}()
	return a
}

// previous is the last mint to finish, nil before any has.
func (m *mintTracker) previous() *mintAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

// launchCheck answers the launch check within budget; see the file comment for the order.
func launchCheck(broker awsauth.Broker, mints *mintTracker, budget time.Duration) hostservice.LaunchCheckReport {
	if !broker.RemintDue() {
		return hostservice.LaunchCheckReport{}
	}
	// Taken BEFORE begin, so a timeout reports what was known when this check started and
	// never the attempt it is waiting on.
	before := mints.previous()
	attempt := mints.begin(broker, hostservice.LaunchCheckAction)
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-attempt.done:
		if attempt.err == nil {
			return hostservice.LaunchCheckReport{}
		}
		return hostservice.LaunchCheckReport{Warnings: []string{mintWarning(attempt.err)}}
	case <-timer.C:
	}
	if before != nil && before.err != nil {
		age := mints.now().Sub(before.at).Round(time.Second)
		return hostservice.LaunchCheckReport{Warnings: []string{fmt.Sprintf(
			"%s (from the mint %s ago; a new one had not finished within %s)",
			mintWarning(before.err), age, budget)}}
	}
	return hostservice.LaunchCheckReport{Notes: []string{fmt.Sprintf(
		"the credential mint for profile %q had not finished within %s, so this launch cannot "+
			"say whether its SSO session is live; if the mint fails, the agent's first Bedrock "+
			"request names the fix", broker.Config.Profile, budget)}}
}

// mintWarning is the one warning line for a failed mint. The Message is the classifier's, the
// same words the jail's 4xx and `yolo check` carry, so the fix it names (`aws sso login
// --profile X` for a lapsed or never-established session, the config for a missing profile) is
// stated once, in awsauth. What follows it says how that fix reaches the running service
// (fixPickup).
func mintWarning(err error) string {
	var mintErr *awsauth.MintError
	what := err.Error()
	if errors.As(err, &mintErr) {
		what = mintErr.Message
	}
	what = strings.TrimRight(strings.TrimSpace(what), ".")
	return "cannot mint a Bedrock credential for this launch: " + what + ". The launch " +
		"continues; Bedrock requests fail until that is fixed" + fixPickup(mintErr)
}

// fixPickup is the end of a mint warning: when a fix reaches the jail that is already running.
//
// Every mint shells out afresh, so a fix made outside yolo (a login, the host's ~/.aws/config,
// the `aws` CLI installed, a role's policy in AWS, the network) is found by the next mint, with
// no relaunch. A change to loopholes.aws-auth.settings is not: this daemon reads its settings
// once, at spawn (prepare), and what restarts it on a settings change is a launch
// (broker.EnsureSingleton's settings-drift restart; an attach reports the drift and restarts
// nothing). So "no relaunch" is promised outright only for the failures whose every named fix is
// outside yolo: a session to log in to, and a CLI to install. Every other failure, including one
// this package did not classify, can be fixed by a setting, and gets both halves.
func fixPickup(mintErr *awsauth.MintError) string {
	if mintErr != nil && (mintErr.Kind == awsauth.FailureLoginRequired ||
		mintErr.Kind == awsauth.FailureCLIMissing) {
		return ", and then work with no relaunch"
	}
	return ". A fix made outside yolo is picked up with no relaunch; a change to " +
		awsauth.SettingsScope + ", which the service reads only when it starts, takes effect " +
		"when the next launch of a new jail restarts it"
}
