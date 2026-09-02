package entrypoint

// selectionapply_test.go pins the boot-path half of the selection mechanism: that the
// derive's reserved `selection` namespace is REACHED by the stateful render and decided
// per key, through the same entries the boot path uses — ConfigurePackSurfaces over the
// real embedded codex pack, and renderDeclaredSurface for the mode-specific halves.
//
// agentcfg/selection_test.go owns the decision table; this file owns the wiring. The
// distinction is the one AGENTS.md keeps drawing: a test that exercised ApplySelection
// directly would stay green if the boot stopped lifting, so these drive the render and
// read the file the agent reads.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// twoReachableProvidersJSON is two providers codex can both speak (the base_url
// shorthand, which is codex's own responses default), so a selection can CHANGE from
// one to the other — the case that separates "yolo re-asserts its own write" from
// "yolo reverts the user".
const twoReachableProvidersJSON = `{"llamacpp":{
  "base_url":"http://127.0.0.1:8080/v1","models":{"default":"llama"}},
 "vllm":{"base_url":"http://127.0.0.1:8000/v1","models":{"default":"qwen"}}}`

// selectionRender drives the boot render of the real codex pack repeatedly over ONE
// home and workspace, so each render reads the sidecars the previous one wrote. The
// selection mechanism is a state machine across boots; a harness that started fresh
// every time could only ever test its first step.
type selectionRender struct {
	e    *Env
	errw *bytes.Buffer
	path string
}

func newSelectionRender(t *testing.T, providersJSON string) *selectionRender {
	t.Helper()
	r := &selectionRender{errw: &bytes.Buffer{}}
	r.e = &Env{Home: t.TempDir(), Workspace: t.TempDir(),
		Vars: map[string]string{"YOLO_PROVIDERS": providersJSON}, Stderr: r.errw}
	withCtxRoot(t, t.TempDir(), "codex")
	r.path = filepath.Join(r.e.Home, ".codex", "config.toml")
	return r
}

// render runs one boot with the given profile table and returns the config.toml the
// agent would read. A boot failure is fatal: every later step of a sequence would be
// measuring a render that never happened.
//
// The resolved table is lowered the way the launcher lowers it (ResolveProfiles over the
// DECLARED set, whatever this boot activates): the names selected here are the user's own
// provider names, so a real launch refuses them unless the user declares them (OQ-CS6),
// and the selection a derive sees is read off that table — not off the pack manifests,
// which hold neither name.
func (r *selectionRender) render(t *testing.T, profilesJSON string) map[string]any {
	t.Helper()
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatalf("embedded codex: %v", err)
	}
	zai, err := embeddedPack("zai")
	if err != nil {
		t.Fatalf("embedded zai: %v", err)
	}
	r.e.Vars["YOLO_USE_PROFILES"] = profilesJSON
	// The composed provider table a launch would resolve against — composed first, the
	// order a real launch has, because the resolution reads its declared-options census
	// off it. Neither llamacpp nor vllm is in it (no pack here ships them), so neither
	// provider imposes a census, which is what the user-only declarations above rely on.
	providers, err := packload.ComposeProviders(nil, []*packload.Pack{codex, zai})
	if err != nil {
		t.Fatalf("composing the provider table: %v", err)
	}
	resolved, err := packload.ResolveProfiles(nil, map[string]packload.UserProfile{
		"llamacpp": {Provider: "llamacpp"},
		"vllm":     {Provider: "vllm"},
	}, providers)
	if err != nil {
		t.Fatalf("resolving the declared profiles: %v", err)
	}
	r.e.Vars["YOLO_PROFILES"], err = jsonx.DumpsCompact(packload.ProfilesWireTable(resolved))
	if err != nil {
		t.Fatalf("encoding the resolved table: %v", err)
	}
	ConfigurePackSurfaces(r.e, []*packload.Pack{codex, zai})
	if fails := r.e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, r.errw.String())
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatalf("read %s: %v", r.path, err)
	}
	m, err := tomlx.Decode(raw)
	if err != nil {
		t.Fatalf("decode %s: %v\n---\n%s", r.path, err, raw)
	}
	return m
}

// edit rewrites the surface file with one top-level string key set — the shape of an
// interactive change, which leaves the rest of the file alone. It goes through the
// codec so the hand edit is a file the agent could have written, not a corruption.
func (r *selectionRender) edit(t *testing.T, key, value string) {
	t.Helper()
	m := r.read(t)
	m[key] = value
	r.write(t, m)
}

func (r *selectionRender) read(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatalf("read %s: %v", r.path, err)
	}
	m, err := tomlx.Decode(raw)
	if err != nil {
		t.Fatalf("decode %s: %v", r.path, err)
	}
	return m
}

