package luahook

// Derive registrations and ctx: docs/reference/providers.md

// derive.go is the whole Lua slot: the PRODUCER a pack declares. A mutator
// (transform) half stood beside it in vm.go until
// the transform removal (docs/reference/pack-system.md#oq-lt1) deleted it; vm.go is now the VM and its
// sandbox and nothing else, so there is no other half to be distinct from.
//
// A `derive` runs PRE-merge: it receives the live config tables (mcp_servers,
// lsp_servers) and RETURNS a fresh object — the computed layer that feeds
// Inputs.Computed (packsurfaces.go). It is the one place a pack runs Lua:
// a sandboxed producer of a config value, never an effect
// (docs/reference/pack-system.md §7).
//
// The facilities the producer needs, all added here:
//
//   - the live tables, exposed read-only as ctx.mcp_servers / ctx.lsp_servers.
//     A derive is a pure function of these; it may not mutate them.
//   - a TOMBSTONE sentinel, ctx.tombstone. The computed layer uses Go nil as an
//     RFC-7386 "delete this key" marker (compose.go), and BuildComputed emitted
//     Go nil for a tombstone declaration, a false flag, and a dropped key. But
//     Lua tables CANNOT hold nil as a value — assigning nil deletes the key
//     (marshal.go). So `out.x = nil` in Lua would OMIT x, not tombstone it — a
//     silent behavior change from the DSL. ctx.tombstone is a unique sentinel
//     userdata that survives the round-trip and is decoded back to Go nil ONLY on
//     the derive path, reproducing the DSL's tombstone semantics exactly.
//   - the EMPTY-ARRAY sentinel, ctx.empty_array (see newDeriveSession).
//   - the IN-FULL sentinel, ctx.in_full(t), which wraps a table the derive
//     regenerates in full (CO13, docs/design/config-ownership-and-promotion.md
//     #co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided). It is the
//     one sentinel that carries a DECLARATION rather than a value: the table decodes to
//     the plain object it always was, and the key it sat under is reported beside the
//     layer (DeriveOutput.InFull) instead of inside it, so no reader of a computed layer
//     has anything new to strip.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// DeriveCtx is what a derive function receives: only the inputs the producer is a
// pure function of. It deliberately carries no composed config and no stage
// handle — a derive reads its sources and returns a value, and that is all.
type DeriveCtx struct {
	// Agent / Surface identify which registered derive fn to invoke, matching the
	// (agent, name) surface identity — a derive registers with BOTH, because one
	// agent has several surfaces (claude: config + settings) with different
	// derivations.
	Agent   string
	Surface string

	// Env selects the ENV producer instead of a surface derive: when true, Derive
	// invokes the yolo.env(agent, fn) registration — a THIRD registration with a key
	// space of its own, so a pack declaring a real surface named "env" can never
	// collide with the environment composition (the surface loop never sets this, and
	// never sees the env registration at all). The producer returns a flat table of
	// string values; ctx.tombstone in a value is a REMOVAL, decoded to Go nil here
	// exactly as on the surface path.
	Env bool

	// SelectedProvider is the provider the active profile resolved to at this agent's
	// CLI name — the key the env producer reads ctx.providers with. Exposed as
	// ctx.selected_provider; "" when no variant is active.
	SelectedProvider string

	// ProfileName is the variant active at this agent's CLI name, exposed as
	// ctx.profile_name.
	ProfileName string

	// Profile is the ACTIVE profile's resolved option map (the provider-declared defaults
	// with the profile's own values over them — docs/reference/providers.md),
	// exposed as ctx.profile. ALWAYS a table, empty when no profile is active, so a derive
	// can read ctx.profile.model without a nil guard and "no profile" is the same world to
	// it as "a profile with no options". What the options MEAN — which one is the
	// selection surface — is the derive's business (OQ-CS4); the empty map is what carries
	// OQ-CS2's ruling (§5.1) to it: no active profile means the derive writes nothing, so
	// the agent's own choice of model stays untouched.
	Profile map[string]string

	// ViaURL is the agent's per-agent route on the service its active profile's `via`
	// names (docs/design/wire-bridge-gateway.md OQ-WG7 (d)), exposed as ctx.via_url; ""
	// when the agent uses its own client. A derive that writes an OpenAI-protocol base URL
	// writes THIS instead of the provider's when it is set; the service forwards to the
	// provider's own endpoint. Per agent, never a composed-table fact: the provider table
	// is jail-wide and `via` is one agent's choice.
	ViaURL string

	// Tables are the live config tables a derive may read, keyed by source name
	// (manifest.SourceMCPServers / SourceLSPServers). Exposed read-only as
	// ctx.<name>. Absent source => an empty table (a jail with no MCP configured
	// and one with an empty table are the same world — matches BuildComputed).
	//
	// ctx.mcp_servers is the one table this layer FILTERS before exposing: capability-
	// driven MCP delivery drops every server the active authentication source already
	// performs the job of (buildDeriveCtxTable). What arrives here is the configured
	// table; what a derive sees is the ELIGIBLE one.
	Tables map[string]map[string]any

	// NativeCapabilities is the capability set of the agent's BUILT-IN authentication
	// source — the first-party login its CLI uses when SelectedProvider is "" — resolved
	// by the caller through packload.NativeCapabilities (bin ownership).
	//
	// It is the only half of the source resolution that needs a field: when a provider IS
	// selected, its capabilities are already in Tables["providers"] under the same
	// `capabilities` key a user's config declares, so the resolver reads what it was
	// handed rather than asking for the same fact twice.
	NativeCapabilities []string

	// UnknownAPI, when non-nil, makes an unknown `yolo.<name>` member TOLERATED
	// instead of fatal, and is the callback that reports each one (once per name per
	// Derive call, whatever a script does with it). Nil — the zero value — is STRICT:
	// reading an unknown member raises a Lua error that names it.
	//
	// The field carries the switch and the reporting channel together on purpose: a
	// tolerated skip that nobody hears is the one outcome the skew rules forbid
	// (packdecl.DecodeTolerant), so there is no way to ask for tolerance without also
	// saying where the note goes.
	//
	// WHY THIS EXISTS. The set of `yolo.*` functions is a VERSION BOUNDARY exactly like
	// the manifest vocabulary is, and it went un-noticed as one until it broke a jail.
	// The in-jail entrypoint executes a derive.lua the HOST staged, and the two halves
	// deploy on different cadences (AGENTS.md, "Build & deploy"), so a host newer than
	// the baked image stages a script calling an API that build has never registered.
	// Adding yolo.env (f55f2109) did precisely that: every jail on a pre-f55f2109 image
	// died at boot with
	//
	//	surface claude/config: derive: lua transform error:
	//	    <string>:51: attempt to call a non-function object
	//
	// — line 51 being packs/claude/derive.lua's `yolo.env("claude", …)`. The failure is
	// worse than it looks in two ways. The whole script is executed to REGISTER its
	// producers, so one unknown call at the top level takes down every surface the
	// script serves, not just the one it belongs to (both claude/config and
	// claude/settings failed, at lines 4 and 26, over a call at line 51). And in the
	// jail the yolo.env registration is INERT anyway — the entrypoint never invokes the
	// env producer (packload/deriveenv.go: host-side only) — so the boot died over an
	// API surface it must EXPOSE but does not USE. Tolerating the read costs the jail
	// nothing it was going to render.
	//
	// STRICT STAYS THE ZERO VALUE so tolerance is something a caller ASKS for at a
	// boundary it can name, the way packdecl.Decode stays strict beside DecodeTolerant.
	// Of the three production readers, one takes it: the host-side env composition
	// (packload.AgentEnv) refuses an unknown member, and both paths that render a
	// SURFACE — the jail's boot loop and `yolo check`'s dry run, which share
	// deriveComputedLayer — tolerate and report it.
	UnknownAPI func(name string)
}

