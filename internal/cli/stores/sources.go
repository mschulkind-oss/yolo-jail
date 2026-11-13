package stores

// sources.go is the machine's sources as `yolo ps` discovered them, read by the inventory
// (named-setups.md §12.5, slice 11f): each technology-bound section is collected once per listed
// container source, and a judgment that needs to know what is running reads the union of every
// source's jails, declining when a container source did not answer.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// StoresSchema names the JSON document and its version (named-setups.md §12.5).
const StoresSchema = "yolo.stores/1"

// Source is one technology's answer to "what is running", handed in by the CLI from `yolo ps`'s
// discovery, so the inventory execs nothing to learn it.
type Source struct {
	// Confinement is the technology: "podman", "apple-container", "seatbelt" or "host".
	Confinement string
	// Display names it in prose: "Podman", "Apple Container".
	Display string
	// Setup is the built-in setup whose `yolo prune` acts for this technology, "" for a
	// technology with no container store (Seatbelt, host).
	Setup string
	// State is the source's answer: listed, not-installed, unreachable or timed-out.
	State runtime.SourceState
	// Detail says why a source did not list; NextStep is the one command that helps.
	Detail   string
	NextStep string
	// Jails are the running jails it listed.
	Jails []SourceJail
}

// SourceJail is one running jail a source listed: its name, and its workspace when known.
type SourceJail struct {
	Name      string
	Workspace string
}

// SourceJSON is one source in the JSON document, the shape `yolo ps` prints.
type SourceJSON struct {
	Confinement string `json:"confinement"`
	State       string `json:"state"`
	Detail      string `json:"detail,omitempty"`
	NextStep    string `json:"next_step,omitempty"`
}

// runtimeOf is the container runtime binary a container technology answers through, "" for one
// that is no container runtime.
func runtimeOf(confinement string) string {
	switch confinement {
	case "podman":
		return "podman"
	case "apple-container":
		return "container"
	}
	return ""
}

// listedContainerSources are the container sources that listed, in discovery order: the ones
// whose technology-bound sections are collected.
func listedContainerSources(o Options) []Source {
	var out []Source
	for _, s := range o.Sources {
		if runtimeOf(s.Confinement) != "" && s.State == runtime.SourceListed {
			out = append(out, s)
		}
	}
	return out
}

// unansweredContainerSource is the first container source that did not list, and whether there is
// one. A source that is not installed did not answer either: a runtime off this PATH may still run
// jails (SUP-D53).
func unansweredContainerSource(o Options) (Source, bool) {
	for _, s := range o.Sources {
		if runtimeOf(s.Confinement) != "" && s.State != runtime.SourceListed {
			return s, true
		}
	}
	return Source{}, false
}

// sourcesDecline is the clause a liveness judgment declines with when a container source did not
// answer, naming it and its next step; "" when every container source listed.
func sourcesDecline(o Options) string {
	s, ok := unansweredContainerSource(o)
	if !ok {
		return ""
	}
	var out string
	if s.State == runtime.SourceNotInstalled {
		out = fmt.Sprintf("%s is not on this PATH, and a jail it runs may be using these bytes", s.Display)
	} else {
		why := string(s.State)
		if s.Detail != "" {
			why += ": " + s.Detail
		}
		out = fmt.Sprintf("%s did not answer (%s), and a jail it runs may be using these bytes", s.Display, why)
	}
	if s.NextStep != "" {
		out += "; " + s.NextStep
	}
	return out
}

// liveJails is the union of every source's running jails. Known is false when a container
// source did not list, so a judgment reading it declines rather than read "nothing runs".
func liveJails(o Options) runtime.LiveSet {
	live := runtime.LiveSet{Known: true, Names: map[string]struct{}{}}
	if _, ok := unansweredContainerSource(o); ok {
		live.Known = false
	}
	for _, s := range o.Sources {
		for _, j := range s.Jails {
			live.Names[j.Name] = struct{}{}
		}
	}
	return live
}

// sourceWorkspaces are the workspaces of every running jail any source listed, each resolved and
// named once, in sorted order: the projects the per-project rows are made for, merged across
// sources so a project two technologies run gets one row.
func sourceWorkspaces(o Options) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range o.Sources {
		for _, j := range s.Jails {
			if j.Workspace == "" {
				continue
			}
			ws := j.Workspace
			if r, err := filepath.EvalSymlinks(ws); err == nil {
				ws = r
			}
			if !seen[ws] {
				seen[ws] = true
				out = append(out, ws)
			}
		}
	}
	sort.Strings(out)
	return out
}

// techTrigger is the command that reclaims a technology-bound row for src's technology (§12.5):
// `yolo prune` acts for one technology, so the trigger names it.
func techTrigger(src Source) string {
	return "`YOLO_SETUP=" + src.Setup + " yolo prune --apply`"
}

// tagTechnology marks rows as src's: each carries src's confinement, and a trigger or note naming
// the plain prune names src's own instead.
func tagTechnology(rows []Store, src Source) []Store {
	for i := range rows {
		r := &rows[i]
		r.Confinement = src.Confinement
		r.Reclaimer.Trigger = withTechPrune(r.Reclaimer.Trigger, src)
		r.Note = withTechPrune(r.Note, src)
	}
	return rows
}

// withTechPrune rewrites each `yolo prune --apply` in text, quoted or not, to src's own.
func withTechPrune(text string, src Source) string {
	const quoted = "`yolo prune --apply`"
	const bare = "yolo prune --apply"
	if text == "" || src.Setup == "" {
		return text
	}
	parts := strings.Split(text, quoted)
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, bare, techTrigger(src))
	}
	return strings.Join(parts, techTrigger(src))
}

// complete is whether every source listed or is not installed, `yolo ps`'s flag (§12.4), which a
// script reads instead of an exit code.
func complete(o Options) bool {
	for _, s := range o.Sources {
		if s.State != runtime.SourceListed && s.State != runtime.SourceNotInstalled {
			return false
		}
	}
	return true
}

// sourcesJSON is the sources in the JSON document.
func sourcesJSON(o Options) []SourceJSON {
	out := []SourceJSON{}
	for _, s := range o.Sources {
		out = append(out, SourceJSON{Confinement: s.Confinement, State: string(s.State),
			Detail: s.Detail, NextStep: s.NextStep})
	}
	return out
}

// techKey is a technology-bound row's ledger key: a Podman row keeps the key it always had, so a
// Linux machine's history goes on; another technology's row is suffixed with its confinement, so
// its samples never mix with Podman's. Apple Container's image and scratch rows were never
// measured (unknown and absent), so no history is lost by its keys changing; its tool disks, which
// only it has, keep theirs (toolDiskStores does not call this).
func techKey(key string, src Source) string {
	if src.Confinement == "podman" {
		return key
	}
	return key + "@" + src.Confinement
}
