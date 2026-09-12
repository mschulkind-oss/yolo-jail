package cli

// configpromotewrite.go is §10 step 5 of docs/design/config-ownership-and-promotion.md: the
// WRITE half of `yolo config promote` — resolving a destination, declaring the keys there,
// and resetting them out of the capture overlay so the value is declared in exactly one
// place.
//
// # Steps 5 and 6 are ONE logical write
//
// §5.2: "if any part fails the whole promotion is abandoned with nothing changed: a
// half-promoted key is declared AND captured, which is the double-declaration the verb
// exists to end". There is no transaction across two files, so the substitute is a
// pre-image per file and a rollback that restores every one of them — which is exactly the
// contract agentcfg.DeleteOverlayKeys returns its pre-image for, and why the manifest write
// keeps its own.
//
// # §5.2 step 7 (record the move in the destination's provenance) is NOT built
//
// Nothing in the tree carries per-pack provenance, and the two ways to invent it are both
// worse than the gap. A field on the contribution would have to be added to packdecl's
// closed vocabulary (which DisallowUnknownFields makes a hard refusal for every older yolo
// that reads the pack) to record something no reader consumes. A sidecar beside pack.json
// would be provenance nobody can read, which is the exact failure `config diff`'s R3
// reporting exists to fix. What DOES record the move is the destination file itself: it is
// the user's own, hand-readable, and usually in git — which is what [OQ-CO7] already leans
// on for the later-regression case.
//
// # Two refusals about the destination FILE, not the pack
//
// A `pack.jsonc` is refused because promotion rewrites the manifest through a JSON encoder
// and would silently drop every comment in it. A fetched pack is refused because it is not
// the user's file to edit (§5.1). Both name what they found.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// The destination names `--to` accepts. `pack:<name>` is the third, matched by prefix.
const (
	promoteDestLocal      = "local"
	promoteDestHost       = "host"
	promoteDestPackPrefix = "pack:"
)

// promoteDest is a resolved destination: which pack it is, and which file would be written.
type promoteDest struct {
	// host marks `--to host`: the keys themselves, into the surface's own real-home file.
	// It has no pack and no single path — the destination is per surface — and it is the
	// one destination whose WRITE this step does not build (refusePromoteHostWrite).
	host bool
	// pack is the pack NAME the keys would be declared in — the same name the fold order is
	// keyed on, which is what lets the precedence check ask where this destination sits.
	pack string
	// dir is the pack directory; path is the manifest inside it.
	dir  string
	path string
	// implicit marks the conventional local pack when it does not exist yet: this
	// promotion creates it, which is a fact the report states rather than doing silently.
	implicit bool
}

// label is how the destination is named in output: "local", "pack:<name>" or "host".
func (d promoteDest) label() string {
	switch {
	case d.host:
		return promoteDestHost
	case d.pack == config.LocalPackName:
		return promoteDestLocal
	}
	return promoteDestPackPrefix + d.pack
}

// resolvePromoteDest turns a `--to` value into a writable destination, or refuses.
//
// rc != 0 is a REFUSAL the caller returns, and every one of them names both what was found
// and why it stops here. Resolution happens before the plan is built, including under
// --plan: a classification against a destination that can never be written would be a plan
// for something that cannot happen.
func resolvePromoteDest(to string, errw io.Writer) (promoteDest, int) {
	switch {
	case to == promoteDestHost:
		return promoteDest{host: true}, refuseHostPromoteContract(errw)
	case to == promoteDestLocal:
		dir := paths.LocalPackDir()
		d := promoteDest{pack: config.LocalPackName, dir: dir, path: filepath.Join(dir, "pack.json")}
		if _, err := os.Stat(dir); err != nil {
			d.implicit = true
		}
		return d, refusePromoteManifestForm(d, errw)
	case strings.HasPrefix(to, promoteDestPackPrefix):
		return resolvePromotePack(strings.TrimPrefix(to, promoteDestPackPrefix), errw)
	case to == "workspace":
		// [OQ-CO8]: out of scope as a DECISION, not a wait — and unbuildable besides (the
		// workspace layer has no config key and no producer). Named rather than swept into
		// "unknown destination", because a user who read the design will type it.
		fmt.Fprintf(errw, "yolo config promote: `--to workspace` is out of scope by ruling "+
			"[OQ-CO8] — the workspace layer has no config key and nothing sets it, so there "+
			"is no file to write. Use `--to local` (every jail and the host) or "+
			"`--to pack:<name>`.\n")
		return promoteDest{}, 1
	default:
		fmt.Fprintf(errw, "yolo config promote: unknown destination %q (want %s, %s<name>, or %s)\n",
			to, promoteDestLocal, promoteDestPackPrefix, promoteDestHost)
		return promoteDest{}, 2
	}
}