// DeriveVM is the boundary for running a derive producer, mirroring LuaVM. The
// production impl is GopherLuaVM (it satisfies both).
type DeriveVM interface {
	// Derive runs script, invokes the derive fn registered for (ctx.Agent,
	// ctx.Surface), and returns the object it produced — the computed layer.
	// Returns (nil, nil) when the script registers no derive for this surface
	// (the identity: no computed layer). Any Lua error is a non-nil Go error.
	Derive(script string, ctx *DeriveCtx) (map[string]any, error)
}

// DeriveOutput is one producer's whole answer: the computed layer, and the declarations
// the derive made ABOUT that layer through the in-full sentinel.
//
// A struct beside the layer rather than a marker inside it, and that is the point of the
// CO13 ruling that chose a sentinel over a reserved key: a marker inside the layer is a
// new obligation on every reader of it (TakeSelection's call site, the host's table probe,
// `yolo config render`), and a reader that forgets writes a literal key into a user's
// file. The sentinel is stripped here, by the decoder that already strips the other two.
type DeriveOutput struct {
	// Layer is the computed layer, exactly what Derive returns. nil when the script
	// registers no producer for this surface.
	Layer map[string]any

	// InFull are the TOP-LEVEL keys of Layer whose value the derive wrapped in
	// ctx.in_full — the tables it declares it REGENERATES IN FULL, so that an entry under
	// one that it did not produce this run is its own stale output rather than anyone
	// else's. Sorted; nil when the derive declared none.
	//
	// A key a derive produced WITHOUT the wrapper is the other kind, a table it only
	// ASSERTS LEAVES of (claude's `env`): yolo owns the keys it names and nothing else
	// under it. That is the reading the two consumers of this field take for a table not
	// declared in full — the stateful adoption (agentcfg's dropComputedTables, through
	// Inputs.ComputedInFull) and the host's table probe (entrypoint.hostTableKeys) —
	// because of the two ways to guess wrong it is the one that costs correctness rather
	// than data: guessing "in full" wrongly deletes the agent's own entries, while guessing
	// "leaves" wrongly keeps an entry yolo itself wrote on an earlier run.
	//
	// ⚠ NOT every reader of the computed layer consults it. The jail's rmw arm
	// (entrypoint.regenerateManagedTables) still regenerates EVERY object-valued computed
	// key wholesale, declared or not — a residual awaiting a ruling, unobservable for the
	// shipped packs (docs/design/config-ownership-and-promotion.md, "Built 2026-09-25 —
	// what shipped").
	InFull []string
}

