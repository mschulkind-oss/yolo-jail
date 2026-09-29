package image

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// storespec.go answers the second store-write question, the one storewrite.go's
// namespace decision left open: WHICH containers-storage does the copier write?
//
// # The copier and podman can disagree about the answer
//
// A bare `containers-storage:<ref>` destination leaves the store to the copier's
// own containers/storage library, which looks it up from storage.conf. podman
// looks it up too, from the same files, and for a long time the two lookups
// agreed. They stopped agreeing in containers/storage v1.63.0 (upstream commit
// 5eaf7d2ba1, "storage/types: rework config files parsing"): from that version
// on, a rootless process takes `runroot`/`graphroot` from the FIRST storage.conf
// it finds, whereas the older library a rootless podman may still carry (podman
// 5.7.0 vendors v1.61.0) read only the user's own file and always built rootless
// paths otherwise. The copier this flake builds is on v1.64.1.
//
// So on a host whose only storage.conf is a distro file that spells the ROOT
// store (stock Ubuntu 26.04 ships /usr/share/containers/storage.conf with
// `runroot = "/run/containers/storage"`), rootless podman reads the rootless
// store under the user's home while the copier, inside `podman unshare`, resolves
// the root one and dies creating it: `Invalid destination name
// containers-storage:…: mkdir /run/containers: permission denied` (issue #47).
// Nothing about the namespace is wrong; the two processes simply mean different
// stores.
//
// # The fix is to stop asking the copier
//
// The launch already runs one `podman info --format json` to decide the
// namespace. That same output names the store podman uses — driver, graph root,
// run root — so the destination names it too, in the transport's own store
// syntax: `containers-storage:[driver@graphroot+runroot:options]ref`. Given BOTH
// roots and a driver, the copier's storage library opens exactly those
// directories and takes no path from any storage.conf (store.go GetStore: the
// config's defaults are used only when every one of the four fields is empty).
// It is done for a rootful podman as well: there the two lookups already agree,
// and naming the store makes that agreement a fact of the argv rather than of the
// host's config files.
//
// # What an explicit store costs, and why the options travel with it
//
// The same GetStore rule that stops the config's paths also stops the config's
// DRIVER OPTIONS: with an explicit spec the copier gets exactly the options the
// spec's `:options` suffix carries and nothing else. So the options podman
// reports (`store.graphOptions`) are carried, which is the closest thing to
// today: on a host where podman and the copier read one file, the copier already
// parsed every one of these. Two shapes cannot be spelled, because the suffix is
// split on commas and ends at the first `]` — a value containing either (an
// `overlay.mountopt` of `nodev,metacopy=on`), and a value that is not a string
// (the additional image stores list). Those are dropped and NAMED on the launch
// line. `overlay.mount_program`, the one podman renders as an object, is carried
// as its Executable. What the spec cannot carry at all — the config's image
// store, pull options, a rootful `remap-uids` — never shaped where layers land
// for a copy into podman's own graph root.
//
// # Tri-state
//
// A store that `podman info` did not report, or reported in a shape the spec
// cannot express (a relative path, or a `:`, `]` or — in the graph root — `+`,
// each a delimiter the transport cuts at its first occurrence), is NOT GUESSED.
// The destination is then today's bare `containers-storage:<ref>` and the launch
// line says the copier is picking its own store.

// PodmanStore is the containers-storage podman reports it uses.
type PodmanStore struct {
	Driver    string
	GraphRoot string
	RunRoot   string
	// ConfigFile is the storage.conf podman says it read — reported, never used to
	// decide anything.
	ConfigFile string
	// Options are the driver options carried in the spec, `key=value`, sorted.
	Options []string
	// Dropped names the options podman reported that the spec cannot spell.
	Dropped []string
}

// Spec is the store in the transport's syntax, without the brackets:
// `driver@graphroot+runroot[:opt,opt]`.
func (s PodmanStore) Spec() string {
	spec := s.Driver + "@" + s.GraphRoot + "+" + s.RunRoot
	if len(s.Options) > 0 {
		spec += ":" + strings.Join(s.Options, ",")
	}
	return spec
}

