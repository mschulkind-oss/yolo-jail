package run

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	reasons := map[int]string{}
	// Callback publications are disjoint from forward_host_ports (the opposite direction): a
	// forward already binds that jail-side port.
	_, forwards := briefingReadablePortsFor("bridge", in.cfg, cfgMap(in.cfg, "providers"))
	if parsed, err := ParsePortForwards(forwards, nil); err == nil {
		for i := range facts.Callbacks {
			row := &facts.Callbacks[i]
			if row.Disposition != "publication pending" {
				continue
			}
			for _, forward := range parsed {
				if forward.LocalPort == row.Port {
					row.Disposition = "port busy"
					reasons[i] = fmt.Sprintf("port %d is also a forward_host_ports entry; change that forward or the MCP server's callback port to publish it", row.Port)
				}
			}
		}
	}
	if o.DryRun {
		return
	}
	eligible := slices.ContainsFunc(facts.Callbacks, func(row mcpsignin.Callback) bool { return row.Disposition == "publication pending" })
	if !eligible {
		o.reportMCPCallbacks(facts.Callbacks, reasons)
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
			reasons[i] = "this launch's launch lock is not held"
			continue
		}
		release, owned := o.callbackLifecycleExclusion(in.cname, in.cname)
		if !owned {
			row.Disposition = "unavailable"
			reasons[i] = "the launch lock " + launchLockPath(in.cname) + " is not a regular file you own, or others can write it; fix its owner and mode"
			continue
		}
		release()
		lease := mcpsignin.CallbackLease{Version: 1, Port: row.Port, Launch: in.cname, Runtime: in.rt, Generation: generation}
		row.Disposition, row.Owner = o.acquireCallbackLease(lease)
		if row.Disposition == "unavailable" {
			reasons[i] = "the callback lease state under " + o.callbackRoot() + " could not be read or written privately; fix that directory's owner and mode"
		}
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
			reasons[i] = "yolo could not check whether the port is free"
			if known {
				row.Disposition = "port busy"
				delete(reasons, i)
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
				reasons[i] = "the launch receipt could not be written under " + o.callbackRoot() + "; fix that directory's owner and mode"
			}
		}
	}
	in.callbacks = slices.Clone(o.mcpCallbackLeases)
	o.reportMCPCallbacks(facts.Callbacks, reasons)
}

func (o *Options) reportMCPCallbacks(rows []mcpsignin.Callback, reasons map[int]string) {
	for i, row := range rows {
		if row.Disposition == "not requested" {
			continue
		}
		disposition := row.Disposition
		if disposition == "held" {
			disposition += " by " + row.Owner
		}
		if reason := reasons[i]; reason != "" {
			disposition += " (" + reason + ")"
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
		o.removeCallbackReceiptFiles(o.mcpCallbackGeneration)
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
			o.removeCallbackReceiptFiles(lease.Generation)
		}
	}
}

// removeCallbackReceiptFiles deletes a retired launch's receipt and its failure notes, which
// would otherwise accumulate one set per launch. The caller has proved the runtime gone; a
// generation that is not a valid key names no path.
func (o *Options) removeCallbackReceiptFiles(generation string) {
	path := mcpsignin.CallbackReceiptPath(o.callbackRoot(), generation)
	if path == "" {
		return
	}
	_ = os.Remove(path)
	notes, _ := filepath.Glob(path + ".failure-*")
	for _, note := range notes {
		_ = os.Remove(note)
	}
}
