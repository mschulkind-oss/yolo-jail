# `wire-bridge` — the official pack that ships the anthropic → chat-completions bridge

The first `kind: "service"` pack. What it ships is **not a loophole**, and the
vocabulary says so on purpose: a loophole reaches *out* from the jail to a host
capability the jail lacks — the host grants something, and the security model
reviews the crossing. The bridge serves *inward*. It binds the jail's own
loopback, crosses no boundary, reads no host state, and holds no grant, which is
exactly why it is declared as a service (a daemon in a namespace, an endpoint
file, a restart policy, a reachability witness) rather than wearing loophole
vocabulary it would misuse — see
[wire-bridge.md](../../docs/design/wire-bridge.md) §2, where the kind ruling and
the loophole/service decomposition are written down.

## The user story: nothing to configure

You do not select this pack. **The cerebras pack needs it whenever a consumer of
the bridged URL is in the launch**: `packs/cerebras` declares `needs:
[{"pack": "wire-bridge", "when_bins": ["claude", "copilot"]}]`, and the launcher
joins this pack to the selection automatically when a selected pack installs the
`claude` or the `copilot` CLI — printing one banner line to say so:

```console
+ wire-bridge (needed by cerebras: claude selected)
```

(Why two bins: claude reads an `anthropic` endpoint directly, and copilot's
derive *prefers* the anthropic endpoint of any provider that declares one — so
once cerebras declares the bridge's URL, both agents compose it, and a launch
with either must stage the listener that makes the URL true.)

With claude (or copilot) and cerebras selected and `-p cerebras` active, the
agent's provider environment points at `http://127.0.0.1:8214` — the loopback
URL cerebras's manifest declares as its `anthropic` endpoint — and the daemon
staged by this pack answers there: it speaks the Anthropic Messages wire to the
agent, translates to Cerebras's chat-completions upstream, and reads the
credential once at boot from `yolo-user-env.sh`. Nothing about the setup grows a
second step; the bridge is why the URL cerebras declares is true.

Selected but with no active profile routed at a bridged provider (claude riding
zai, say), the daemon boots, reads the same selection table every agent honors,
finds nothing to serve, and idles healthy — one stderr line, no listener, no
endpoint file. That laziness is what makes the coarse bin condition precise
(wire-bridge.md §3.2/§3.4).

The listen port lives ONLY in the provider's manifest URL (`8214`, clear of
every baked service) — one writer, no second knob. `count_tokens` deliberately
answers 404 so claude uses its own estimator instead of a fabricated count, and
inbound requests carry no auth because the jail is the boundary. What the bridge
never does: dial anything but the boot-selected upstream, listen off loopback,
log a body or a key (wire-bridge.md §5).

## Verifying

```console
$ yolo pack lint packs/wire-bridge      # claims + the strict manifest read
$ yolo pack footprint wire-bridge       # the service claim: one jail daemon,
                                        # one endpoint file, no grants
$ yolo check                            # packs ["claude","cerebras"]: the pack list
                                        # shows wire-bridge joined, with its cause
$ go test ./integration/ -run WireBridge   # the end-to-end: a launch whose claude
                                           # rides the bridge against an in-jail
                                           # stub upstream — no agent runs, no
                                           # external API is touched
```

Then the human check a test cannot stand in for: `claude` on a real task in a
`-p cerebras` launch, watching the first request come back translated.
