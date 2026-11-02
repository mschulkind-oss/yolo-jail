package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// loopholegrantcarried.go is the approval record's CARRIED GRANTS, a term coined here: a grant
// `yolo config migrate` wrote into a workspace's approval record from the per-workspace switch
// file a host-side `yolo loopholes enable` left (docs/reference/loophole-grants.md §6, the
// switch-file row; LG-D11). That command was the human's approval, so the grant is recorded; but
// it was a switch, not a request, so the project's files do not ask for it. A carried grant is
// read as this project's request until a file states it, so the grant stands meanwhile, and
// `yolo check` names it with the `yolo request` line that writes it (UnstatedRecordGrantNotes).
//
// The record's grant part holds them beside the pack's own list, under `carried:<pack>`, a key
// no pack can take (a pack's name has no colon). The pack's own list holds the same grants, so
// a reader that knows nothing of the mark still reads them as recorded. The gate drops a mark once
// a file states the grant or the gate no longer holds it (compareLoopholeGrants), so removing the
// request afterwards withdraws the grant as it does for any other.

const carriedRecordPrefix = "carried:"

func carriedRecordKey(pack string) string { return carriedRecordPrefix + pack }

func isCarriedRecordKey(key string) bool { return strings.HasPrefix(key, carriedRecordPrefix) }

// carriedLoopholeGrants is rec's carried grants, by pack.
func carriedLoopholeGrants(rec map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range rec {
		if pack, ok := strings.CutPrefix(k, carriedRecordPrefix); ok && len(v) > 0 {
			out[pack] = append([]string(nil), v...)
		}
	}
	return out
}

// layCarriedRequests adds to requests (a copy the caller owns) each carried grant no file
// requests, and returns those grants by pack. A pack whose request table is written "off", or
// is not a table, is left as written: that is the project's word, or the resolver's error.
func layCarriedRequests(decls []LoopholeGrantDeclaration, requests *jsonx.OrderedMap, carried map[string][]string) map[string][]string {
	open := map[string][]string{}
	for _, d := range decls {
		grants := carried[d.Pack]
		if len(grants) == 0 {
			continue
		}
		existing, has := requests.Get(d.Pack)
		if has && mapOrNil(existing) == nil {
			continue
		}
		packReq := shallowCopyMap(mapOrNil(existing))
		for _, grant := range grants {
			kind, value, valued := strings.Cut(grant, "=")
			g, declared := loopholeGrantByKind(d.Grants, kind)
			if !declared || g.Valued != valued {
				continue
			}
			raw, written := packReq.Get(kind)
			if !g.Valued {
				if written {
					continue // the file states it, true or false: its word stands
				}
				packReq.Set(kind, true)
				open[d.Pack] = append(open[d.Pack], grant)
				continue
			}
			var list []any
			if written {
				values, err := readableStringList(raw, "requests."+d.Pack+"."+kind)
				if err != nil {
					continue // the resolver reports the misshapen request
				}
				if inList(values, value) {
					continue
				}
				for _, v := range values {
					list = append(list, v)
				}
			}
			packReq.Set(kind, append(list, value))
			open[d.Pack] = append(open[d.Pack], grant)
		}
		requests.Set(d.Pack, packReq)
	}
	return open
}

// CarryLoopholeGrants adds grants, `kind` or `kind=value` by pack, to workspace's approval
// record as carried grants: each joins the pack's recorded list and its carried mark. It is
// `yolo config migrate`'s write of a switch file's grants (§6), made before the file is removed.
func CarryLoopholeGrants(workspace string, grants map[string][]string) error {
	if len(grants) == 0 {
		return nil
	}
	rec, _, err := readLoopholeGrantRecord(workspace)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = loopholeGrantRecord{}
	}
	for pack, list := range grants {
		rec[pack] = appendUnique(rec[pack], list...)
		rec[carriedRecordKey(pack)] = appendUnique(rec[carriedRecordKey(pack)], list...)
	}
	return writeLoopholeGrantRecord(workspace, rec)
}