// tombstoneName / emptyArrayName are the globals under which the two VALUE
// sentinels are exposed (as ctx.tombstone / ctx.empty_array), recognized by
// identity when marshalling the derive's return back to Go.
//
// The third sentinel, ctx.in_full, has no global: it is a FUNCTION returning a fresh
// userdata per call (inFullTable), recognized by the Go type it carries rather than by
// one identity, since each wrapped table is a different value.
const (
	tombstoneName  = "yolo_tombstone_sentinel"
	emptyArrayName = "yolo_empty_array_sentinel"
)

// inFullName is the ctx field the in-full sentinel is exposed under — named because the
// error messages spell it and a typo there would send a pack author looking for a
// function that does not exist.
const inFullName = "in_full"

// inFullTable is what ctx.in_full(t) returns: a userdata wrapping the table the derive
// declared it regenerates in full. Unexported, and only buildDeriveCtxTable constructs
// one, so a userdata carrying this type can only have come from the sentinel.
type inFullTable struct{ table *lua.LTable }

// deriveSession is ONE registration run of a derive script: the sandboxed VM, the ctx
// table it exposes, and the producer tables the script's `yolo.derive` / `yolo.env` calls
// filled — stopping short of invoking anything.
//
// It exists because two readers need that state and MUST NOT be able to disagree about
// what a registration is. Derive invokes one producer and marshals its return;
// DeriveRegistrations only lists what registered, which is how `yolo config ls` derives
// its `computed` column (docs/design/host-render-target.md §3.4). Sharing the setup is
// the whole point: the column and the boot render then answer "does this surface have a
// computed layer?" from one execution of one script, so the column cannot drift from the
// render the way the hand-maintained Go map it replaced had — that map was missing three
// of the surfaces the shipped packs register.
//
// The caller closes it.
type deriveSession struct {
	L        *lua.LState
	ctxTable *lua.LTable
	derives  map[deriveKey]*lua.LFunction
	envs     map[string]*lua.LFunction
	sentinel *lua.LUserData
	emptyArr *lua.LUserData
	cancel   context.CancelFunc
}

// deriveKey identifies one registered surface producer. Was a function-local type inside
// Derive; package-level now because the session carries the table across two readers.
type deriveKey struct{ agent, surface string }

// close releases the VM. cancel FIRST, then the state, which is the order the two
// deferred statements this replaced ran in.
func (s *deriveSession) close() {
	s.cancel()
	s.L.Close()
}