// PodmanStoreFacts is everything one `podman info` read tells a delivery: the
// namespace answer and the store.
type PodmanStoreFacts struct {
	Rootless PodmanRootless
	Store    PodmanStore
	// StoreKnown is false when Store must not be used; Unknown then says why.
	StoreKnown bool
	Unknown    string
}

// podmanInfoForStore is the slice of `podman info --format json` a delivery reads.
// Rootless is a POINTER so an absent field stays "unknown" rather than decoding as
// false — the two want opposite branches (StoreWritePrefix).
type podmanInfoForStore struct {
	Host struct {
		Security struct {
			Rootless *bool `json:"rootless"`
		} `json:"security"`
	} `json:"host"`
	Store *struct {
		ConfigFile      string                     `json:"configFile"`
		GraphDriverName string                     `json:"graphDriverName"`
		GraphRoot       string                     `json:"graphRoot"`
		RunRoot         string                     `json:"runRoot"`
		GraphOptions    map[string]json.RawMessage `json:"graphOptions"`
	} `json:"store"`
}

// ReadPodmanStoreFacts runs ONE `podman info --format json` (PodmanInfoCmd) and
// reads both answers from it. capture runs an argv and returns its stdout;
// ok=false for anything that did not run cleanly.
func ReadPodmanStoreFacts(runtime string, capture func(argv []string) (string, bool)) PodmanStoreFacts {
	if capture == nil {
		return PodmanStoreFacts{Unknown: "`podman info` was not asked"}
	}
	out, ok := capture(PodmanInfoCmd(runtime))
	if !ok {
		return PodmanStoreFacts{Unknown: "`podman info` did not run cleanly"}
	}
	var info podmanInfoForStore
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return PodmanStoreFacts{Unknown: "`podman info` did not print JSON"}
	}
	facts := PodmanStoreFacts{Rootless: RootlessUnknown}
	if r := info.Host.Security.Rootless; r != nil {
		facts.Rootless = RootlessNo
		if *r {
			facts.Rootless = RootlessYes
		}
	}
	if info.Store == nil {
		facts.Unknown = "`podman info` reported no store"
		return facts
	}
	st := PodmanStore{
		Driver:     info.Store.GraphDriverName,
		GraphRoot:  info.Store.GraphRoot,
		RunRoot:    info.Store.RunRoot,
		ConfigFile: info.Store.ConfigFile,
	}
	if why := unspellableStore(st); why != "" {
		facts.Unknown = why
		return facts
	}
	st.Options, st.Dropped = spellDriverOptions(info.Store.GraphOptions)
	facts.Store, facts.StoreKnown = st, true
	return facts
}

// unspellableStore says why a store cannot be named in the transport's spec, ""
// when it can. Each refused character is one ParseReference cuts at its FIRST
// occurrence (vendored go.podman.io/image/v5/storage/storage_transport.go), so a
// path containing it would silently name a different directory.
func unspellableStore(s PodmanStore) string {
	switch {
	case s.Driver == "":
		return "`podman info` reported no graphDriverName"
	case s.GraphRoot == "":
		return "`podman info` reported no graphRoot"
	case s.RunRoot == "":
		return "`podman info` reported no runRoot"
	case strings.ContainsAny(s.Driver, "@:+],"):
		return fmt.Sprintf("the driver name %q cannot be spelled in a store spec", s.Driver)
	}
	for _, p := range []struct{ name, val, bad string }{
		{"graphRoot", s.GraphRoot, ":+]"},
		{"runRoot", s.RunRoot, ":]"},
	} {
		if !path.IsAbs(p.val) {
			return fmt.Sprintf("%s %q is not an absolute path", p.name, p.val)
		}
		if strings.ContainsAny(p.val, p.bad) {
			return fmt.Sprintf("%s %q contains one of %q, which a store spec cannot carry", p.name, p.val, p.bad)
		}
	}
	return ""
}

// spellDriverOptions turns podman's rendered graphOptions back into the
// `key=value` driver options they came from. A string value is itself;
// mount_program's object is its Executable; anything else, or a spelling the
// suffix cannot carry, is dropped by key. Both lists are sorted, so the argv is
// the same on every launch.
func spellDriverOptions(opts map[string]json.RawMessage) (carried, dropped []string) {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		val, ok := driverOptionValue(opts[k])
		opt := k + "=" + val
		if !ok || k == "" || val == "" || strings.ContainsAny(k, "=,]") || strings.ContainsAny(val, ",]") {
			dropped = append(dropped, k)
			continue
		}
		carried = append(carried, opt)
	}
	return carried, dropped
}

