# `wire-bridge` — the official pack that ships the anthropic → chat-completions bridge

The first `kind: "service"` pack. What it ships is **not a loophole**, and the
vocabulary says so on purpose: a loophole reaches *out* from the jail to a host
capability the jail lacks — the host grants something, and the security model
reviews the crossing. The bridge serves *inward*. It binds the jail's own
loopback, crosses no boundary, reads no host state, and holds no grant, which is
exactly why it is declared as a service (a daemon in a namespace, an endpoint
file, a restart policy, a reachability witness) rather than wearing loophole
vocabulary it would misuse — see
[wire-bridge.md](../../docs/reference/wire-bridge.md) §2, where the kind ruling and
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

(Why two bins: both declare `anthropic` among the protocols their program speaks —
claude only, copilot first — so for either of them a provider that offers only
`openai` resolves through this pack's adaptation, and a launch with either must
stage the listener that answers at the address below.)

With claude (or copilot) and cerebras selected and `-p cerebras` active, the
agent's provider environment points at `http://127.0.0.1:8214` — the address
THIS pack declares, composed into cerebras's entry by the resolver because
cerebras offers `openai`, the agent speaks `anthropic`, and this pack declares
the conversion between them
([protocol-resolution.md](../../docs/reference/protocol-resolution.md#the-four-outcomes)).
The daemon staged here answers at that address: it speaks the Anthropic Messages
wire to the agent, translates to Cerebras's chat-completions upstream, and reads
the credential once at boot from `yolo-user-env.sh`. Nothing about the setup
grows a second step, and no provider has to name a yolo-internal port.

Selected but with no active profile routed at a bridged provider (claude riding
zai, say), the daemon boots, reads the same selection table every agent honors,
finds nothing to serve, and idles healthy — one stderr line, no listener, no
endpoint file. That laziness is what makes the coarse bin condition precise
(wire-bridge.md §3.2/§3.4).

The listen port lives ONLY in this pack's own `adapter` declarations (`8214` for
`openai → anthropic`, `8215` for `openai-responses → anthropic`, both clear of
every baked service) — one writer, and it is the OWNER rather than each
consumer. It used to be the other way round: every bridged provider hand-wrote
the port into its own manifest, which worked for a pack author and left a
user-declared provider unable to be bridged at all. `count_tokens` deliberately
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