// refuseHostPromoteContract is the `--to host` CONTRACT check, and it is the only part of
// that destination decided without looking at a surface.
//
// Under `host_management: none` or `own` the destination is wrong in principle, and for
// opposite reasons: at `none` yolo writes nothing into the real home at all, and at `own`
// the file is DERIVED output, so writing a key into it is writing into a render the next
// apply composes over — promote to the pack that derives it (§5.1).
//
// What is NOT decided here is the per-surface half: whether the surface even HAS a host
// layer. That is a fact about one surface, so it is classified per surface
// (promotionNoHostLayer) and appears in the plan beside every other reason a key stays put,
// rather than aborting a run that may name several surfaces.
func refuseHostPromoteContract(errw io.Writer) int {
	switch config.HostManagementMode() {
	case config.HostManagementNone:
		fmt.Fprintf(errw, "yolo config promote: `--to host` writes into your real home, and "+
			"`host_management` is \"none\" in %s — your config files are yours entirely, so "+
			"yolo will not write one. Promote to a pack instead: `--to local`.\n",
			paths.UserConfigPath())
		return 1
	case config.HostManagementOwn:
		fmt.Fprintf(errw, "yolo config promote: `--to host` is refused under `host_management: "+
			"\"own\"` in %s — that value declares the file DERIVED output, so a key written "+
			"into it is a key written into a render and the next apply composes over it. "+
			"Promote to a pack, which is what derives it: `--to local`.\n",
			paths.UserConfigPath())
		return 1
	}
	return 0
}

// refusePromoteHostWrite refuses the `--to host` WRITE, which this step does not build.
//
// It refuses at the WRITE rather than at resolution so the plan still runs: `--to host
// --plan` is how a user finds out that, of the surfaces they have captures on, almost none
// has a host layer to promote into — the §5.1 fact that makes `--to host` a narrow
// convenience rather than a general destination.
//
// Not a quiet fallback to `local`: they are different files with different reach, and §5.1's
// own warning is that promote's UI must not offer `host` as though it were the same kind of
// thing.
func refusePromoteHostWrite(errw io.Writer) int {
	fmt.Fprintf(errw, "yolo config promote: `--to host` is not built — this step ships the pack "+
		"destinations (`--to local`, `--to pack:<name>`). The plan above is accurate; only "+
		"the write is missing.\n"+
		"  `--to local` is the better answer for almost every key anyway: it reaches every "+
		"jail AND the host, folds three slots above the `host` layer, and needs no surface "+
		"to declare `readsHost` (docs/design/config-ownership-and-promotion.md §5.1).\n")
	return 1
}