// newDeriveSession builds the sandbox identically to Run (openSandboxLibs), exposes the
// live tables plus the two sentinels as ctx, and RUNS the script — which is what performs
// the registrations. It invokes no producer.
func newDeriveSession(vm GopherLuaVM, script string, ctx *DeriveCtx) (*deriveSession, error) {
	if ctx == nil {
		return nil, fmt.Errorf("luahook: nil derive ctx")
	}

	L := lua.NewState(lua.Options{SkipOpenLibs: true})

	timeout := vm.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	goCtx, cancel := context.WithTimeout(context.Background(), timeout)
	L.SetContext(goCtx)

	s := &deriveSession{
		L:       L,
		cancel:  cancel,
		derives: map[deriveKey]*lua.LFunction{},
		envs:    map[string]*lua.LFunction{},
	}

	if err := openSandboxLibs(L); err != nil {
		s.close()
		return nil, err
	}

	// The tombstone sentinel: a unique userdata, exposed as ctx.tombstone and
	// recognized by identity when marshalling back to Go nil.
	s.sentinel = L.NewUserData()
	L.SetGlobal(tombstoneName, s.sentinel)

	// The empty-array sentinel. Lua cannot distinguish {} the array from {} the
	// object, and the marshaller decodes an empty table to an empty OBJECT
	// (map[string]any) — so a derive that wants an empty ARRAY (e.g. a defaulted
	// `args = []`, which must render as JSON `[]`, not `{}`) needs a distinct
	// marker. ctx.empty_array is that marker, decoded back to []any{}. A NON-empty
	// array is unambiguous (1..n integer keys) and needs no sentinel.
	s.emptyArr = L.NewUserData()
	L.SetGlobal(emptyArrayName, s.emptyArr)

	// yolo.derive(agent, surface, fn) records fn keyed by (agent, surface).
	yolo := L.NewTable()
	L.SetField(yolo, "derive", L.NewFunction(func(L *lua.LState) int {
		agent := L.CheckString(1)
		surface := L.CheckString(2)
		fn := L.CheckFunction(3)
		s.derives[deriveKey{agent, surface}] = fn
		return 0
	}))
	// yolo.env(agent, fn) records fn keyed by agent ALONE. A separate table, not a
	// surface name: the environment composition is not a surface (nothing renders it
	// to a file), so keying it by (agent, "env") would let a pack's REAL surface named
	// "env" collide with it. Distinct storage is what makes the collision
	// unrepresentable — see DeriveCtx.Env.
	L.SetField(yolo, "env", L.NewFunction(func(L *lua.LState) int {
		agent := L.CheckString(1)
		fn := L.CheckFunction(2)
		s.envs[agent] = fn
		return 0
	}))
	guardUnknownAPI(L, yolo, ctx.UnknownAPI)
	L.SetGlobal("yolo", yolo)

	ctxTable, err := buildDeriveCtxTable(L, ctx, s.sentinel, s.emptyArr)
	if err != nil {
		s.close()
		return nil, err
	}
	s.ctxTable = ctxTable
	L.SetGlobal("ctx", ctxTable)

	if err := L.DoString(script); err != nil {
		s.close()
		return nil, wrapLuaErr(err)
	}
	return s, nil
}

// Derive implements DeriveVM on the same gopher-lua VM as Run. It runs the registration
// script (newDeriveSession), invokes the derive fn registered for (ctx.Agent,
// ctx.Surface), and marshals the returned table back — converting the tombstone sentinel
// to Go nil.
//
// It is DeriveLayer without the declarations, for the callers that consume a layer's
// VALUES only (the env composition, previews). A caller that decides what an adopting or
// host render may claim as yolo's own output needs DeriveLayer's InFull.
func (vm GopherLuaVM) Derive(script string, ctx *DeriveCtx) (map[string]any, error) {
	out, err := vm.DeriveLayer(script, ctx)
	return out.Layer, err
}

// DeriveLayer is Derive plus the declarations the producer made about its layer
// (DeriveOutput.InFull). A zero DeriveOutput and nil error is the identity: no producer
// registered for this surface.
func (vm GopherLuaVM) DeriveLayer(script string, ctx *DeriveCtx) (DeriveOutput, error) {
	s, err := newDeriveSession(vm, script, ctx)
	if err != nil {
		return DeriveOutput{}, err
	}
	defer s.close()

	fn, ok := s.derives[deriveKey{ctx.Agent, ctx.Surface}]
	who := fmt.Sprintf("derive for %s/%s", ctx.Agent, ctx.Surface)
	if ctx.Env {
		fn, ok = s.envs[ctx.Agent]
		who = "env producer for " + ctx.Agent
	}
	if !ok {
		// No derive registered for this surface — the identity (no computed layer).
		// The env spelling lands here the same way: an agent whose pack registered no
		// yolo.env composes no environment.
		return DeriveOutput{}, nil
	}
	if err := s.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, s.ctxTable); err != nil {
		return DeriveOutput{}, wrapLuaErr(err)
	}
	ret := s.L.Get(-1)
	s.L.Pop(1)

	tbl, isTable := ret.(*lua.LTable)
	if !isTable {
		return DeriveOutput{}, fmt.Errorf("luahook: %s returned %s, want a table (the computed layer)",
			who, ret.Type())
	}
	out, inFull, err := deriveTableToGo(tbl, s.sentinel, s.emptyArr)
	if err != nil {
		return DeriveOutput{}, err
	}
	return DeriveOutput{Layer: out, InFull: inFull}, nil
}

// DeriveRegistration is one `yolo.derive(agent, surface, fn)` a script performed: which
// surface has a producer, without the producer itself.
//
// A luahook-local type rather than manifest.SurfaceKey on purpose: manifest and luahook
// are siblings neither of which imports the other, and a reporting type is not worth
// being the first edge between them.
// packload maps it to a SurfaceKey, which is where the two vocabularies already meet.
type DeriveRegistration struct {
	// Agent is the first argument of the yolo.derive call.
	Agent string
	// Surface is the second — the per-agent surface name, not a path.
	Surface string
}