func (r *selectionRender) write(t *testing.T, m map[string]any) {
	t.Helper()
	out, err := (codec.TOML{}).Encode(m)
	if err != nil {
		t.Fatalf("encode the hand edit: %v", err)
	}
	if err := os.WriteFile(r.path, out, 0o644); err != nil {
		t.Fatalf("write the hand edit: %v", err)
	}
}

// selectionKey is the namespace as the agent's file would spell it. It must never
// appear: the namespace is an implementation detail of the computed layer, and a file
// carrying a literal `selection` table is a config the agent does not read.
const selectionKey = "selection"

// requireSelection asserts the two lifted keys at the surface root — where codex reads
// them — and that the namespace itself never leaked into the file.
func requireSelection(t *testing.T, got map[string]any, provider, model string) {
	t.Helper()
	if absentOr(got["model_provider"]) != provider {
		t.Errorf("model_provider = %v, want %q", got["model_provider"], provider)
	}
	if absentOr(got["model"]) != model {
		t.Errorf("model = %v, want %q", got["model"], model)
	}
	if _, leaked := got[selectionKey]; leaked {
		t.Errorf("config.toml carries a literal %q table — the reserved namespace reached the "+
			"file, which is an implementation detail of the layer, never of the file", selectionKey)
	}
}

// TestSelectionAppliesThroughTheBootRender walks the §5.1 state machine end to end, in
// order, over one home: activate, user edit, selection change, deactivate. The steps
// SHARE the harness deliberately — each one is a boot on top of the previous boot's
// sidecars, which is the only state the mechanism has.
func TestSelectionAppliesThroughTheBootRender(t *testing.T) {
	r := newSelectionRender(t, twoReachableProvidersJSON)

	// (a) Activation: a profile names a provider, and the file that did not have the
	// keys gets them.
	t.Run("activation writes the selection", func(t *testing.T) {
		requireSelection(t, r.render(t, `{"codex":"llamacpp"}`), "llamacpp", "llama")
	})

	// (b) The user changed the model interactively mid-session. The next boot of the
	// SAME selection must not revert them — the hazard OQ-CS2 refuses, and the reason a
	// selection key is not an ordinary computed key.
	t.Run("a user edit survives the same selection", func(t *testing.T) {
		r.edit(t, "model_provider", "mine")
		requireSelection(t, r.render(t, `{"codex":"llamacpp"}`), "mine", "llama")
	})

	// (c) A NEW selection is explicit intent, and it outranks the stale interactive
	// choice: the profile moved, and the file follows it.
	t.Run("a changed selection outranks the stale user edit", func(t *testing.T) {
		requireSelection(t, r.render(t, `{"codex":"vllm"}`), "vllm", "qwen")
	})

	// (d) Deactivation writes NOTHING — not a clear, not a default. The keys stay at
	// the values the last active selection left, which is what makes yolo able to turn
	// a selection on and unable to turn it off (§5.1).
	t.Run("deactivation clears nothing", func(t *testing.T) {
		requireSelection(t, r.render(t, ``), "vllm", "qwen")
	})

	// (e) Every NON-selection computed key still re-asserts. The selection mechanism
	// must not buy its gentleness by making the rest of the layer gentle: the catalog
	// is yolo's own output, a hand edit to it is captured and then outranked, and the
	// next boot puts the declared value back.
	t.Run("a non-selection computed key still re-asserts", func(t *testing.T) {
		m := r.read(t)
		provs, ok := m["model_providers"].(map[string]any)
		if !ok {
			t.Fatalf("model_providers missing from the render: %v", m)
		}
		llama, ok := provs["llamacpp"].(map[string]any)
		if !ok {
			t.Fatalf("model_providers.llamacpp missing: %v", provs)
		}
		llama["base_url"] = "http://127.0.0.1:9/v1"
		r.write(t, m)

		got := r.render(t, ``)
		provs, ok = got["model_providers"].(map[string]any)
		if !ok {
			t.Fatalf("model_providers missing after the re-render: %v", got)
		}
		llama, ok = provs["llamacpp"].(map[string]any)
		if !ok {
			t.Fatalf("model_providers.llamacpp missing after the re-render: %v", provs)
		}
		if llama["base_url"] != "http://127.0.0.1:8080/v1" {
			t.Errorf("model_providers.llamacpp.base_url = %v, want the declared value back — "+
				"a non-selection computed key must still re-assert", llama["base_url"])
		}
		requireSelection(t, got, "vllm", "qwen")
	})
}