// resolvePromotePack resolves `--to pack:<name>` against the configured packs.
func resolvePromotePack(name string, errw io.Writer) (promoteDest, int) {
	if name == "" {
		fmt.Fprintf(errw, "yolo config promote: `--to %s` needs a pack name\n", promoteDestPackPrefix)
		return promoteDest{}, 2
	}
	entries, err := config.LoadPacks(nil)
	if err != nil {
		fmt.Fprintf(errw, "yolo config promote: reading the configured packs: %v\n", err)
		return promoteDest{}, 1
	}
	for _, e := range entries {
		if e.Name != name {
			continue
		}
		switch {
		case e.Embedded():
			// An embedded pack lives inside the yolo binary; the materialized tree is a temp
			// dir released on the way out (packload.Embedded's contract), so a write there
			// would vanish and would be yolo's file besides.
			fmt.Fprintf(errw, "yolo config promote: `%s` is a pack yolo SHIPS — its manifest "+
				"is inside the binary, not a file on disk. Promote to `--to local`, whose "+
				"config-overlay folds after every shipped pack.\n", name)
			return promoteDest{}, 1
		case !e.IsLocal():
			// §5.1: refused for a fetched pack — not the user's file to edit. A write would
			// also be undone by the next `yolo pack install`, which re-materializes the
			// store from the locked commit.
			fmt.Fprintf(errw, "yolo config promote: `%s` is a FETCHED pack (%s) — it is not "+
				"your file to edit, and the next `yolo pack install` would overwrite the "+
				"change from the locked commit. Promote to `--to local`, or edit that pack "+
				"at its source and re-install.\n", name, e.Source)
			return promoteDest{}, 1
		}
		dir := strings.TrimPrefix(e.Source, "file://")
		d := promoteDest{pack: name, dir: dir, path: filepath.Join(dir, "pack.json")}
		return d, refusePromoteManifestForm(d, errw)
	}
	// A pack nothing selects renders nothing, so a key promoted there would be declared and
	// still not reach any file — the silent non-delivery this verb exists to end.
	fmt.Fprintf(errw, "yolo config promote: no configured pack named %q — a key declared in a "+
		"pack that is not in `packs` would render nowhere. `yolo pack ls` lists them.\n", name)
	return promoteDest{}, 1
}

// refusePromoteManifestForm refuses a destination whose manifest is a `pack.jsonc`.
//
// packload reads pack.jsonc in preference to pack.json, so writing the JSON form beside it
// would produce a manifest yolo IGNORES — a promotion that reports success and delivers
// nothing. Rewriting the .jsonc itself is worse: the round trip goes through a JSON encoder
// and would silently delete every comment the user wrote, in a file they hand-maintain.
func refusePromoteManifestForm(d promoteDest, errw io.Writer) int {
	jsonc := filepath.Join(d.dir, "pack.jsonc")
	if _, err := os.Stat(jsonc); err != nil {
		return 0
	}
	fmt.Fprintf(errw, "yolo config promote: %s is a `pack.jsonc`, and promote writes JSON — "+
		"rewriting it would strip the comments in it, and writing a `pack.json` beside it "+
		"would be ignored (packload reads the .jsonc first).\n"+
		"  Add the contribution by hand, or rename the manifest to pack.json first.\n", jsonc)
	return 1
}

