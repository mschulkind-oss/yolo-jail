package run

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

func callbackPortFree(port int) (bool, bool) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return false, true
		}
		return false, false
	}
	listener.Close()
	return true, true
}

func (o *Options) prepareMCPCallbacks(in *assembleInput) {
	facts := o.launchInputMCP.SignIn
	if facts == nil {
		return
	}
	netMode := o.resolveNetMode(in.cfg)
	bridge := appliedNetMode(in.rt, netMode, o.inContainer()) == "bridge"
	var declared []string
	for _, value := range readablePublishPorts(in.cfg) {
		declared = append(declared, pyStrCoerce(value))
	}
	facts.Callbacks = mcpsignin.PlanCallbacks(facts, in.rt, o.IsLinux && !o.IsMacOS, bridge, declared)
	if o.DryRun {
		return
	}
	eligible := slices.ContainsFunc(facts.Callbacks, func(row mcpsignin.Callback) bool { return row.Disposition == "publication pending" })
	if !eligible {
		o.reportMCPCallbacks(facts.Callbacks)
		return
	}
	generation := rand.Text()
	o.mcpCallbackGeneration = generation
	facts.CallbackGeneration = generation
	for i := range facts.Callbacks {
		row := &facts.Callbacks[i]
		if row.Disposition != "publication pending" {
			continue
		}
		if o.launchLock == nil || o.launchLock.isClosed() {
			row.Disposition = "unavailable"
			continue
		}
		release, owned := o.callbackLifecycleExclusion(in.cname, in.cname)
		if !owned {
			row.Disposition = "unavailable"
			continue
		}
		release()
		lease := mcpsignin.CallbackLease{Version: 1, Port: row.Port, Launch: in.cname, Runtime: in.rt, Generation: generation}
		row.Disposition, row.Owner = o.acquireCallbackLease(lease)
		if row.Disposition != "publication pending" {
			continue
		}
		probe := o.mcpCallbackPortFree
		if probe == nil {
			probe = callbackPortFree
		}
		free, known := probe(row.Port)
		if !free {
			row.Disposition = "unavailable"
			if known {
				row.Disposition = "port busy"
			}
			o.releaseCallbackLease(lease) // no create has been attempted
			row.Owner = ""
			continue
		}
		if !slices.Contains(o.mcpCallbackLeases, lease) {
			o.mcpCallbackLeases = append(o.mcpCallbackLeases, lease)
		}
	}
	if err := o.writeCallbackReceipt(generation, facts.Callbacks); err != nil {
		for _, lease := range o.mcpCallbackLeases {
			o.releaseCallbackLease(lease)
		}
		o.mcpCallbackLeases = nil
		for i := range facts.Callbacks {
			if facts.Callbacks[i].Disposition == "publication pending" {
				facts.Callbacks[i].Disposition = "unavailable"
				facts.Callbacks[i].Owner = ""
			}
		}
	}
	in.callbacks = slices.Clone(o.mcpCallbackLeases)
	o.reportMCPCallbacks(facts.Callbacks)
}

func (o *Options) reportMCPCallbacks(rows []mcpsignin.Callback) {
	for _, row := range rows {
		if row.Disposition == "not requested" {
			continue
		}
		disposition := row.Disposition
		if disposition == "held" {
			disposition += " by " + row.Owner
		}
		o.pr(o.Stderr).printf("MCP native callback for %s (%s): %s; IPv4 loopback TCP port %d only. If browser redirect does not complete, paste the full callback URL into the agent's prompt.", row.Server, row.Agent, disposition, row.Port)
	}
}

// assembleMCPCallbackPublications extends the existing declared publications,
// never configuration. Called after actual delivery freezes the managed point.
func assembleMCPCallbackPublications(argv []string, image string, leases []mcpsignin.CallbackLease) []string {
	if len(leases) == 0 {
		return argv
	}
	at := slices.Index(argv, image)
	if at < 0 {
		return argv
	}
	var ports []string
	for i := 0; i+1 < at; i++ {
		if argv[i] == "-e" && strings.HasPrefix(argv[i+1], "YOLO_PUBLISHED_PORTS=") {
			_ = json.Unmarshal([]byte(strings.TrimPrefix(argv[i+1], "YOLO_PUBLISHED_PORTS=")), &ports)
			argv = slices.Delete(argv, i, i+2)
			at -= 2
			break
		}
	}
	var args []string
	for _, lease := range leases {
		port := strconv.Itoa(lease.Port)
		args = append(args, "-p", "127.0.0.1:"+port+":"+port+"/tcp")
		if !slices.Contains(ports, port+"/tcp") {
			ports = append(ports, port+"/tcp")
		}
	}
	if !slices.Contains(argv, "net.ipv4.conf.all.route_localnet=1") {
		args = append(args, "--sysctl", "net.ipv4.conf.all.route_localnet=1")
	}
	args = append(args, "-e", "YOLO_PUBLISHED_PORTS="+jsonDumpsStrings(ports))
	return slices.Insert(argv, at, args...)
}

// The caller holds the existing cname lifecycle lock and proved runtime absence.
// Explicit-generation teardown never discovers a replacement generation.
func (o *Options) releaseGoneMCPCallbacks(cname, runtime string) {
	if o.mcpCallbackGeneration != "" {
		for _, lease := range o.mcpCallbackLeases {
			if lease.Launch == cname && lease.Runtime == runtime && lease.Generation == o.mcpCallbackGeneration {
				o.releaseCallbackLease(lease)
			}
		}
		return
	}
	fd, err := syscall.Open(o.callbackRoot(), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	directory := os.NewFile(uintptr(fd), o.callbackRoot())
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil || info.Mode().Perm() != 0700 {
		return
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "port-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "port-"), ".json"))
		if err != nil || name != "port-"+strconv.Itoa(port)+".json" {
			continue
		}
		lease, err := mcpsignin.ReadCallbackLease(o.callbackRoot(), port)
		if err == nil && lease.Launch == cname && lease.Runtime == runtime {
			o.releaseCallbackLease(lease)
		}
	}
}