// UnstatedRecordGrantNotes is, for each carried grant the gate holds that this project's files do
// not state, the note `yolo check` prints, naming the `yolo request` line that writes it.
func (s *ScopeCheck) UnstatedRecordGrantNotes() []string {
	if !s.grantsInPlay() {
		return nil
	}
	var out []string
	for _, src := range s.LoopholeGrants {
		byKind := map[string][]string{}
		var kinds []string
		for _, grant := range src.carried {
			kind, value, valued := strings.Cut(grant, "=")
			if _, seen := byKind[kind]; !seen {
				kinds = append(kinds, kind)
				byKind[kind] = nil
			}
			if valued {
				byKind[kind] = append(byKind[kind], termsafe.Visible(value))
			}
		}
		for _, kind := range kinds {
			values := byKind[kind]
			sort.Strings(values)
			what, args := src.Pack+" "+kind, ""
			if len(values) > 0 {
				what += " " + strings.Join(values, ", ")
				args = " " + strings.Join(values, " ")
			}
			out = append(out, fmt.Sprintf("the approval record grants %s to this project, carried from `yolo loopholes enable` "+
				"by `yolo config migrate`, but this project's files do not request it.\n"+
				"  It stands until they do. To write the request (in the workspace, or in its jail):\n"+
				"    yolo request %s %s%s", what, src.Pack, kind, args))
		}
	}
	return out
}

// LoopholeStandingInput is what one workspace's loophole grant standings are read from, for a
// caller that holds the configs rather than reading them from disk (`yolo config migrate`'s
// proof): the user and workspace configs as read, the switch file's switches, the approval
// record's grant part (its carried marks included), and the selected grant-declaring loopholes.
type LoopholeStandingInput struct {
	User, Workspace *jsonx.OrderedMap
	Switches        map[string]bool
	Record          map[string][]string
	Declarations    []LoopholeGrantDeclaration
}

// LoopholeStandings is each declared kind's standing, by pack, as a launch's gate would read it
// from in, before it asks: granted (with a valued kind's values), pending, requestable or
// not_allowed (§4.6).
func LoopholeStandings(in LoopholeStandingInput) (map[string]map[string]LoopholeGrantStanding, error) {
	if len(in.Declarations) == 0 {
		return map[string]map[string]LoopholeGrantStanding{}, nil
	}
	input, legacyRecorded, carried := withLegacyLoopholeGrantsFrom(LoopholeGrantInput{User: in.User, Workspace: in.Workspace,
		Declarations: in.Declarations, UserOrigins: map[string][]string{}, WorkspaceOrigins: map[string][]string{}},
		nil, nil, in.Switches, "", carriedLoopholeGrants(in.Record))
	plan, err := ResolveLoopholeGrants(input)
	if err != nil {
		return nil, err
	}
	check := &ScopeCheck{}
	for _, p := range plan.Packs {
		check.LoopholeGrants = append(check.LoopholeGrants, LoopholeGrantSource{Pack: p.Pack, Loophole: p.Loophole,
			Access: p.Access, Kinds: p.Kinds, Skipped: p.Skipped, legacyRecorded: legacyRecorded[p.Pack], carried: carried[p.Pack]})
	}
	out := check.loopholeGrantStandings(func(pack, grant string) bool {
		return inList(in.Record[pack], grant) || legacyRecordHolds(legacyRecorded[pack], grant)
	})
	return out, nil
}

// RecordedLoopholeGrantPart is workspace's approval record grant part as written, carried marks
// included, for `yolo config migrate`'s proof; empty when there is none or it does not read.
func RecordedLoopholeGrantPart(workspace string) map[string][]string {
	rec, _, err := readLoopholeGrantRecord(workspace)
	out := map[string][]string{}
	if err != nil {
		return out
	}
	for k, v := range rec {
		out[k] = append([]string(nil), v...)
	}
	return out
}