// applyPromotion is §5.2 steps 5-6: declare the promotable keys at the destination and
// reset exactly those keys out of the capture overlay.
//
// WITHOUT promoteFlagAccept IT WRITES NOTHING and says what it would have written. The
// report above has already printed the plan, so this is the "dry run IS the confirmation"
// shape hostrevert.go takes, for the same reason: the user has seen the exact keys, and a
// [y/N] on top of that asks the same question twice. The flag NAMES what is approved
// ([OQ-CO4]), so it cannot be the generic --yes someone pastes.
//
// # ONE WINDOW, stated because it is a consequence of the reset being sidecar-only
//
// last_render is deliberately left alone (agentcfg.DeleteOverlayKeys says why), so until
// the next BOOT it still predates the promotion — and a capture taken in between (a jail
// still running, or its capture-on-terminate fold) will diff the unchanged surface file
// against that older baseline and record the promoted key again. The VALUE is identical, so
// nothing renders differently and nothing is lost; the entry is simply a redundant capture,
// which the next `promote` names as redundant and `config reset` clears. Moving last_render
// to close the window would be the far worse trade: it is the branch selector, so writing it
// here turns the next boot into a first migration, which ADOPTS the on-disk file — putting
// the key back in the overlay for real, not merely redundantly.
func applyPromotion(plan promotePlan, o promoteOptions, pr richtext.Printer, errw io.Writer) int {
	if plan.Dest.host {
		// Before the no-op check: someone who typed `--to host` is owed the fact that the
		// write does not exist, whether or not this particular run found a movable key.
		return refusePromoteHostWrite(errw)
	}
	moves := promotableCount(plan)
	if moves == 0 {
		// §5.6's no-ops: no captures, or every captured key redundant. Exit 0 — nothing is
		// wrong, and the report above already named what was found.
		pr.Printf("[dim]Nothing to promote for %s%s.[/dim]", plan.Agent, surfaceSuffix(o.surface))
		return 0
	}
	if !o.accept {
		pr.Printf("[bold]%d %s would be declared in %s[/bold] [dim](%s)[/dim]",
			moves, plural(moves, "key", "keys"), plan.Dest.label(), plan.Dest.path)
		pr.Printf("Nothing was written. Re-run with [cyan]%s[/cyan] to write them and clear "+
			"them from the capture overlay.", promoteFlagAccept)
		return 0
	}
	set, err := buildPromoteWrite(plan)
	if err != nil {
		fmt.Fprintf(errw, "yolo config promote: %v\n", err)
		return 1
	}
	if err := set.apply(); err != nil {
		fmt.Fprintf(errw, "yolo config promote: %v\n", err)
		return 1
	}
	for _, ps := range plan.Surfaces {
		var moved []string
		for _, k := range ps.Keys {
			if k.promotable() {
				moved = append(moved, k.Key)
			}
		}
		if len(moved) == 0 {
			continue
		}
		pr.Printf("Promoted [cyan]%s/%s[/cyan] → %s: %s",
			ps.Surface.Agent, ps.Surface.Name, plan.Dest.label(), strings.Join(moved, ", "))
	}
	if plan.Dest.implicit {
		pr.Printf("[dim]Created %s — the conventional local pack, which needs no `packs` "+
			"entry and folds after every other pack.[/dim]", plan.Dest.dir)
	}
	pr.Printf("[dim]Declared in %s. The next launch renders these keys from there; the "+
		"capture overlay no longer holds them.[/dim]", plan.Dest.path)
	return 0
}

// promotableCount is how many keys the plan would actually move.
func promotableCount(plan promotePlan) int {
	n := 0
	for _, ps := range plan.Surfaces {
		for _, k := range ps.Keys {
			if k.promotable() {
				n++
			}
		}
	}
	return n
}

// promoteWrite is the whole write as data: every file to change, with the bytes to restore
// it. Built completely before anything is written, so a failure at any point has a
// pre-image for every file already touched.
type promoteWrite struct {
	files []promoteFileWrite
}

// promoteFileWrite is one file's new content plus what undoes it. preImage == nil means the
// file did not exist, and the rollback is to remove it.
type promoteFileWrite struct {
	path     string
	data     []byte
	preImage []byte
	existed  bool
	// mkdir is the directory to create first (the conventional local pack's, on the run
	// that creates it).
	mkdir string
}

