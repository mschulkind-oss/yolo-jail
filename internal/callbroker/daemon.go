package callbroker

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerhook"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func sameRoots(a, b []string) bool { return slices.Equal(a, b) }

// Main is the core-owned host daemon's hidden entry. The selected pack's
// launch orders it with a private input and a live host scope. A pack write
// takes Allow once or a timed answer (BX-D17); there is no permanent answer.
func Main(args []string) int {
	defer packload.ReleaseEmbedded()
	fs := flag.NewFlagSet("call-broker", flag.ContinueOnError)
	socket := fs.String("socket", "", "host socket behind the authenticated service front")
	input := fs.String("launch-input", "", "private gate-approved per-launch input")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *socket == "" || *input == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "call-broker: --socket and --launch-input are required; restart with the selected pack")
		return 2
	}
	// Its approval authority fetches a selected pack the pack store lacks, in line, and skips it
	// only when that fetch fails (config.EnableMissingPackFetch).
	config.EnableMissingPackFetch(os.Stderr)
	workspace, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "call-broker: cannot identify the workspace; restart the jail")
		return 1
	}
	data, err := readPrivateInput(*input)
	var identity struct {
		Source    string `json:"source"`
		ScopeFile string `json:"scope_file"`
		Front     string `json:"front"`
	}
	if err == nil {
		err = json.Unmarshal(data, &identity)
	}
	if err != nil || !loopholedecl.ValidBrokerSourceName(identity.Source) || identity.ScopeFile == "" {
		fmt.Fprintln(os.Stderr, "call-broker: selected input is invalid; relaunch with the pack selected")
		return 1
	}
	store := &brokerstore.Store{Dir: paths.BrokerSourceDir(identity.Source), Source: identity.Source}
	if err := os.MkdirAll(store.Dir, 0700); err != nil {
		fmt.Fprintln(os.Stderr, "call-broker: cannot create private store; repair host state and retry")
		return 1
	}
	if _, err := store.Working(); err != nil {
		fmt.Fprintln(os.Stderr, "call-broker: cannot open private store; repair host state and retry")
		return 1
	}
	log := brokeraudit.Open(paths.BrokerAuditLog(), nil)
	var h *CLIHandler
	var httpHandler *HTTPHandler
	if identity.Front == "http" {
		httpHandler, err = newHTTPHandler(*input, workspace, store, log)
		if err == nil {
			h = httpHandler.CLIHandler
		}
	} else {
		h, err = newCLIHandler(*input, workspace, store, log)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "call-broker: selected input is unavailable; relaunch the jail and retry")
		return 1
	}
	if h.input.ScopeFile == "" {
		fmt.Fprintln(os.Stderr, "call-broker: no selected host scope; relaunch the jail")
		return 1
	}
	sf, err := brokerscope.ReadFile(h.input.ScopeFile)
	if err != nil || sf.Form != brokerscope.FormCallRoot || sf.CallInput != *input || sf.Source != h.input.Source || sf.Workspace != workspace || !sameRoots(sf.Repos, h.input.Roots) {
		fmt.Fprintln(os.Stderr, "call-broker: host scope and selected input disagree; relaunch the jail")
		return 1
	}
	start, err := h.lifetimeStart(sf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "call-broker: cannot bind broker lifetime; retry the launch")
		return 1
	}
	bound, lease, err := brokercore.BindLease(h.input.Source, h.input.ScopeFile, sf, start)
	if err != nil {
		fmt.Fprintln(os.Stderr, "call-broker: cannot bind live host scope; retry the launch")
		return 1
	}
	defer brokercore.CloseLease(h.input.Source, lease)
	if identity.Front == "cli" {
		if err = brokerscope.BindCallCLI(h.input.ScopeFile, bound, h.program.path, h.program.sha256); err != nil {
			fmt.Fprintln(os.Stderr, "call-broker: cannot pin host CLI for this launch; retry")
			return 1
		}
		bound.CallCLIHash = h.program.sha256
		bound.CallCLIPath = h.program.path
	}
	h.scopePath, h.bound, h.start = h.input.ScopeFile, bound, start
	if h.input.Version == callbrokerinput.SnapshotLaunchVersion {
		if _, err := h.currentLegacySnapshot(bound); err != nil {
			fmt.Fprintln(os.Stderr, "call-broker: initial snapshot validation failed:", err)
			return 1
		}
		if err := h.publishSnapshotConsumer(bound, start); err != nil {
			fmt.Fprintln(os.Stderr, "call-broker: cannot publish snapshot consumer proof; retry the launch:", err)
			return 1
		}
	}
	if httpHandler != nil {
		store.Revalidate = httpHandler.revalidateHTTP
	} else {
		store.Revalidate = h.revalidate
	}
	store.PublishRecords = true
	store.Audit = StoreAudit(log, h.input.Source)
	// A record the jail's `yolo wait` could not be given is reported, never authoritative.
	store.PublishFailed = func(q brokerstore.Request, err error) {
		fmt.Fprintln(os.Stderr, daemonName(h.input.Source)+": could not write the jail's record of request", q.ID+" (`yolo wait` there will not see its answer):", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.shutdown = ctx
	// The user's on_request hook runs once per request this broker lifetime files, as for the
	// native brokers (brokercore.StartOnRequest); a join notifies nobody.
	hookDone := brokercore.StartOnRequest(ctx, daemonName(h.input.Source), store, start, config.OnRequest(), os.Stderr)
	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			// WITHDRAW BEFORE CANCEL: no selected-front call begins a send after this, and one
			// that already began is neither refunded nor retried (slice 3.3).
			if httpHandler != nil {
				httpHandler.withdrawFront()
			}
			cancel()
			close(stop)
		case <-ctx.Done():
		}
	}()
	serve := hostservice.ServeFrontedUnix
	handle := hostservice.Handler(h.Handle)
	if httpHandler != nil {
		serve = func(handler hostservice.Handler, socket string, stop <-chan struct{}) error {
			return hostservice.ServeHTTPBrokerFrontedUnix(handler, socket, stop, validateHTTPBrokerFrame)
		}
		handle = httpHandler.Handle
	}
	err = serve(handle, *socket, stop)
	cancel()
	// The jails this broker served have no call waiting on their pending requests any more:
	// close them, so a waiter wakes with "closed". No grant ends; a workspace holds its own.
	h.lifetimes.EndAll(brokercore.LifetimeOwner{Name: daemonName(h.input.Source), Start: start, Store: store, Log: os.Stderr})
	// A hook runs in its own process group, which the launcher's stop signal does not reach, so
	// wait for the runner to stop what it started.
	select {
	case <-hookDone:
	case <-time.After(brokerhook.StopGrace + brokerhook.KillWait + time.Second):
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "call-broker: host service failed; inspect the host service and retry")
		return 1
	}
	return 0
}