// DeriveRegistrations reports which (agent, surface) pairs the script registers a
// producer for, sorted. It is "which surfaces have a computed layer?", answered by
// RUNNING the registrations rather than by a table maintained beside a reader.
//
// The producers are not invoked, so this is safe to call for a surface that does not
// exist and cheap enough for a reporting command: it costs one DoString of the script,
// which is the same work the registration half of Derive already does per surface.
//
// TOLERANT of an unknown `yolo.<name>`, for the reason DeriveCtx.UnknownAPI gives and
// then some: a listing that refused a script written for a newer yolo would report "no
// computed layer" for every surface that script serves — the silent-skip shape
// host-render-target.md §6.2 exists to prevent — and the caller here is a read-mostly
// reporting path with nothing to fail into.
func (vm GopherLuaVM) DeriveRegistrations(script string) ([]DeriveRegistration, error) {
	s, err := newDeriveSession(vm, script, &DeriveCtx{UnknownAPI: func(string) {}})
	if err != nil {
		return nil, err
	}
	defer s.close()

	out := make([]DeriveRegistration, 0, len(s.derives))
	for k := range s.derives {
		out = append(out, DeriveRegistration{Agent: k.agent, Surface: k.surface})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Agent != out[j].Agent {
			return out[i].Agent < out[j].Agent
		}
		return out[i].Surface < out[j].Surface
	})
	return out, nil
}

// guardUnknownAPI installs the `yolo` table's __index, which decides what reading a
// member this build never registered means. It is the ONLY thing standing between a
// pack script written for a newer yolo and gopher-lua's "attempt to call a non-function
// object" — see DeriveCtx.UnknownAPI for the boot that error cost.
//
// __index fires only for MISSING keys: every registered API is a raw field on the table,
// so the guard cannot shadow, intercept or slow down a call that resolves normally.
//
// report == nil is STRICT: raise, naming the member and everything this build does have.
// The list is READ OFF THE TABLE rather than written out here, so an API added tomorrow
// appears in the message without anyone remembering to add it — a hardcoded list would be
// wrong in exactly the situation this message is read in.
//
// report != nil is TOLERANT: the read yields a stub that accepts any arguments and does
// nothing, which is the right answer for a REGISTRATION (the producer is simply never
// registered, and an unregistered producer is already the identity — see Derive's !ok
// branch). Each name is reported at most once however many times the script touches it;
// a script calling an unknown API in a loop is one finding, not a thousand lines.
func guardUnknownAPI(L *lua.LState, yolo *lua.LTable, report func(name string)) {
	var known []string
	yolo.ForEach(func(k, _ lua.LValue) {
		if ks, ok := k.(lua.LString); ok {
			known = append(known, "yolo."+string(ks))
		}
	})
	sort.Strings(known)

	seen := map[string]bool{}
	mt := L.NewTable()
	L.SetField(mt, "__index", L.NewFunction(func(L *lua.LState) int {
		name := L.Get(2).String()
		if report == nil {
			L.RaiseError("yolo.%s is not an API this build of yolo provides (it has: %s) — "+
				"either this derive.lua was written for a newer yolo than the one running it "+
				"(version skew), or the name is a typo", name, strings.Join(known, ", "))
			return 0
		}
		if !seen[name] {
			seen[name] = true
			report(name)
		}
		L.Push(L.NewFunction(func(*lua.LState) int { return 0 }))
		return 1
	}))
	// Locked, like ctx.managed's: a script must not be able to swap the guard out and
	// turn a reported skip back into the opaque failure it replaces.
	L.SetField(mt, "__metatable", lua.LString("locked"))
	L.SetMetatable(yolo, mt)
}

