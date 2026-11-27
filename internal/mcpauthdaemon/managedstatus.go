package mcpauthdaemon

import (
	"errors"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ReadHostStatus reads only derives already named by the composed selected share.
// It starts no broker, constructs no writable store and never takes a source lock.
func ReadHostStatus(stateDir string, share mcpauth.Share, requested ...string) []map[string]any {
	reader := &broker{share: share}
	if err := reader.loadScripts(); err != nil {
		return readOnceStatus(stateDir, share, nil, "not determinable without effect", true, requested...)
	}
	return readOnceStatus(stateDir, share, reader.onceSources(), "not determinable without effect; running launcher session or jail poll", true, requested...)
}

// ReadOnceStatus takes only the existing canonical read lock and unlocked source
// snapshots. A literal descriptor cannot create or repair missing state. It answers a
// jail, so it lists only the servers this share names: the machine-wide record file also
// holds other launches' records, which a jail must not see. ReadHostStatus adds those.
func ReadOnceStatus(stateDir string, share mcpauth.Share, sources []mcpauth.OnceSource, refresher string, requested ...string) []map[string]any {
	return readOnceStatus(stateDir, share, sources, refresher, false, requested...)
}

func readOnceStatus(stateDir string, share mcpauth.Share, sources []mcpauth.OnceSource, refresher string, includeOrphans bool, requested ...string) []map[string]any {
	store := &mcpauth.CanonicalStore{Dir: filepath.Join(stateDir, "once"), Filename: "records.json", LockName: "records.lock", Wait: mcpauth.HeaderReadWait}
	file, readErr := store.ReadStatus()
	availability := "readable"
	if readErr != nil {
		availability = "unavailable"
		if errors.Is(readErr, fs.ErrNotExist) {
			availability = "missing"
		} else if errors.Is(readErr, mcpauth.ErrStoreBusy) {
			availability = "busy"
		}
	}
	snapshots := make([]mcpauth.SourceSnapshot, len(sources))
	for i, source := range sources {
		snapshots[i] = source.Store.Snapshot()
	}
	rows := make([]map[string]any, 0, len(share.Once))
	used := map[string]bool{}
	for _, server := range share.Once {
		if len(requested) > 0 && requested[0] != "" && server.Name != requested[0] {
			continue
		}
		row := map[string]any{"name": server.Name, "scope": "user", "sign_in": "once", "state": "not-signed-in", "identity": server.Identity, "url": server.URL, "transport": server.Transport, "expected_client_id": server.ExpectedClientID, "configuration": map[string]any{"scope": "user", "deciding_layer": "frozen user composition", "own_client_set": server.ExpectedClientID != ""}, "callback": "not requested", "refresher": refresher, "writeback": "current", "canonical_availability": availability}
		input := mcpsignin.DiagnosisInput{Server: server.Name, URL: server.URL, Once: true, Record: "none", Source: "no store", Capturable: false, Mode: "no source store", Busy: availability == "busy"}
		if availability == "unavailable" {
			input.Source = "unavailable"
		}
		record := (*mcpauth.Record)(nil)
		for i := range file.Records {
			r := &file.Records[i]
			if r.Server == server.Name && r.Identity == server.Identity {
				record = r
				used[r.Server+"|"+r.Identity] = true
				break
			}
		}
		canonical := map[string]any{"state": "none"}
		if record != nil {
			input.Record = record.State
			if input.Record == "live" && record.ExpiresAtMS <= time.Now().UnixMilli() {
				input.Record = "expired"
			}
			row["state"], row["generation"], row["expires_at_ms"], row["adopted_from"] = record.State, int64(record.Generation), record.ExpiresAtMS, record.AdoptedFrom
			canonical = map[string]any{"state": input.Record, "generation": int64(record.Generation), "expires_at_ms": record.ExpiresAtMS, "adopted_from": record.AdoptedFrom, "native_name": "not determinable without effect"}
		}
		row["record"] = canonical
		if note, found, err := store.ReadOutcome(server.Name, server.Identity); err != nil {
			row["outcome"] = "unavailable"
		} else if found {
			input.Outcome = string(note.Code)
			row["outcome"] = map[string]any{"version": note.Version, "server": note.Server, "identity": note.Identity, "native_name": note.NativeName, "code": string(note.Code), "at": note.At.UTC().Format(time.RFC3339Nano), "generation": int64(note.Generation)}
			if record != nil && note.Generation == record.Generation {
				canonical["native_name"] = note.NativeName
			}
		} else {
			row["outcome"] = "none recorded"
		}
		points, deliveries := []any{}, []any{}
		if share.SignIn != nil {
			for _, decision := range share.SignIn.Decisions {
				if decision.Server != server.Name {
					continue
				}
				pointRow := map[string]any{"agent": decision.Agent, "reason": decision.Reason, "capturable": decision.Capturable, "state": "none", "alternatives": pointMaps(decision.Alternatives)}
				delivery := "native sign-in point"
				reason := decision.Reason
				if decision.HeaderForm != "" && ((decision.Point != nil && decision.Capturable) || decision.UsableRecord) {
					delivery = "header"
				} else if decision.HeaderForm == "" {
					delivery = "delivered plain"
					reason = "header route unsupported"
				}
				deliveries = append(deliveries, map[string]any{"agent": decision.Agent, "state": delivery, "reason": reason})
				if decision.Point != nil {
					pointRow["state"] = "managed, placed"
					pointRow["point"] = pointMap(*decision.Point)
					if input.Point == nil {
						input.Point = decision.Point
						input.Agent = decision.Agent
						input.Capturable = decision.Capturable
						input.Mode = decision.Reason
						if input.Mode == "" && !decision.Capturable {
							input.Mode = "credential view, alternate storage, or Mac host"
						}
						input.Delivery = delivery
						input.DeliveryReason = reason
						input.HeaderSource = delivery == "header"
					}
					if decision.Point.CallbackRoute {
						row["callback"] = "not published on this backend"
						input.Callback = "not published on this backend"
					}
				} else if decision.Reason != "no sign-in point" {
					pointRow["state"] = "bundle not usable"
				}
				points = append(points, pointRow)
			}
		}
		callback := mcpsignin.ObserveCallback(filepath.Join(filepath.Dir(stateDir), "mcp-callback"), share.SignIn, server.Name)
		if share.SignIn != nil && (len(share.SignIn.Callbacks) > 0 || share.SignIn.CallbackGeneration != "") {
			row["callback"] = callback.Disposition
			input.Callback = callback.Disposition
			if callback.Disposition == "held" {
				row["callback"] = "held by " + callback.Owner
			}
			if callback.Port != 0 {
				row["callback_port"] = int64(callback.Port)
				row["callback_ipv4_only"] = true
			}
			if callback.Owner != "" {
				row["callback_owner"] = callback.Owner
			}
		}
		sourceRows := []any{}
		for i, source := range sources {
			snapshot := snapshots[i]
			state := string(snapshot.State)
			srcRow := map[string]any{"pack": source.Pack, "path": filepath.Join(source.Store.Dir, source.Store.File), "classification": state, "capturable": true, "state": "no entry"}
			if snapshot.State != mcpauth.SourceParsed {
				srcRow["state"] = state
				if state == "malformed" || state == "unsafe" || state == "unavailable" {
					input.Source = state
				}
			} else {
				srcRow["state"] = "no entry"
				for key, raw := range snapshot.Entries {
					if source.Match == nil {
						continue
					}
					url, _, matches := source.Match(key, raw)
					if !matches || url != server.URL {
						continue
					}
					public := mcpauth.ProjectNativeSourceStatus(source, raw, key, record)
					native := public.NativeName
					srcRow["native_name"], srcRow["refresh_present"], srcRow["expires_at_ms"] = native, public.RefreshPresent, public.ExpiresAtMS
					if public.RefreshPresent {
						srcRow["state"] = "entry awaiting adoption"
						input.Source = "entry awaiting adoption"
					} else {
						srcRow["state"] = "managed entry"
						if public.PendingWriteback {
							row["writeback"] = "pending"
						}
					}
					if input.Point == nil && native != "" {
						input.Point = &mcpsignin.Point{NativeName: native}
						input.Agent = source.Pack
						input.Capturable = true
						input.Mode = "bridged"
						points = append(points, map[string]any{"agent": source.Pack, "state": "unmanaged", "native_name": native, "capturable": true})
					}
				}
			}
			sourceRows = append(sourceRows, srcRow)
		}
		row["points"], row["source_stores"], row["delivery"] = points, sourceRows, deliveries
		diagnosis := mcpsignin.Diagnose(input)
		if diagnosis.Verdict == "no sign-in point" {
			var context *mcpsignin.AdviceContext
			if share.SignIn != nil {
				context = share.SignIn.Advice
			}
			candidates := packload.ProvisioningCandidates(packload.EmbeddedFS(), mcpsignin.Once{Name: server.Name, URL: server.URL, Transport: server.Transport, ExpectedClientID: server.ExpectedClientID}, context)
			advice := []any{}
			for _, candidate := range candidates {
				advice = append(advice, map[string]any{"state": "candidate", "pack": candidate.Pack, "origin": candidate.Origin, "revision": candidate.Revision})
			}
			row["provisioning"] = advice
			row["provisioning_state"] = "none derivable"
			if len(candidates) > 0 {
				row["provisioning_state"] = "candidate"
			}
			diagnosis.NextStep = packload.ProvisioningRecovery(mcpsignin.Once{Name: server.Name, URL: server.URL}, candidates)
		}
		row["diagnosis"], row["next_step"] = diagnosis.Verdict, diagnosis.NextStep
		rows = append(rows, row)
	}
	for _, record := range file.Records {
		if !includeOrphans {
			break
		}
		if len(requested) > 0 && requested[0] != "" && record.Server != requested[0] {
			continue
		}
		if used[record.Server+"|"+record.Identity] {
			continue
		}
		rows = append(rows, map[string]any{"name": record.Server, "identity": record.Identity, "state": "retained, unused", "record": map[string]any{"state": record.State, "generation": int64(record.Generation), "expires_at_ms": record.ExpiresAtMS}, "next_step": "retained canonical state is unused by this composition; no sign-in is required"})
	}
	return rows
}

func pointMap(p mcpsignin.Point) map[string]any {
	return map[string]any{"pack": p.Pack, "native_name": p.NativeName, "origin": p.Origin, "revision": p.Revision, "client_id": p.ClientID, "callback_port": p.CallbackPort, "callback_route": p.CallbackRoute}
}
func pointMaps(points []mcpsignin.Point) []any {
	out := []any{}
	for _, p := range points {
		out = append(out, pointMap(p))
	}
	return out
}