// daemonName is the name this daemon's log lines and hook notices carry.
func daemonName(source string) string { return source + "-broker" }

// lifetimeStart is the broker lifetime start: the one the launch's delivered baseline
// binds when one exists, otherwise a fresh random one.
func (h *CLIHandler) lifetimeStart(sf brokerscope.File) (string, error) {
	start, err := brokerstore.NewStartID()
	if _, statErr := os.Lstat(brokerscope.DeliveredBaselinePath(sf)); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
		var delivered brokerscope.DeliveredBaseline
		delivered, err = brokerscope.ReadDelivered(sf, h.input.DeclarationHash, snapshotInputDigest(h.original))
		if err == nil {
			start = delivered.Start
		}
	}
	return start, err
}

// publishSnapshotConsumer writes the daemon's proof that it reads the policy snapshot.
func (h *CLIHandler) publishSnapshotConsumer(bound brokerscope.File, start string) error {
	proof := brokerscope.SnapshotConsumer{
		Version: brokerscope.SnapshotConsumerVersion,
		Binding: brokerscope.PolicyBinding{
			Source:          h.input.Source,
			Workspace:       bound.Workspace,
			LaunchID:        bound.LaunchID,
			DeclarationHash: h.input.DeclarationHash,
			InputDigest:     brokerscope.InputDigest(h.original),
		},
		Start:                 start,
		BrokerPID:             bound.BrokerPID,
		SupportedReaderPolicy: brokerscope.PolicySnapshotVersion,
	}
	return brokerscope.PublishSnapshotConsumer(bound, proof)
}