// buildPromoteWrite computes the destination manifest and the reset sidecars.
func buildPromoteWrite(plan promotePlan) (promoteWrite, error) {
	var w promoteWrite
	manifestPre, existed, err := readIfPresent(plan.Dest.path)
	if err != nil {
		return w, fmt.Errorf("reading %s: %w", plan.Dest.path, err)
	}
	decl, err := decodePackManifest(manifestPre, existed, plan.Dest.pack)
	if err != nil {
		return w, err
	}
	for _, ps := range plan.Surfaces {
		keys := map[string]any{}
		var names []string
		for _, k := range ps.Keys {
			if !k.promotable() {
				continue
			}
			keys[k.Key] = k.value
			names = append(names, k.Key)
		}
		if len(names) == 0 {
			continue
		}
		if err := declareOverlayKeys(decl, ps.Surface.Agent+"/"+ps.Surface.Name, keys); err != nil {
			return w, err
		}
		updated, pre, _, derr := agentcfg.DeleteOverlayKeys(ps.OverlayJSON, names)
		if derr != nil {
			return w, derr
		}
		w.files = append(w.files, promoteFileWrite{
			path:     prismOverlayPath(ps.Surface.Agent, ps.Surface.Name),
			data:     append(updated, '\n'),
			preImage: pre,
			existed:  true,
		})
	}
	body, err := jsonx.DumpsIndent(decl, 2)
	if err != nil {
		return w, fmt.Errorf("encoding %s: %w", plan.Dest.path, err)
	}
	// The MANIFEST FIRST, so a failure resetting an overlay leaves the declaration to roll
	// back rather than a captured key already gone with nowhere to put it back.
	w.files = append([]promoteFileWrite{{
		path: plan.Dest.path, data: []byte(body + "\n"),
		preImage: manifestPre, existed: existed, mkdir: plan.Dest.dir,
	}}, w.files...)
	return w, nil
}

// apply writes every file, rolling ALL of them back if any write fails.
//
// The rollback is best-effort by necessity — if the filesystem refused one write it may
// refuse another — so a failed rollback is reported as loudly as the original failure. What
// it must never do is stay silent, which is the state where a key is declared and captured
// at once.
func (w promoteWrite) apply() error {
	for i, f := range w.files {
		if f.mkdir != "" {
			if err := os.MkdirAll(f.mkdir, 0o755); err != nil {
				return fmt.Errorf("creating %s: %w", f.mkdir, err)
			}
		}
		if err := promoteWriteFile(f.path, f.data); err != nil {
			rollback := w.rollback(i)
			if rollback != nil {
				return fmt.Errorf("writing %s: %w — AND THE ROLLBACK FAILED: %v. The "+
					"promotion is half-applied; `yolo config diff` shows what the overlay "+
					"still holds", f.path, err, rollback)
			}
			return fmt.Errorf("writing %s: %w — the promotion was abandoned and every file "+
				"restored", f.path, err)
		}
	}
	return nil
}

// rollback restores the first n files (those already written) to their pre-images.
func (w promoteWrite) rollback(n int) error {
	var failed error
	for i := 0; i < n; i++ {
		f := w.files[i]
		var err error
		if f.existed {
			err = promoteWriteFile(f.path, f.preImage)
		} else {
			err = os.Remove(f.path)
		}
		if err != nil && failed == nil {
			failed = err
		}
	}
	return failed
}

