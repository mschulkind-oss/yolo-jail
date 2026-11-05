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

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func sameRoots(a, b []string) bool { return slices.Equal(a, b) }

// Main is the core-owned host daemon's hidden entry. The selected pack's
// launch orders it with a private input and a live host scope. Pack writes
// require exact-once approval; timed and permanent answers remain unavailable.
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
	start, err := brokerstore.NewStartID()
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
	if httpHandler != nil {
		store.Revalidate = httpHandler.revalidateHTTP
	} else {
		store.Revalidate = h.revalidate
	}
	store.PublishRecords = true
	store.Audit = StoreAudit(log, h.input.Source)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.shutdown = ctx
	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
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
	if err := serve(handle, *socket, stop); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "call-broker: host service failed; inspect the host service and retry")
		return 1
	}
	return 0
}
