package approvalwait

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// history.go is `yolo history requests` and `yolo history r-<id>` over the records alone
// (docs/design/jail-history.md §5.3, the thin first slice JH-D16): every request of this
// workspace, filed, answered how, spent or closed, and the next step. It reads the same
// directory in a jail and on the host; nothing here decides anything (JH-D2).

// HistoryOpts is one `yolo history` over the records.
type HistoryOpts struct {
	Workspace string
	ID        string    // one request; empty lists them all
	Since     time.Time // only requests filed at or after it; zero for every record
	JSON      bool      // the records as stored, one per line
	InJail    bool      // on the host, the listing also names `yolo approve`, the store's own view
	Out, Err  io.Writer
	Now       func() time.Time
}

func (o HistoryOpts) withDefaults() HistoryOpts {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Err == nil {
		o.Err = os.Stderr
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

func (o HistoryOpts) say(format string, a ...any) {
	fmt.Fprintf(o.Err, "yolo history: "+format+"\n", a...)
}

// readAll is every readable record beneath dir, oldest filed first. A record that cannot be
// read is skipped: the listing is a report, and one bad file must not hide the rest.
func readAll(dir *os.Root) ([]Record, error) {
	d, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	names, _ := d.Readdirnames(-1)
	d.Close()
	slices.Sort(names)
	var recs []Record
	for _, n := range names {
		id, ok := strings.CutSuffix(n, ".json")
		if !ok || !ValidID(id) {
			continue
		}
		if rec, err := readRecord(dir, id); err == nil && rec != nil {
			recs = append(recs, *rec)
		}
	}
	slices.SortStableFunc(recs, func(a, b Record) int { return a.Filed.Compare(b.Filed) })
	return recs, nil
}

// waitsOnAgent is what a record still wants of the agent at now.
type waitsOnAgent int

const (
	waitsNothing  waitsOnAgent = iota
	waitsForHost               // pending: the agent waits with `yolo wait`
	waitsForRetry              // allowed once, unspent: the agent runs its command again, once
)

// answered is how rec was answered, in words: "allowed once", "allowed for 15m, until …",
// "denied", … ; empty while it is pending or for one closed unanswered.
//
// With at set, " at <at>" follows the verb and who answered (when the record says) ends it.
func (rec Record) answered(at time.Time) string {
	when := ""
	if !at.IsZero() {
		when = " at " + stamp(at)
	}
	var s string
	switch {
	case rec.State == StateDenied:
		s = "denied" + when
	case rec.State != StateAllowed:
		return ""
	case rec.Answer == AnswerOnce:
		s = "allowed once" + when
	case rec.Answer == AnswerRemember:
		s = "allowed once" + when + ", its refs remembered in the project's config"
	case rec.Answer == AnswerFor && rec.Until.IsZero():
		s = "allowed" + when + " for a time"
	case rec.Answer == AnswerFor:
		s = fmt.Sprintf("allowed%s for %s, until %s", when, shortDuration(rec.Until.Sub(rec.Decided)), stamp(rec.Until))
	case rec.Answer == AnswerAdd:
		s = "allowed" + when + ", added to the working set"
	case rec.Answer == "":
		s = "allowed" + when + " (the yolo that answered it did not record how)"
	default:
		s = "allowed" + when + " (" + termsafe.Visible(rec.Answer) + ")"
	}
	if b := rec.by(); b != "" && !at.IsZero() {
		s += ", " + b
	}
	return s
}

// covers reports whether rec's answer covers its commands for a time or for good, rather than
// one run: such a request never waits on the agent.
func (rec Record) covers() bool { return rec.Answer == AnswerFor || rec.Answer == AnswerAdd }

// by is who answered rec, in words, or "" when the record does not say.
func (rec Record) by() string {
	switch rec.Via {
	case ViaOnRequest:
		return "by `yolo approve` from the on_request hook (the toast)"
	case ViaHost:
		return "by `yolo approve` on the host"
	}
	return ""
}

// hookWords is the on_request hook's part, or "" when the record does not say.
func (rec Record) hookWords() string {
	switch rec.Hook {
	case HookRan:
		return "on_request hook ran"
	case HookFailed:
		return "on_request hook failed (`yolo approve` on the host shows its output)"
	case HookNone:
		return "no on_request hook ran"
	}
	return ""
}

// outcome is what became of rec at now in one clause, the next step for the agent, and what
// it still wants of the agent.
func (rec Record) outcome(now time.Time) (what, next string, waits waitsOnAgent) {
	switch state := rec.effectiveState(now); {
	case state == StatePending:
		return "pending until " + stamp(rec.Expires),
			fmt.Sprintf("start `%s` in the background, unless one already waits on it", WaitCommand(rec.ID)), waitsForHost
	case state == StateExpired:
		return "expired unanswered at " + stamp(rec.Expires),
			"nothing waits on you; running its command again files a new request", waitsNothing
	case state == StateRefused:
		what := "closed unanswered at " + stamp(rec.Decided)
		if rec.Reason == ReasonBrokerStopped {
			what += ": its broker stopped (its jail stopped or restarted)"
		}
		return what, "nothing waits on you; running its command again files a new request", waitsNothing
	case state == StateDenied:
		return rec.answered(rec.Decided),
			"do not run its command again; ask the human before trying anything else", waitsNothing
	case state == StateAllowed && rec.Used:
		what := rec.answered(rec.Decided)
		if !rec.SpentAt.IsZero() {
			what += "; spent at " + stamp(rec.SpentAt)
		} else {
			what += "; spent"
		}
		return what, "nothing to do: it ran", waitsNothing
	case state == StateAllowed && rec.Lapsed:
		return rec.answered(rec.Decided) + "; its window closed before the command ran",
			"running its command again asks the human once more; ask before you do", waitsNothing
	case state == StateAllowed && rec.covers():
		next := "nothing waits on you: a command it covers runs without asking"
		if rec.Answer == AnswerFor && !rec.Until.IsZero() {
			if now.Before(rec.Until) {
				next += " until " + stamp(rec.Until)
			} else {
				next = "nothing waits on you; its window has closed, so a command it covered asks again"
			}
		}
		return rec.answered(rec.Decided), next, waitsNothing
	case state == StateAllowed:
		return rec.answered(rec.Decided), "run its command again now, once", waitsForRetry
	default:
		return termsafe.Visible(state), "report it; do not retry", waitsNothing
	}
}

// History prints the workspace's requests, or one request, and the next step; it returns the
// exit status.
func History(o HistoryOpts) int {
	o = o.withDefaults()
	where := filepath.Join(paths.WorkspaceStateDir(o.Workspace), DirName)
	if o.ID != "" && !ValidID(o.ID) {
		o.say("%s is not a request id such as r-0123456789abcdef0123456789abcdef; `yolo history requests` lists this workspace's.", termsafe.Visible(o.ID))
		return ExitUsage
	}
	dir, err := openDir(o.Workspace)
	if err != nil {
		o.say("cannot read %s (%s). Next: check that it is a directory you can read, then run this again.", termsafe.Visible(where), termsafe.Visible(err.Error()))
		return ExitUnavailable
	}
	var recs []Record
	if dir != nil {
		recs, err = readAll(dir)
		dir.Close()
		if err != nil {
			o.say("cannot list %s (%s). Next: check that it is a directory you can read, then run this again.", termsafe.Visible(where), termsafe.Visible(err.Error()))
			return ExitUnavailable
		}
	}
	now := o.Now()
	if o.ID != "" {
		i := slices.IndexFunc(recs, func(r Record) bool { return r.ID == o.ID })
		if i < 0 {
			o.say("this workspace has no record of %s in %s: the id is mistyped, the request was filed for another workspace, or the host's yolo predates these records. `yolo history requests` lists the ones it has%s.",
				o.ID, termsafe.Visible(where), o.hostHint("; on the host, `yolo approve` lists the store's own"))
			return ExitUnavailable
		}
		if o.JSON {
			return o.printJSON(recs[i : i+1])
		}
		o.showOne(recs[i], now)
		return 0
	}
	if !o.Since.IsZero() {
		recs = slices.DeleteFunc(recs, func(r Record) bool { return r.Filed.Before(o.Since) })
	}
	if o.JSON {
		return o.printJSON(recs)
	}
	if len(recs) == 0 {
		span := ""
		if !o.Since.IsZero() {
			span = " filed since " + stamp(o.Since)
		}
		fmt.Fprintf(o.Out, "No broker request of this workspace%s is recorded in %s. A refused `gh` write or `git push` files one. Next: nothing waits on you.\n", span, termsafe.Visible(where))
		return 0
	}
	var pending, retry []string
	for _, r := range recs {
		what, next, waits := r.outcome(now)
		fmt.Fprintf(o.Out, "%s  %s  %s  %s\n", stamp(r.Filed), r.ID, termsafe.Visible(r.Source), termsafe.Visible(r.Command))
		facts := []string{what}
		if h := r.hookWords(); h != "" && r.Hook != HookNone {
			facts = append([]string{h}, facts...)
		}
		fmt.Fprintf(o.Out, "    %s. Next: %s.\n", strings.Join(facts, "; "), next)
		switch waits {
		case waitsForHost:
			pending = append(pending, r.ID)
		case waitsForRetry:
			retry = append(retry, r.ID)
		}
	}
	fmt.Fprintln(o.Out)
	switch {
	case len(pending) > 0:
		fmt.Fprintf(o.Out, "Next: %s still wait(s) on the host; start `yolo wait <id>` in the background for each one nothing waits on yet.\n", strings.Join(pending, ", "))
	case len(retry) > 0:
		fmt.Fprintf(o.Out, "Next: %s was allowed and not used yet; run its command again, once.\n", strings.Join(retry, ", "))
	default:
		fmt.Fprintln(o.Out, "Next: nothing waits on you. `yolo history r-<id>` shows one request in full.")
	}
	if h := o.hostHint("On the host, `yolo approve` shows the request store's own view of these."); h != "" {
		fmt.Fprintln(o.Out, h)
	}
	return 0
}

// hostHint is s on the host and "" in a jail: the request store is the host's authority, and a
// jail has none to name.
func (o HistoryOpts) hostHint(s string) string {
	if o.InJail {
		return ""
	}
	return s
}

// showOne prints one request end to end.
func (o HistoryOpts) showOne(r Record, now time.Time) {
	what, next, _ := r.outcome(now)
	fmt.Fprintf(o.Out, "%s  %s  %s\n", r.ID, termsafe.Visible(r.Source), termsafe.Visible(r.Command))
	filed := "  filed     " + stamp(r.Filed)
	if r.Jail != "" {
		filed += "  by jail " + termsafe.Visible(r.Jail)
	}
	if h := r.hookWords(); h != "" {
		filed += "; " + h
	}
	fmt.Fprintln(o.Out, filed)
	switch state := r.effectiveState(now); state {
	case StatePending, StateExpired, StateRefused:
		fmt.Fprintf(o.Out, "  outcome   %s\n", what)
	default:
		answered := "  answered  " + stamp(r.Decided) + "  " + r.answered(time.Time{})
		if b := r.by(); b != "" {
			answered += ", " + b
		}
		fmt.Fprintln(o.Out, answered)
		switch {
		case r.Used && !r.SpentAt.IsZero():
			fmt.Fprintf(o.Out, "  spent     %s  the command it allowed ran\n", stamp(r.SpentAt))
		case r.Used:
			fmt.Fprintln(o.Out, "  spent     the command it allowed ran")
		case r.Lapsed:
			fmt.Fprintln(o.Out, "  lapsed    its window closed before the command ran")
		}
	}
	fmt.Fprintf(o.Out, "Next: %s.\n", next)
}

// printJSON prints recs as stored, one per line, for a program.
func (o HistoryOpts) printJSON(recs []Record) int {
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			o.say("cannot encode %s (%v).", r.ID, err)
			return ExitUnavailable
		}
		fmt.Fprintf(o.Out, "%s\n", b)
	}
	return 0
}

// stamp is t in local time, to the second.
func stamp(t time.Time) string { return t.Local().Format(time.DateTime) }

// shortDuration is d to the minute, without a trailing zero unit: 15m, 1h30m, 24h.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	s := d.Round(time.Minute).String()
	s = strings.TrimSuffix(s, "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