func driverOptionValue(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var prog struct {
		Executable *string `json:"Executable"`
	}
	if json.Unmarshal(raw, &prog) == nil && prog.Executable != nil {
		return *prog.Executable, true
	}
	return "", false
}

// ContainersStorageDestFor is the destination a podman-on-Linux delivery copies
// to: the store podman reports, named explicitly, when it is known; today's bare
// ContainersStorageDest otherwise. The ref after the bracket is unchanged, so
// every name lookup (inspect, tag, the reapers, the identity check) is untouched.
func ContainersStorageDestFor(facts PodmanStoreFacts, contentRef string) string {
	if !facts.StoreKnown {
		return ContainersStorageDest(contentRef)
	}
	return "containers-storage:[" + facts.Store.Spec() + "]" + contentRef
}

// storeWrite is the whole podman-on-Linux delivery decision from one read: the
// namespace prefix and the destination. AutoLoadImage and DeliveryCopyArgvFor
// both come through here, so the two cannot drift.
func storeWrite(runtime string, facts PodmanStoreFacts, contentRef string) (dest string, prefix []string) {
	return ContainersStorageDestFor(facts, contentRef), StoreWritePrefix(runtime, facts.Rootless)
}

// DeliveryCopyArgvFor is the complete argv a podman-on-Linux launch runs for one
// delivery, for callers outside this package that must run the same copy: the
// integration harness's own image load and the macOS in-VM copier experiment.
func DeliveryCopyArgvFor(runtime string, facts PodmanStoreFacts, copier, imageJSON, contentRef string) []string {
	dest, prefix := storeWrite(runtime, facts, contentRef)
	return copyArgv(prefix, copier, imageJSON, dest)
}

// StorePreflight is `yolo check`'s store line: the store a launch will copy into,
// or a WARNING when podman answered but its store cannot be named — that launch
// will let the copier choose from storage.conf, which is issue #47's failure, and
// `podman unshare -- /bin/sh -c :` passing says nothing about it. Silent when
// podman did not answer at all (UnsharePreflight's tri-state rule).
func StorePreflight(facts PodmanStoreFacts) DeliveryPreflight {
	if facts.Rootless == RootlessUnknown && !facts.StoreKnown {
		return DeliveryPreflight{}
	}
	if !facts.StoreKnown {
		return DeliveryPreflight{
			Line: "Image store: yolo cannot name podman's store (" + facts.Unknown + "), so a " +
				"launch lets the copier pick one from its own storage.conf lookup",
			Warn: true,
			Hint: "If the copier's pick is not podman's, every image copy fails. Check " +
				"`podman info --format '{{.Store.GraphRoot}} {{.Store.RunRoot}}'`; a " +
				"~/.config/containers/storage.conf naming that store makes the two agree.",
		}
	}
	line := "Image store: " + facts.Store.Spec() + " (podman's own"
	if facts.Store.ConfigFile != "" {
		line += ", from " + facts.Store.ConfigFile
	}
	line += ") — a launch copies into exactly this store"
	return DeliveryPreflight{Line: line}
}

// storeLine is the launch's second store-write line: which store the copy goes
// to, or that yolo does not know and the copier is choosing.
func storeLine(facts PodmanStoreFacts) string {
	if !facts.StoreKnown {
		return "  Image store: yolo could not read podman's store (" + facts.Unknown + "), so the " +
			"copier picks one from its own storage.conf lookup. If that is not the store podman " +
			"reads, the copy below fails; a ~/.config/containers/storage.conf naming podman's " +
			"store makes the two agree."
	}
	line := "  Image store: " + facts.Store.Spec() + " — the store `podman info` reports"
	if facts.Store.ConfigFile != "" {
		line += " (podman read " + facts.Store.ConfigFile + ")"
	}
	line += ", named on the copy so the copier cannot resolve another."
	if len(facts.Store.Dropped) > 0 {
		line += " Driver options not carried: " + strings.Join(facts.Store.Dropped, ", ") + "."
	}
	return line
}
