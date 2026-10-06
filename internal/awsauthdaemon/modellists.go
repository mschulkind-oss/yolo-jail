package awsauthdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// modellists.go is this daemon's half of the BEDROCK LIST FETCH (docs/design/model-lists-and-pickers.md
// OQ-MM6): the `bedrock-models` action a launch sends when no selected pack supplies a Bedrock
// model list. The daemon answers from its cache when the region's list is less than a day old,
// and otherwise fetches it (internal/awsauth's modellist.go) with the configured profile's own
// credentials, the SSO role's, before any narrowing, so a jail's served credential stays
// invoke-only. The request is
//
//	{"action": "bedrock-models", "region": "<region>", "budget_ms": <n>}
//
// and the answer, on stdout with exit 0, one ModelListAnswer. Like every request here it names no
// profile, role or policy (BuildHandler's rule): the daemon fetches as the profile it serves.
//
// # The host's alone
//
// The action is answered on the private `.host` socket only, never through a front. A jail
// reaching it would be a jail spending the un-narrowed credential on the control plane, which is
// exactly the permission the ruling keeps out of jails.
//
// # The budget is the launch's, and a slow fetch outlives it
//
// One fetch runs per region at a time, on a background context with the lister's own timeout, so
// a launch that stops waiting does not cancel the fetch whose result the next launch reads from
// the cache. Within the budget the answer is the fetched list; past it, or when the fetch fails,
// it is the cache's last good list with a note saying why it was not refreshed, and with no list
// at all, why there is none.

// ModelListAction is the action's name.
const ModelListAction = "bedrock-models"

// ModelListRegionKey is the request field naming the region.
const ModelListRegionKey = "region"

// ModelListBudget is the most a launch waits for a list it has to fetch: three `aws` starts run
// side by side and one round trip each to the control plane.
const ModelListBudget = 5 * time.Second

// ModelListAnswer is the answer to one region's question.
type ModelListAnswer struct {
	// List is the region's list, its zero value when there is none.
	List awsauth.ModelList `json:"list"`
	// Source says where List came from: "cache" (a day old or less), "fetched" (just now), or
	// "stale cache" (older, kept because a refresh failed or had not finished).
	Source string `json:"source,omitempty"`
	// Note says why a stale list was not refreshed, or why there is no list.
	Note string `json:"note,omitempty"`
}

// Has reports whether the answer carries a list with any model on it.
func (a ModelListAnswer) Has() bool { return len(a.List.Models) > 0 }

// ModelListSource is what an answer is obtained from: the cache, the profile it is fetched as, the
// lister, and the tracker that keeps one fetch per region in flight.
type ModelListSource struct {
	CachePath string
	Profile   string
	Lister    awsauth.ModelLister
	// NoCreateDir makes the cache write refuse a missing state directory (the daemon's rule,
	// awsauth.Broker.NoCreateDir).
	NoCreateDir bool
	Now         func() time.Time
	Tracker     *ModelListTracker
	// Log receives one line per failed fetch; nil logs nowhere.
	Log io.Writer
}

func (s ModelListSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ModelListTracker runs at most one fetch per region at a time.
type ModelListTracker struct {
	mu       sync.Mutex
	inflight map[string]*listAttempt
}

// NewModelListTracker is an empty tracker.
func NewModelListTracker() *ModelListTracker {
	return &ModelListTracker{inflight: map[string]*listAttempt{}}
}

type listAttempt struct {
	done chan struct{}
	list awsauth.ModelList
	err  error
}

// begin starts region's fetch, or joins the one already running. The fetch stores its list in the
// cache before it reports done, so a launch that read the cache after done finds it there.
func (t *ModelListTracker) begin(src ModelListSource, region string) *listAttempt {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a := t.inflight[region]; a != nil {
		return a
	}
	a := &listAttempt{done: make(chan struct{})}
	t.inflight[region] = a
	go func() {
		list, err := src.Lister.Fetch(context.Background(), src.Profile, region)
		if err == nil {
			if serr := awsauth.StoreModelList(src.CachePath, src.Profile, list, !src.NoCreateDir); serr != nil &&
				src.Log != nil {
				fmt.Fprintf(src.Log, "aws-auth: the Bedrock model list for %s was fetched and could not be "+
					"cached: %v\n", region, serr)
			}
		} else if src.Log != nil {
			fmt.Fprintf(src.Log, "aws-auth: the Bedrock model list fetch for %s failed: %v\n", region, err)
		}
		t.mu.Lock()
		a.list, a.err = list, err
		delete(t.inflight, region)
		t.mu.Unlock()
		close(a.done)
	}()
	return a
}

// ObtainModelList answers region's question within budget; see the file comment for the order.
func ObtainModelList(src ModelListSource, region string, budget time.Duration) ModelListAnswer {
	if src.Profile == "" {
		return ModelListAnswer{Note: "the aws-auth service has no profile to fetch it as (" +
			awsauth.SettingsScope + ".profile is not set)"}
	}
	cache, _ := awsauth.LoadModelCache(src.CachePath)
	cached, have := cache.Lookup(src.Profile, region)
	if have && cached.Fresh(src.now()) {
		return ModelListAnswer{List: cached, Source: "cache"}
	}
	tracker := src.Tracker
	if tracker == nil {
		tracker = NewModelListTracker()
	}
	attempt := tracker.begin(src, region)
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-attempt.done:
		if attempt.err == nil {
			return ModelListAnswer{List: attempt.list, Source: "fetched"}
		}
		why := "the fetch failed: " + fetchFailure(attempt.err)
		if have {
			return ModelListAnswer{List: cached, Source: "stale cache", Note: why}
		}
		return ModelListAnswer{Note: why}
	case <-timer.C:
	}
	why := fmt.Sprintf("the fetch had not finished within %s; it continues, and a later launch reads "+
		"its answer", budget)
	if have {
		return ModelListAnswer{List: cached, Source: "stale cache", Note: why}
	}
	return ModelListAnswer{Note: why}
}

// fetchFailure is a fetch error's sentence: the classifier's Message, which names the fix.
func fetchFailure(err error) string {
	var me *awsauth.MintError
	if errors.As(err, &me) {
		return strings.TrimRight(strings.TrimSpace(me.Message), ".")
	}
	return err.Error()
}

// answerModelList is the action's handler body.
func answerModelList(session *hostservice.Session, src ModelListSource) {
	if session.Fronted {
		// Through a front is a jail's way in (or a launch's own dial through its endpoint): the
		// list is fetched with the un-narrowed credential, which no jail may spend.
		session.Stderr("refused: " + ModelListAction + " is answered on the host socket only\n")
		session.Exit(2)
		return
	}
	region := field(session, ModelListRegionKey)
	data, err := json.Marshal(ObtainModelList(src, region, modelListBudgetOf(session)))
	if err != nil {
		session.Stderr("encode the answer: " + err.Error() + "\n")
		session.Exit(1)
		return
	}
	session.Stdout(string(data) + "\n")
}

// modelListBudgetOf is the request's budget, clamped to (0, ModelListBudget].
func modelListBudgetOf(session *hostservice.Session) time.Duration {
	raw, _ := session.Get(hostservice.LaunchCheckBudgetKey)
	var ms float64
	if n, ok := jsonx.AsInt(raw); ok {
		ms = float64(n)
	} else if f, ok := raw.(float64); ok {
		ms = f
	}
	budget := time.Duration(ms * float64(time.Millisecond))
	if budget <= 0 || budget > ModelListBudget {
		return ModelListBudget
	}
	return budget
}