// buildDeriveCtxTable exposes the derive inputs: the three sentinels
// (ctx.tombstone, ctx.empty_array, ctx.in_full), ctx.agent / ctx.surface, the resolved
// selection (ctx.selected_provider, ctx.profile_name), and one read-only table per live
// source (ctx.mcp_servers, ctx.lsp_servers).
func buildDeriveCtxTable(L *lua.LState, ctx *DeriveCtx, sentinel, emptyArr *lua.LUserData) (*lua.LTable, error) {
	t := L.NewTable()
	L.SetField(t, "tombstone", sentinel)
	L.SetField(t, "empty_array", emptyArr)
	// ctx.in_full(t): declare that t is a table this derive REGENERATES IN FULL (CO13). It
	// wraps rather than marks, so the declaration travels with the value to wherever the
	// derive puts it and the decoder can refuse it anywhere but a top-level key. Anything
	// but a table is refused at the call, where the line number still points at the mistake.
	L.SetField(t, inFullName, L.NewFunction(func(L *lua.LState) int {
		tbl := L.CheckTable(1)
		ud := L.NewUserData()
		ud.Value = inFullTable{table: tbl}
		L.Push(ud)
		return 1
	}))
	L.SetField(t, "agent", lua.LString(ctx.Agent))
	L.SetField(t, "surface", lua.LString(ctx.Surface))
	L.SetField(t, "selected_provider", lua.LString(ctx.SelectedProvider))
	L.SetField(t, "profile_name", lua.LString(ctx.ProfileName))
	L.SetField(t, "via_url", lua.LString(ctx.ViaURL))
	// ctx.profile, always a table. Keys are sorted because a Go map has no order and a
	// derive that iterates it must not see a different order between runs.
	profile := L.NewTable()
	for _, k := range sortedStringKeys(ctx.Profile) {
		L.SetField(profile, k, lua.LString(ctx.Profile[k]))
	}
	L.SetField(t, "profile", profile)
	have := sourceCapabilities(ctx)
	for _, src := range knownDeriveSources {
		table := ctx.Tables[src]
		if table == nil {
			table = map[string]any{}
		}
		if src == sourceMCPServers {
			table = withoutProvidesKey(eligibleMCPServers(table, have))
		}
		lv, err := goToLua(L, table)
		if err != nil {
			return nil, fmt.Errorf("luahook: marshalling ctx.%s: %w", src, err)
		}
		L.SetField(t, src, lv)
	}
	return t, nil
}

// sourceCapabilities resolves the ACTIVE authentication source's capability set —
// agent-auth-modes.md §6.1's "if the active agent/mode already has capability C" — as a
// set, for eligibleMCPServers to ask membership of.
//
// An authentication source (docs/reference/mcp-configuration.md#authentication-source)
// is the credential-and-endpoint mode this render's agent runs under. There are exactly
// two, and the selection is what tells them apart rather than the agent's name:
//
//   - a SELECTED PROVIDER, whose capabilities are a field of its row in the composed
//     providers table — pack default under user override, one key, already in ctx;
//   - the agent's BUILT-IN login, when no profile selects a provider at its CLI name.
//     It has no row to carry anything, which is why NativeCapabilities exists.
//
// A selected provider that the composed table has no row for resolves to the EMPTY set,
// not to the built-in one. The two are different sources — an absent row means the
// launcher composed nothing for that name, never that the agent fell back to its own
// login — and silently substituting the agent's capabilities there would suppress an MCP
// server on a source that never claimed to replace it.
func sourceCapabilities(ctx *DeriveCtx) map[string]bool {
	names := ctx.NativeCapabilities
	if ctx.SelectedProvider != "" {
		names = nil
		if entry, ok := ctx.Tables[sourceProviders][ctx.SelectedProvider].(map[string]any); ok {
			if list, ok := entry["capabilities"].([]any); ok {
				for _, v := range list {
					if s, ok := v.(string); ok {
						names = append(names, s)
					}
				}
			}
		}
	}
	have := make(map[string]bool, len(names))
	for _, n := range names {
		if n != "" {
			have[n] = true
		}
	}
	return have
}

// eligibleMCPServers is capability-driven MCP delivery: the configured servers minus the
// ones whose declared job the active source already does.
//
// THE RULE IS THE SERVER'S `provides` AGAINST THE SOURCE'S CAPABILITIES, and nothing
// else. A server with no `provides` is not making a claim this rule can answer, so it
// passes; a server providing a capability the source does not declare passes; only the
// exact-name match is dropped. That is what makes it generic — the same resolver
// suppresses a future `code_search` server for a source that declares `code_search`,
// with no edit here and none in any pack.
//
// It sits at the ctx boundary rather than in each agent's derive because this is the one
// place BOTH production callers pass through (packsurfaces.go's surface render and
// packload's env composition), so no agent can opt out and no agent has to opt in. The
// per-agent `provides == "web_search"` branches this replaced were the opt-in shape, and
// one of them had drifted: claude's suppressed web search for every profile that was not
// bedrock or codex, so a Kilo launch — a source with no native search — lost its search
// MCP because of the agent it was running under.
//
// Returns the input unchanged when the source declares nothing, which is the overwhelming
// case (no provider capabilities, no built-in declaration) and keeps the render
// byte-identical for it.
func eligibleMCPServers(servers map[string]any, have map[string]bool) map[string]any {
	if len(have) == 0 || len(servers) == 0 {
		return servers
	}
	out := make(map[string]any, len(servers))
	for name, v := range servers {
		if cfg, ok := v.(map[string]any); ok {
			if provides, ok := cfg["provides"].(string); ok && have[provides] {
				continue
			}
		}
		out[name] = v
	}
	return out
}