// promoteWriteFile writes one file atomically: a temp file in the destination directory,
// then a rename over the target, so a crash mid-write cannot leave a truncated pack
// manifest or a truncated capture overlay.
//
// A package var so a test can make one named path fail, which is the only way to exercise
// the rollback: the writes are ordinary files in a temp home, and the test suite runs as
// root in this jail, where permissions refuse nothing.
var promoteWriteFile = func(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".yolo-promote-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// readIfPresent reads a file, reporting whether it existed. An absent file is not an error
// — it is the §5.6 case where the destination has no manifest and one is created.
func readIfPresent(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// decodePackManifest decodes the destination manifest, or scaffolds one (§5.6: "destination
// pack has no pack.json — one is created with the pack's name").
//
// Decoded with jsonx rather than encoding/json so the user's own key ORDER survives the
// round trip and an integer stays an integer. A manifest that does not decode is an error:
// promote must not overwrite a file it could not read.
func decodePackManifest(data []byte, existed bool, name string) (*jsonx.OrderedMap, error) {
	if !existed {
		m := jsonx.NewOrderedMap()
		m.Set("name", name)
		m.Set("contributes", []any{})
		return m, nil
	}
	v, err := jsonx.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("the destination manifest does not parse as JSON (%w) — promote "+
			"will not overwrite a manifest it cannot read", err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return nil, fmt.Errorf("the destination manifest is not a JSON object")
	}
	if _, present := m.Get("name"); !present {
		m.Set("name", name)
	}
	return m, nil
}

// declareOverlayKeys merges the promoted keys into the manifest's config-overlay for one
// surface, appending the contribution when there is none.
//
// IT TARGETS THE UNPROFILED OVERLAY, and never a `profile`-gated one: a gated contribution
// applies only while that profile is active for the agent, so merging a promoted key into
// it would deliver the key sometimes — the silent non-delivery a promotion must not produce.
// A pack whose only overlay on this surface is gated therefore gets a second, ungated
// contribution beside it.
func declareOverlayKeys(decl *jsonx.OrderedMap, surface string, keys map[string]any) error {
	list, present := decl.Get("contributes")
	items, isList := list.([]any)
	if present && list != nil && !isList {
		// Replacing it would silently delete whatever the user has there. promote does not
		// repair a manifest it cannot read, for the same reason decodePackManifest refuses
		// one that will not parse.
		return fmt.Errorf("the destination manifest's `contributes` is not a list — fix it by "+
			"hand rather than having promote overwrite it (%s)", surface)
	}
	for _, item := range items {
		c, ok := item.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		if kind, _ := c.Get("kind"); kind != "config-overlay" {
			continue
		}
		if target, _ := c.Get("surface"); target != surface {
			continue
		}
		if profile, present := c.Get("profile"); present && profile != "" {
			continue
		}
		body, _ := c.Get("config")
		cfg, ok := body.(*jsonx.OrderedMap)
		if !ok {
			return fmt.Errorf("the existing config-overlay for %s has no object `config` body "+
				"— fix it by hand rather than having promote rewrite it", surface)
		}
		managed, _ := cfg.Get("managed")
		mm, ok := managed.(*jsonx.OrderedMap)
		if !ok {
			mm = jsonx.NewOrderedMap()
		}
		for _, k := range sortedMapKeys(keys) {
			existing, present := mm.Get(k)
			if present {
				mm.Set(k, promoteMergeValue(existing, keys[k]))
				continue
			}
			mm.Set(k, keys[k])
		}
		cfg.Set("managed", mm)
		c.Set("config", cfg)
		return nil
	}
	managed := jsonx.NewOrderedMap()
	for _, k := range sortedMapKeys(keys) {
		managed.Set(k, keys[k])
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("managed", managed)
	c := jsonx.NewOrderedMap()
	c.Set("kind", "config-overlay")
	c.Set("surface", surface)
	c.Set("config", cfg)
	decl.Set("contributes", append(items, c))
	return nil
}

// promoteMergeValue deep-merges a promoted value over what the destination already
// declares, the promoted side winning (§5.6: "deep-merged; the promoted value wins").
//
// DEEP, not replace, and the capture overlay's own shape is why: a capture is a merge
// PATCH, so the value for an object key holds only the leaves that changed. Replacing would
// drop every sibling the destination already declared — the pack's own `permissions.ask`
// under a promoted `permissions.defaultMode`.
func promoteMergeValue(dst, src any) any {
	dstMap, dstOK := dst.(*jsonx.OrderedMap)
	srcMap, srcOK := src.(*jsonx.OrderedMap)
	if !dstOK || !srcOK {
		return src
	}
	out := jsonx.NewOrderedMap()
	for _, k := range dstMap.Keys() {
		v, _ := dstMap.Get(k)
		out.Set(k, v)
	}
	for _, k := range srcMap.Keys() {
		v, _ := srcMap.Get(k)
		if existing, present := out.Get(k); present {
			out.Set(k, promoteMergeValue(existing, v))
			continue
		}
		out.Set(k, v)
	}
	return out
}
