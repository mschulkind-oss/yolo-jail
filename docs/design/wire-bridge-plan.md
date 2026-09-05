# Plan: the wire bridge

**Design:** [`wire-bridge.md`](wire-bridge.md) · **Status:** ready · Written against `77b71e84`, 2026-09-04.
Precedence: the design wins on behavior, the tree wins on fact, this file is advice and is the first thing to be wrong.

## Map

| Path | Change |
| :--- | :--- |
| `internal/packdecl/packdecl.go` | `Manifest.Needs []PackNeed` — top-level key, modeled on `Supersedes` (same file; read its doc comment for why-top-level and copy the comment discipline) |
| `internal/packdecl/needs.go` | new — `PackNeed{Pack string, WhenBins []string}`, validation, tolerant-skew skip |
| `internal/packload/needs.go` | new — `ResolveNeeds(selected []*Pack) ([]*Pack, []Addition, error)`: transitive closure, cycle refusal |
| `internal/cli/run/packs.go` | run the closure after selection, before staging; print each addition (`+ <pack> (needed by <pack>: <bin> selected)`) |
| `internal/cli/check/…` | the same closure + line in `yolo check`'s pack listing |
| `internal/packdecl/kinds.go` | `KindService Kind = "service"` (kind census pin in `kinds_test.go` moves 18→19) |
| `internal/packdecl/contributes.go` | service contribution fields: the §2.1 service half — `jail_daemon{cmd, restart}`, `host_daemon{cmd…}`, `endpoint`, `platforms`, `serves`, `settings`; NO grant fields |
| `internal/wirebridge/` | new package — the translator (request/response/event mapping, zero deps outside stdlib) |
| `internal/wirebridge/*_test.go` | new — fixture-pair table tests |
| `cmd/yolo-jaild/` or existing dispatch | `wire-bridge` subcommand: read `YOLO_PROVIDERS`/`YOLO_PROFILES`/`YOLO_USE_PROFILES`, bind the loopback URL from the table, serve |
| `internal/entrypoint/…` | service jail_daemons join `YOLO_JAIL_DAEMONS` (reuse the loophole `JailDaemon` wire shape verbatim — both halves in one commit); endpoint via `svcendpoint.Publish` |
| `packs/wire-bridge/` | new pack — README + one `kind: service` contribution |
| `packs/cerebras/pack.json` | `needs: [{pack: wire-bridge, when_bins: [claude]}]`, `endpoints.anthropic.base_url: http://127.0.0.1:8214`, `options.context_window: "65536"` |
| `packs/embed.go`, `AGENTS.md` | census: fifteen packs, nine CLI-less in four kinds; embed list |
| `integration/wirebridge_test.go` | new — stub upstream (`httptest`), anthropic-shaped curls through a real launch |

## Reuse

- `internal/svcendpoint` — `Publish`/`Read`/`Probe` for the endpoint file; the oauth broker is the worked path.
- `internal/entrypoint/providers.go` — `LoadProviders`/`LoadUseProfiles`/`LoadProfiles` parse everything the daemon reads at boot.
- `internal/loopholes/loopholes.go:75-146` — `JailDaemon` shape and lifecycle; the service kind is its extraction (design §2.1 table).
- Test harness: `internal/cli/run/zaipack_test.go` — `officialPack`, `zaiLaunch`, `envArgValues`, `hydratedKey` are package-shared; mirror `cerebraspack_test.go` for a real-pack test.
- `retireHome`/`retireOptions`/`discardBuf` (`providerpreflight_test.go`) for options-scoped tests.
- `yolo-user-env.sh` writer: `internal/cli/run/userenv.go` — the daemon reads this file at startup; its path constant already exists there.

## House style (not obvious)

- packdecl doc comments ARE the reference (`yolo pack schema` reads them); every new field gets one, and facts carry measurement dates.
- Tests must fail if the production call site is deleted, not just the helper (AGENTS.md "Testing").
- No attribution trailers in commits; conventional prefixes; `just format` before every commit.
- Error text names the fix (every refusal in this repo names its escape hatch).

## Traps

- **`kinds_test.go` pins the kind census** — adding `service` fails it; update the count in the same commit.
- **Two-halves skew**: `YOLO_JAIL_DAEMONS` is written host-side (`assemble_parts.go`) and read in-jail (`entrypoint/runtime.go`); the source-skew gate cannot see it. Reuse the existing shape; if it must change, both halves land in ONE commit.
- **Staging prune**: a closure-added pack must be staged like a selected one (`internal/packstage`) — the mount is the filter; test with `TestEmbedMatchesTree` + a staging test.
- **`git add` before nix rebuild** — integration tests build from tracked files only; untracked new files are invisible to the image.
- **Commit messages with parens/quotes**: write the message to a file and `git commit -F` — inline `-m` with unescaped quotes has eaten two messages this week.
- Port 8214: confirm nothing in `flake.nix`/image binds it (nothing does today, checked 2026-09-04).

## Build order

1. **`needs` vocabulary** — packdecl fields + closure + banner/check lines + fixture-pack tests. → `go test ./internal/packdecl/ ./internal/packload/ ./internal/cli/run/`
2. **Translator** — `internal/wirebridge` library + fixture table tests, no daemon. → `go test ./internal/wirebridge/`
3. **`kind: service` + daemon** — kind, entrypoint rendering, `yolo-jaild wire-bridge` subcommand, endpoint, witness. → `go test ./internal/entrypoint/ ./internal/cli/run/`
4. **Pack + cerebras + census + integration** — everything in the map's tail, one commit (the `needs` entry and the endpoint it makes true cannot ship apart). → `just test-fast`, then `just test ./integration -run WireBridge`

Steps 1 and 2 are independent — parallelizable. Step 3 needs 2's library; step 4 needs 1+3.

## Ships with

- Unit: closure (transitive, cycle, already-selected join, condition-false no-op); translator rows (each §4 table row, both directions, SSE event-for-event, count_tokens 404, unknown-block 400 naming the block); daemon boot table-read (bridged, idle).
- Integration: `integration/wirebridge_test.go` — launch with `claude+cerebras` (no bridge in config), assert the banner line, curl the bridge with anthropic fixtures against an `httptest` stub upstream. **No agent binary runs; no real API call from a test.**
- Rewrites: none expected — no existing behavior changes (the zai/cerebras delivery paths are untouched).
- Docs: `AGENTS.md` census; `packs/embed.go` census; `docs/reference/providers.md` (needs vocabulary row + cerebras endpoint row); `packs/cerebras/README.md` (claude row flips to bridged, needs note); new `packs/wire-bridge/README.md`; `yolo pack lint/footprint` output for the edge.
- Config surfaces: none new (no settings, no env knobs — port lives in the manifest URL only).
- Nested-jail verification (mandatory, after step 4): `cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash` after `just build-go`; verify banner + one curl with `max_tokens=8` (manual, real upstream OK — it is not an automated test).

## Don't

- No `reasoning_effort` anywhere (WB-D15); no count_tokens estimate (WB-D14); no inbound auth (WB-D4).
- Don't touch the five loophole packs — their re-forming is a follow-up design, not this build.
- No new `cmd/` binary (four-binary ship-set trap); the daemon is a `yolo-jaild` subcommand.
- No new module deps — stdlib `net/http` only, vendored tree stays as-is.
- Don't gate claude's derive on anything — composition stays untouched (design §3.3).

## Blockers

- None. Every question is ruled (`wire-bridge.md` Decision Ledger). **Stop and ask** only if the `YOLO_JAIL_DAEMONS` wire shape cannot be reused as-is.