// withoutProvidesKey removes `provides` from every surviving MCP entry on the way out to
// a derive. The key is YOLO'S OWN vocabulary — the capability claim eligibleMCPServers
// answers just above — and an agent has no use for it, but four shipped packs (agy,
// claude, copilot, pi) copy an entry VERBATIM into the agent's own config file
// (`servers[name] = cfg`, or `return ctx.mcp_servers` whole), so a key left here lands in
// ~/.claude.json and its three siblings. codex and opencode rebuild the entry field by
// field and never carried it, which is why the leak looked like one agent's bug.
//
// It runs UNCONDITIONALLY, outside eligibleMCPServers, and both halves of that matter.
// Outside, because that filter returns its input unchanged when the source declares
// nothing — the overwhelming case, and exactly the case where the key leaks. And it
// removes a KEY from the survivors only: which servers are DELIVERED is the filter's
// decision alone, and this function has no way to change it.
//
// It sits at this boundary for the reason the filter's comment gives: one place both
// production callers pass through, so no agent can opt out and no agent has to opt in.
//
// ⚠ NOT beside the `requires_env` strip in entrypoint's LoadMCPServers, which is the
// obvious structural precedent and the wrong seat for this key. LoadMCPServers runs
// UPSTREAM of the derive, so a `provides` stripped there is gone before
// eligibleMCPServers can read it, and capability-driven MCP delivery degrades to a
// no-op — SILENTLY, with every existing test green, because a server with no `provides`
// is documented above as not making a claim the rule can answer. So two of the MCP entry
// keys are stripped at two different layers, deliberately: `requires_env` gates
// DELIVERY and has to go before the derive, `provides` FEEDS the filter and has to go
// after it. Do not unify them.
//
// Entries are COPIED rather than edited: the map handed in is the live config table the
// caller reuses across every surface it renders.
func withoutProvidesKey(servers map[string]any) map[string]any {
	out := make(map[string]any, len(servers))
	for name, v := range servers {
		cfg, isMap := v.(map[string]any)
		if !isMap {
			out[name] = v
			continue
		}
		if _, has := cfg["provides"]; !has {
			out[name] = v
			continue
		}
		stripped := make(map[string]any, len(cfg)-1)
		for k, kv := range cfg {
			if k == "provides" {
				continue
			}
			stripped[k] = kv
		}
		out[name] = stripped
	}
	return out
}

// sortedStringKeys returns a string map's keys sorted — a Go map has no order, and the
// ctx table a derive reads must not reshuffle between runs.
func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// knownDeriveSources are the live-table names exposed to a derive as ctx.<name>.
// Kept in step with manifest's Source* constants by the caller (which builds the
// Tables map from exactly these); listed here so a derive always sees every
// source as at least an empty table, never a nil index.
var knownDeriveSources = []string{sourceMCPServers, "lsp_servers", sourceProviders, "use_profiles"}

// The two source names this file reads by hand — the table it FILTERS and the table it
// resolves the filter's input from. Named because the strings appear twice each and a
// typo in one of them would silently disable capability-driven MCP delivery rather than
// fail: an unknown key reads as an absent table, which is the "source declares nothing"
// answer.
const (
	sourceMCPServers = "mcp_servers"
	sourceProviders  = "providers"
)

// deriveTableToGo is luaTableToGo specialized for the derive return: a value
// equal to the tombstone sentinel decodes to Go nil (the RFC-7386 delete marker
// the computed layer uses) instead of being dropped, and the empty-array sentinel
// decodes to []any{} instead of the ambiguous empty {}.
//
// A top-level value wrapped in ctx.in_full decodes to the plain object it wraps, and its
// key is returned in inFull (sorted) — the declaration leaves the layer here, so nothing
// downstream ever sees the wrapper.
func deriveTableToGo(tbl *lua.LTable, sentinel, emptyArr *lua.LUserData) (map[string]any, []string, error) {
	out := map[string]any{}
	var inFull []string
	var iterErr error
	tbl.ForEach(func(k, v lua.LValue) {
		if iterErr != nil {
			return
		}
		ks, ok := k.(lua.LString)
		if !ok {
			// The computed layer is always object-rooted; a non-string top-level key
			// is a derive bug worth surfacing rather than silently coercing.
			iterErr = fmt.Errorf("luahook: derive produced a non-string top-level key %s", k.Type())
			return
		}
		if wrapped, isInFull := inFullOf(v); isInFull {
			obj, err := decodeInFull(string(ks), wrapped, sentinel, emptyArr)
			if err != nil {
				iterErr = err
				return
			}
			out[string(ks)] = obj
			inFull = append(inFull, string(ks))
			return
		}
		gv, err := deriveValueToGo(v, sentinel, emptyArr)
		if err != nil {
			iterErr = err
			return
		}
		out[string(ks)] = gv
	})
	sort.Strings(inFull)
	return out, inFull, iterErr
}