// TestSelectionWritesNoRecordWhenNothingIsSelected pins the quiet half: a surface whose
// derive emits no selection leaves no selection record at all, so the sidecar tree grows
// only where the mechanism is used — and a no-profile launch on a fresh home writes
// nothing selection-shaped (OQ-CS2), which is only provable beside the record it must
// also not create.
func TestSelectionWritesNoRecordWhenNothingIsSelected(t *testing.T) {
	r := newSelectionRender(t, twoReachableProvidersJSON)
	got := r.render(t, ``)
	if _, present := got["model_provider"]; present {
		t.Errorf("model_provider = %v, want absent — the no-profile case is the agent's own "+
			"(provider-catalog-and-selection.md §5.1 OQ-CS2)", got["model_provider"])
	}
	if _, present := got["model"]; present {
		t.Errorf("model = %v, want absent", got["model"])
	}
	if _, err := os.Stat(prismSelectionRecordPath(r.e, "codex", "config")); !os.IsNotExist(err) {
		t.Errorf("a surface with no selection wrote a selection record: %v", err)
	}
}

// TestSelectionRecordTracksWhatYoloWrote pins the record's content at the seam the next
// boot reads it from: after an activation it holds the selected values, keyed by CONFIG
// key — the same keys the file carries, not the namespace's.
func TestSelectionRecordTracksWhatYoloWrote(t *testing.T) {
	r := newSelectionRender(t, twoReachableProvidersJSON)
	requireSelection(t, r.render(t, `{"codex":"llamacpp"}`), "llamacpp", "llama")

	record := readSelectionRecord(r.e, "codex", "config")
	if absentOr(record["model_provider"]) != "llamacpp" || absentOr(record["model"]) != "llama" {
		t.Errorf("selection record = %v, want the values yolo wrote", record)
	}
}

// selectionDerive is a derive that emits the reserved namespace beside an ordinary
// computed key — the minimal shape whose fate differs by mode.
const selectionDerive = `yolo.derive("example", "cfg", function(ctx)
  return { selection = { model_provider = "llamacpp" }, note = "kept" }
end)`

// TestReservedSelectionByMode pins all three mode halves at the boot entry. The
// stateful half is the lift the codex sequence above measures through a real pack; the
// other two are the drop, whose alternative is a literal `selection` table written into
// an agent's file — overwritten wholesale on a computed surface, and on an rmw one
// regenerated there on every boot as a wholesale-managed one. The rmw row exists
// because the two drops sit in DIFFERENT branches of the mode switch: nothing keeps
// `computed` routing through the drop when someone edits the rmw branch, and the failure
// would then be an rmw surface growing a `selection` table no computed-mode test sees.
func TestReservedSelectionByMode(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		want   string // model_provider in the rendered file; "" asserts absence
		note   string // the derive's other scalar key, as far as the mode carries it
		warned bool
	}{
		{name: "stateful lifts it onto the surface root", mode: "", want: "llamacpp",
			note: "kept"},
		{name: "computed drops it and names the drop", mode: manifest.ModeComputed,
			want: "", note: "kept", warned: true},
		// rmw writes no scalar computed key at all — only object-valued ones regenerate as
		// managed tables — and this fixture's file starts empty, so the derive's `note` has
		// nowhere to land. Which is the point of the row: the scalar keys were never the
		// hazard; the OBJECT the namespace is would have been, and it must not appear.
		{name: "rmw drops it too and names the drop", mode: manifest.ModeRMW,
			want: "", warned: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := modeEnv(t)
			errw := &bytes.Buffer{}
			e.Stderr = errw
			s := manifest.Surface{Agent: "example", Name: "cfg", Path: "~/.example/c.json",
				Codec: "json", Mode: tc.mode}
			if err := renderDeclaredSurface(e, s, nil, selectionDerive, surfaceSelection{}, nil); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(e.Home, ".example", "c.json"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := (codec.JSON{}).Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			m := got.(map[string]any)
			if absentOr(m["model_provider"]) != tc.want {
				t.Errorf("model_provider = %v, want %q", m["model_provider"], tc.want)
			}
			if absentOr(m["note"]) != tc.note {
				t.Errorf("note = %v, want %q — the drop must take the reserved namespace "+
					"alone", m["note"], tc.note)
			}
			if _, leaked := m[selectionKey]; leaked {
				t.Errorf("the file carries a literal %q table", selectionKey)
			}
			hasWarn := bytes.Contains(errw.Bytes(), []byte("only a stateful surface applies"))
			if hasWarn != tc.warned {
				t.Errorf("warning about the namespace = %v, want %v\nstderr: %s",
					hasWarn, tc.warned, errw.String())
			}
		})
	}
}