// inFullOf reports whether v is a ctx.in_full wrapper, and the table it wraps.
func inFullOf(v lua.LValue) (*lua.LTable, bool) {
	ud, ok := v.(*lua.LUserData)
	if !ok {
		return nil, false
	}
	w, ok := ud.Value.(inFullTable)
	if !ok {
		return nil, false
	}
	return w.table, true
}

// decodeInFull decodes the table a top-level ctx.in_full wrapped, refusing one that is not
// an OBJECT. "Regenerated in full" is a claim about a table's ENTRIES, and every consumer
// keys it on an object: an array has no entries to call stale, and the one ambiguous shape
// — an empty table — decodes to an empty object here exactly as it does unwrapped, so
// `ctx.in_full({})` means "yolo regenerates this table and has nothing in it this run".
func decodeInFull(key string, tbl *lua.LTable, sentinel, emptyArr *lua.LUserData) (map[string]any, error) {
	gv, err := deriveNestedTableToGo(tbl, sentinel, emptyArr)
	if err != nil {
		return nil, err
	}
	obj, isObj := gv.(map[string]any)
	if !isObj {
		return nil, fmt.Errorf("luahook: derive wrapped %q in ctx.%s, which declares a table "+
			"of named entries regenerated in full; it holds a %T, not an object", key, inFullName, gv)
	}
	return obj, nil
}

// deriveValueToGo converts one Lua value, mapping the tombstone sentinel to Go
// nil and the empty-array sentinel to []any{}, and recursing into tables so a
// nested sentinel (a false flag under enabledPlugins; a defaulted args=[]) is
// preserved.
//
// It is never handed a TOP-LEVEL value's in-full wrapper (deriveTableToGo takes those
// first), so one arriving here is nested — and refused, because both consumers of the
// declaration (agentcfg's adoption drop, the host's table probe) are keyed on a top-level
// key. Honoring it silently at the wrong depth is the one reading worse than refusing.
func deriveValueToGo(v lua.LValue, sentinel, emptyArr *lua.LUserData) (any, error) {
	if ud, ok := v.(*lua.LUserData); ok {
		if _, isInFull := ud.Value.(inFullTable); isInFull {
			return nil, fmt.Errorf("luahook: derive used ctx.%s below the top level of the "+
				"computed layer; it declares a TOP-LEVEL key regenerated in full, and a nested "+
				"one has nothing that reads it", inFullName)
		}
		switch ud {
		case sentinel:
			return nil, nil // the tombstone: an explicit RFC-7386 delete
		case emptyArr:
			return []any{}, nil // a distinctly-typed empty array (JSON [], not {})
		default:
			return nil, fmt.Errorf("luahook: derive produced unexpected userdata")
		}
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return luaToGo(v)
	}
	return deriveNestedTableToGo(tbl, sentinel, emptyArr)
}

// deriveNestedTableToGo mirrors luaTableToGo's array/object discrimination while
// honoring the sentinels in values. A sentinel cannot appear as an array ELEMENT
// (the config model never puts nulls/empty-arrays inside arrays — marshal.go), so
// array elements go through the normal luaToGo; object values go through
// deriveValueToGo.
func deriveNestedTableToGo(tbl *lua.LTable, sentinel, emptyArr *lua.LUserData) (any, error) {
	strKeys := map[string]lua.LValue{}
	intKeys := map[int]lua.LValue{}
	otherKey := false
	tbl.ForEach(func(k, val lua.LValue) {
		switch key := k.(type) {
		case lua.LString:
			strKeys[string(key)] = val
		case lua.LNumber:
			f := float64(key)
			if i := int(f); float64(i) == f {
				intKeys[i] = val
			} else {
				otherKey = true
			}
		default:
			otherKey = true
		}
	})
	if len(strKeys) == 0 && !otherKey && len(intKeys) > 0 && contiguousFrom1(intKeys) {
		outArr := make([]any, len(intKeys))
		for i := 1; i <= len(intKeys); i++ {
			gv, err := luaToGo(intKeys[i]) // array elements: no sentinel
			if err != nil {
				return nil, err
			}
			outArr[i-1] = gv
		}
		return outArr, nil
	}
	outObj := map[string]any{}
	for k, val := range strKeys {
		gv, err := deriveValueToGo(val, sentinel, emptyArr)
		if err != nil {
			return nil, err
		}
		outObj[k] = gv
	}
	return outObj, nil
}
