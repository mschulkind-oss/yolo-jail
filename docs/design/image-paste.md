---
title: "Image paste in a jail: the keypress fetches the image, and the agent gets a path"
date: 2026-10-09
status: in-review
stage: DESIGN
next: "Rule OQ-PA2, how far Ctrl+V is consumed; the first build slice (§10) waits on nothing else, and measurements M1 to M3 can run now"
tags: [design, ttyproxy, clipboard, paste, images, security, macos-user]
summary: "Every agent yolo ships reads a pasted image from the host clipboard, which a jail cannot reach, so Ctrl+V on an image does nothing inside one. The design puts the read where the user's keypress already is: the host-side terminal proxy sees the one Ctrl+V key, reads the host clipboard's image itself, decodes it and writes a fresh PNG into the workspace's .yolo/paste, and types that file's in-jail path into the agent as a bracketed paste. Every shipped agent attaches a pasted image path, so one mechanism covers all six, Codex and Copilot included, and the jail gets no clipboard channel at all. A clipboard without an image, a program that has not turned bracketed paste on, or a host with no clipboard reader forwards the key unchanged. Darwin backends have no proxy today; the proxy's key path is ported, and a host `yolo paste` command covers every backend meanwhile."
---

# Image paste in a jail: the keypress fetches the image, and the agent gets a path

**Status:** 2026-10-09. Nothing built. Written from a two-agent research pass over the six shipped
agents and yolo's terminal proxy; every yolo file and line cited here was re-read against the tree
at `b573fd48a` the same day. The per-agent facts are from that research and are marked where
unmeasured.

> **In short.** The authority for an image paste is the user's keypress, and yolo already holds
> that keypress on the host before the jail sees it. So the host reads the clipboard, not the jail:
> the proxy turns one Ctrl+V into a saved PNG in the workspace and types its path into the agent,
> which every shipped agent already treats as an attached image.

**Why it matters.** Image paste is broken in every jail for every agent today, and the obvious fix,
handing the jail a clipboard, gives the agent a standing read of everything the user copies,
passwords included.

**The shape.** Three parts: a host-side **clipboard reader** that returns one image or nothing, a
**paste store** under `<workspace>/.yolo/paste/`, and a **paste arm** *(coined here)* in the terminal
proxy that joins them to the key. A host command, `yolo paste`, uses the first two without the third.

**Cost.** Ctrl+V stops reaching the program in the jail while an image is on the host clipboard
and that program has bracketed paste on ([§4](#4-the-chord-consumed-only-when-it-can-be-honored)).
The darwin backends need the proxy's input path ported before they get the key.

**Start at [§3](#3-the-recommendation-type-the-path-dont-open-a-channel)** — why a typed path
beats an armed clipboard channel. The rest follows from it.

**Needs your ruling:** [OQ-PA1](#OQ-PA1), [OQ-PA2](#OQ-PA2), [OQ-PA3](#OQ-PA3).

**Reads with:** [`ctrl-z-and-the-tty-proxy.md`](../reference/ctrl-z-and-the-tty-proxy.md) (the
proxy this extends), [`loophole-transport.md`](../reference/loophole-transport.md) (the channel this
deliberately does not use), [`host-run-mailbox.md`](host-run-mailbox.md) (the same
"host writes into `.yolo/`, jail reads the workspace bind" pattern).

---

## 1. The brief, and the goals it sets

The maintainer's brief, 2026-10-09, verbatim:

> "explore how we can enable image pasting in all of the agents that we support. The issue is that
> generally they require some direct access to the host because the image pasting is handled by the
> agent and it's fetched and that connection is broken inside a jail for a good reason. So we're
> going to have to make some loophole that does this appropriately. I don't know if we can like grab
> the keyboard shortcut at the YOLO level before it hits the agent and then like do whatever munching
> there so we can make sure the user really was the one that requested this."

The goals, numbered so later sections can cite them:

- **P1. Every agent yolo ships.** Claude Code, Codex, Copilot CLI, opencode, pi and agy. kilo and
  cerebras install no program and have no paste.
- **P2. The user's keypress is the authority.** An image crosses only because a human pressed the
  paste key on the host terminal, at that moment.
- **P3. The agent alone cannot read the host clipboard.** Nothing in the jail can ask for the
  clipboard, at any time.
- **P4. Nothing but images crosses.** No text, no file lists, no metadata; only pixels the host has
  decoded.
- **P5. Every stop names the next step**, by the
  [happy path principle](../reference/happy-path-principle.md), and **every crossing is disclosed**,
  by [the no-quiet-mode ruling](../reference/report-tiers.md#why-its-this-way).

### Non-goals

- **Text paste.** The terminal's own paste (Ctrl+Shift+V, Cmd+V) already delivers text as a
  bracketed paste into the jail and keeps working unchanged.
- **Copy-out** (jail to host clipboard). Agents use OSC 52 for that, which the terminal handles.
- **Drag-and-drop of a host file.** A dropped host path does not exist in the jail; it is a
  different feature (a path translation), not this one.
- **Non-image clipboard formats**: files, rich text, HTML.
- **A clipboard for programs in the jail generally.** `xclip -o` in a jail keeps failing.

## 2. What exists today

### 2.1 How each agent gets a pasted image

From the research pass (agent versions: Claude Code 2.1.296, Codex 0.162.1, Copilot CLI 1.0.92,
opencode 1.18.35, pi 1.0.1, agy 1.2.9):

| Agent | Paste key | Where the bytes come from on Linux | Attaches a pasted image path? |
| :--- | :--- | :--- | :--- |
| Claude Code | Ctrl+V | runs `xclip` or `wl-paste` | yes: an absolute path ending `.png`/`.jpg`/`.gif`/`.webp` |
| Codex | Ctrl+V, Ctrl+Alt+V | in-process X11/Wayland (arboard) | yes (`handle_paste_image_path`) |
| Copilot CLI | Ctrl/Alt/Super+V | in-process clipboard library | yes: a bracketed path becomes an `[image]` attachment |
| opencode | Ctrl+V | runs `wl-paste`, then `xclip` | yes |
| pi | Ctrl+V | runs `wl-paste` or `xclip` | its own paste inserts a temp path as text, which it reads |
| agy | Ctrl+V | runs `wl-paste`/`xclip` (arguments unverified) | unverified ([M3](#9-measurements-needed)) |

On macOS, Claude Code and opencode run `osascript` for `«class PNGf»`; pi, Codex and Copilot use
NSPasteboard in-process. **No agent implements OSC 5522** (kitty's clipboard protocol), so there is
no terminal-level image channel to lean on.

The column that decides this design is the last one: **every agent with a verified answer attaches
an image whose path is pasted to it.** The column before it is why a clipboard shim cannot be the
whole answer: Codex and Copilot never run an external program, so faking `xclip` reaches neither.

### 2.2 yolo's side

- **The terminal proxy runs only on Linux.** `internal/ttyproxy` is `//go:build linux`; every podman
  session on Linux runs under it, fresh launch and attach alike
  ([`run.go:2422`](../../internal/cli/run/run.go#L2422),
  [`run.go:3035`](../../internal/cli/run/run.go#L3035), both through `runArmedSession` in
  [`proxy_linux.go:56`](../../internal/cli/run/proxy_linux.go#L56)). On darwin the session is a
  plain `exec.Command` on the inherited terminal
  ([`proxy_other.go:23`](../../internal/cli/run/proxy_other.go#L23)), and macos-user's
  `RunSession` is the same shape
  ([`macosuserarm.go:212`](../../internal/cli/run/macosuserarm.go#L212)). `yolo host` execs the
  agent on the host terminal.
- **The proxy already intercepts one key.** It puts the host terminal in raw mode
  ([`ttyproxy.go:427`](../../internal/ttyproxy/ttyproxy.go#L427)), reads stdin in chunks, and
  strips a Ctrl-Z before forwarding the rest to the pty
  ([`ttyproxy.go:667`](../../internal/ttyproxy/ttyproxy.go#L667)).
  `matchEscapedCtrlKey` matches a Ctrl+key in both escape encodings, the kitty
  protocol's `CSI <code>;<mods>u` and xterm's `CSI 27;<mods>;<code>~`, beside the raw control byte
  ([`suspendkey.go:114`](../../internal/ttyproxy/suspendkey.go#L114)). `classifyInput` already
  recognizes a chunk that is **exactly** one key and nothing else
  ([`suspendkey.go:224`](../../internal/ttyproxy/suspendkey.go#L224)).
- **Ctrl-C is forwarded, by a 2026-09-19 ruling** that reversed the proxy stealing it
  ([`ttyproxy.go:637`](../../internal/ttyproxy/ttyproxy.go#L637)): a key that means something to
  the program in the jail reaches that program. [§4](#4-the-chord-consumed-only-when-it-can-be-honored)
  weighs this design against that ruling.
- **Jail output reaches the terminal unfiltered**
  ([`ttyproxy.go:611`](../../internal/ttyproxy/ttyproxy.go#L611)). So the jail can make the
  terminal send replies (cursor reports, window reports, OSC 52 replies) that arrive on stdin
  looking like input.
- **Loopholes defend against sibling jails, not the jail's own agent**
  ([`loophole-transport.md` threat model](../reference/loophole-transport.md#threat-model)). A
  loophole the agent can call is a loophole the agent can call whenever it likes.
- **No clipboard code exists**, and the jail gets no X11, Wayland or D-Bus socket.
- **macos-user's Seatbelt profile is `(allow default)`**
  ([`seatbelt.go:171`](../../internal/macosuser/seatbelt.go#L171)), and it denies no mach-lookup
  at all (the one it had, `com.apple.diagnosticd`, was dropped:
  [ML-D8](../../packs/macos-log/README.md#the-ledger)). Nothing denies the pasteboard service, so the sandbox account **may already read the user's
  pasteboard**. Unmeasured; [§6.3](#63-macos-user) and [OQ-PA1](#OQ-PA1).

## 3. The recommendation: type the path, don't open a channel

**One mechanism for every agent: the paste arm.** On a host terminal session:

1. The user presses Ctrl+V.
2. The proxy sees a stdin chunk that is exactly that one key
   ([§5.1](#51-only-a-chunk-that-is-exactly-one-key)).
3. If the program in the jail has bracketed paste on
   ([§4](#4-the-chord-consumed-only-when-it-can-be-honored)), the proxy asks the **clipboard
   reader** for an image. Input typed meanwhile is held, in order; jail output keeps flowing.
4. Image present: the reader's bytes are decoded and re-encoded as a new PNG
   ([§7](#7-the-image-from-clipboard-to-file)), written into the **paste store**, and the proxy
   writes `ESC[200~` + the file's in-jail path + `ESC[201~` to the pty **instead of** the key.
5. No image, or any failure: the original key bytes go to the pty unchanged, then the held input.

The agent receives what it would have received had the user pasted the file's path, and attaches
the image by its own path-paste code. pi, whose own Ctrl+V inserts a temp path, ends up in the same
state it reaches natively.

### 3.1 Alternatives, with verdicts

| | Design | Covers | What the jail can do | Verdict |
| :--- | :--- | :--- | :--- | :--- |
| **A** | **Paste arm: the proxy types a path** | all six | nothing new | **Chosen** |
| B | Image-only clipboard loophole: `xclip`/`wl-paste` intercept shims in the jail call a host daemon, armed for a few seconds by the proxy observing Ctrl+V | Claude, opencode, pi, agy; **not** Codex or Copilot | read the clipboard image during each armed window | Rejected |
| B+X | B plus a private in-jail X server whose selection owner serves the daemon's image, for Codex and Copilot | all six, unverified | same as B | Rejected |
| C | Clipboard loophole with a host prompt per read | all six | ask, and wait for a human | Rejected |
| D | Host `yolo paste` only: save the image, print and copy its path | all six | nothing new | **Kept, as the fallback** ([§6.4](#64-the-fallback-on-every-backend-yolo-paste)) |

Why A over B, in order of weight:

- **B does not reach P1.** Codex and Copilot read the clipboard in-process, so B needs A anyway, or
  needs B+X: a display server per jail, a selection-owner daemon, and environment wiring nobody has
  verified either agent honors. Two mechanisms where one covers everything is the second path
  [the fill-the-matrix principle](../reference/fill-the-matrix-principle.md) says to delete.
- **B opens a channel the agent can drive.** However short the armed window, a jail process polling
  `xclip -o` reads the image during it, and the loophole's threat model has no defense against the
  jail's own agent by design. A has no jail-to-host request at all: the host decides, writes a file
  and types. P3 holds by construction rather than by a timer.
- **B is far more to build**: a host daemon run by the keeper, an in-jail client binary (a new
  `cmd/`, with its [`flake.nix`](../../flake.nix) and
  [`stage-source-bundle.sh`](../../scripts/stage-source-bundle.sh) entries), intercept
  contributions, and a host-only socket from each session's proxy to the keeper's daemon so the
  arming signal is not forgeable from the jail.
- **What B buys** is the agent's native flow for four agents, and the research found nothing an
  agent does with a clipboard image that it does not also do with a pasted path. Revisit B only if
  [M3](#9-measurements-needed) finds an agent whose path paste is worse in a way users notice.

C needs the doorbell the [boundary broker](boundary-broker.md) designs and nobody has built, and it
asks the user to approve, in a second place, a paste they just requested by pressing a key.

**Is A a loophole?** Not in yolo's defined sense: a
[loophole](../reference/loophole-system.md) is a host capability the jail calls through a mediated
channel. Here the jail calls nothing. The image arrives through the one channel the user already
drives, their own keystrokes, which is the brief's "grab the keyboard shortcut at the YOLO level".

## 4. The chord: consumed only when it can be honored

**The paste arm consumes Ctrl+V only when all three hold**; otherwise the key is forwarded byte for
byte, as today:

1. **The chunk is exactly one Ctrl+V press** ([§5.1](#51-only-a-chunk-that-is-exactly-one-key)).
2. **Bracketed paste is on** in the session's pty: the proxy has seen the program in the jail send
   `CSI ? 2004 h` and no later `CSI ? 2004 l` ([§8.1](#81-bracketed-paste-detection)). The
   **bracketed-paste gate** *(coined here)*.
3. **The host clipboard holds an image** that passes [§7](#7-the-image-from-clipboard-to-file).

**Why consuming is right here, against the Ctrl-C ruling.** That ruling forwarded Ctrl-C because
stealing it took away something the user wanted from the program: clearing a line, interrupting
generation. Inside a jail, every agent's own Ctrl+V handler looks for a clipboard it cannot reach
and reports "no image". Consuming the key when an image is present delivers what the user pressed
it for; forwarding it as well would make the agent's handler run and report a failure next to the
attached image.

**Why the bracketed-paste gate.** A program that has turned bracketed paste on has promised to treat
bracketed text as pasted data, never as keystrokes. Typing a path into a program without that
promise, such as vim in Normal mode, would run it as commands. Every shipped agent's terminal
library turns bracketed paste on (Ink, crossterm, opentui and pi's TUI; to confirm in
[M3](#9-measurements-needed)).

**What it still costs, said plainly.** bash (readline) and vim also turn bracketed paste on. While an
image sits on the host clipboard, Ctrl+V at a jail's bash prompt types the image's path instead of
quoting the next character, and in vim it inserts the path instead of starting a block selection.
Both are visible and undone in one keystroke, and neither happens with text or nothing on the
clipboard. [OQ-PA2](#OQ-PA2) asks whether that is acceptable.

**Other chords.** Codex's Ctrl+Alt+V and Copilot's Alt/Super+V are forwarded unchanged: one
chord, the one every agent shares, is the feature. Super is usually taken by the desktop, and
Ctrl+Alt+V is Codex's alone. A configurable chord is not in v1; it would be an addition, not a
change.

## 5. The forged-keystroke defense

The adversary is the program in the jail trying to make the proxy fetch an image the user did not
paste. Its only lever into the proxy's stdin is making the host terminal reply to something it
printed.

### 5.1 Only a chunk that is exactly one key

The arm matches a read from the host terminal that is, in its entirety, one of:

- the byte `0x16`;
- `CSI 118 ; <mods> u` or `CSI 118 ; <mods>:<event> u`, Ctrl only (locks masked), press or repeat;
- `CSI 27 ; <mods> ; 118 ~`, Ctrl only.

This is `classifyInput`'s rule ([§2.2](#22-yolos-side)) with key 118, reusing
`matchEscapedCtrlKey` and its modifier handling. **A Ctrl+V inside a larger chunk is never
matched.** That differs on purpose from Ctrl-Z, which is stripped wherever it appears: a missed
Ctrl-Z wedges the session, a missed Ctrl+V only means the agent's own handler runs.

Why that is enough:

- **Terminal replies start with ESC** and are not the key's encoding: cursor reports end in `R`,
  device attributes in `c`, window reports in `t`, OSC 52 replies are `ESC ] 52 ;`. None is
  `CSI 118;5u` or `CSI 27;5;118~`. The one terminal reply that can be an arbitrary byte, xterm's
  answerback to ENQ, is empty by default; a user who sets it to `0x16` has configured this.
- **A text paste cannot forge it**: with bracketed paste on, the terminal wraps pasted text in
  `ESC[200~ … ESC[201~`, so even a clipboard holding the lone byte `0x16` arrives as a larger chunk.
  With bracketed paste off, the gate forwards everything anyway.
- **A key-release event never fires** (kitty's `:3`), so one press is one paste.

### 5.2 What a forgery would win

Even a successful forgery reads only the image the user put on their own clipboard, writes it into
the workspace the jail already owns, and types a path the user sees. That is a privacy leak of one
image, not a capability, and the end-of-session line ([§8.3](#83-disclosure)) names every paste.

## 6. Per backend

| Backend | Proxy today | Paste arm | Until the arm exists |
| :--- | :--- | :--- | :--- |
| Linux podman | yes, every session | first build slice | — |
| darwin podman machine | no | needs the proxy's input path ported | `yolo paste` |
| Apple Container | no | same port | `yolo paste` |
| macos-user | no | same port, after [OQ-PA1](#OQ-PA1) | `yolo paste`, and maybe an open pasteboard |
| `yolo host` | not needed | none: the agent reads the clipboard natively | — |

### 6.1 Linux podman

The arm lives in the existing pump loop. Each session, fresh or attached, runs its own proxy and so
its own arm, with its own bracketed-paste state. The host clipboard is the one the proxy's session
sees: `WAYLAND_DISPLAY` or `DISPLAY` from the environment `yolo` was started with.

### 6.2 Darwin container backends

**Port the proxy's input path to darwin.** The ported half is the raw-mode terminal, the pty pair
and the pump; the pieces in the build tag that are Linux-only are the pty allocation calls, not the
logic. This also gives darwin the Ctrl-Z protection the proxy exists for, which darwin sessions
lack today. The port is the larger half of this design's cost and is sequenced after the Linux
slice ([§10](#10-first-build-slice)). The host reader on macOS is described in
[§7.1](#71-reading-the-host-clipboard).

### 6.3 macos-user

Two separate facts:

- **The arm** comes with the same darwin port; macos-user's `RunSession` would run its command
  under the ported proxy.
- **The sandbox account may already reach the user's pasteboard.** Nothing in the Seatbelt profile
  denies the pasteboard's mach service ([§2.2](#22-yolos-side)). Whether a process of the sandbox
  account, launched as it is, can read the logged-in user's pasteboard depends on which bootstrap
  namespace it inherits, and nobody has measured it ([M1](#9-measurements-needed)). If it can,
  agents' native paste already works on macos-user, and so does `pbpaste` of whatever text the user
  copied last, which breaks P3 for every jail on that backend today. Closing it is a one-rule deny
  in the profile; when to close it is [OQ-PA1](#OQ-PA1).

### 6.4 The fallback on every backend: `yolo paste`

A host command, run in a host terminal whose working directory is inside the workspace:

1. reads the clipboard image with the same reader, applying the same checks;
2. writes it into that workspace's paste store;
3. prints the file's in-jail path; and
4. **replaces the host clipboard with that path as text**, so the user's next ordinary paste
   (Cmd+V, Ctrl+Shift+V) into the agent pastes the path, which the agent attaches.

Step 4 is what makes it usable without retyping a path, and it is said in the command's output
("the clipboard now holds the path; paste it into the agent"). It costs the clipboard's image,
which is saved in the store. A clipboard with no image refuses with the next step ("copy an image,
then run `yolo paste` again"). It works on every backend because it needs no proxy, only the
workspace bind, the same channel [`host-run-mailbox.md`](host-run-mailbox.md) uses. It resolves the
workspace the way `yolo config` verbs do, upward from the working directory, and refuses outside
one with "run it from inside the workspace the jail has open".

## 7. The image, from clipboard to file

### 7.1 Reading the host clipboard

The reader returns one image's bytes and its declared type, or "no image", and is the single place
that knows the host's clipboard tools.

| Host | First choice | Then | Neither |
| :--- | :--- | :--- | :--- |
| Linux, `WAYLAND_DISPLAY` set | `wl-paste --list-types`, then `wl-paste --type <type>` | X11 below, if `DISPLAY` is set | reader off |
| Linux, `DISPLAY` set | `xclip -selection clipboard -t TARGETS -o`, then `-t <type> -o` | — | reader off |
| Linux, no display | — | — | reader off: "no graphical session" (SSH without X forwarding) |
| macOS | `osascript -e 'the clipboard as «class PNGf»'`, written to a temp file | — | — |

- **Type preference**: `image/png`, then `image/jpeg`, then `image/gif`. Any other image type alone
  on the clipboard (webp, bmp, tiff) is refused with "copy it as PNG"; screenshot tools and browsers
  offer PNG alongside it. On macOS the `«class PNGf»` coercion converts a TIFF screenshot.
- **No version gates**: whichever `wl-paste`, `xclip` or `osascript` is on the PATH yolo was
  started with is used, and breakage is fixed when it happens (the 2026-10-09 ruling).
- **A deadline of 2 seconds** per paste for the whole read; a clipboard owner that does not answer
  counts as a failure.
- The reader is probed once at session start (is a tool present, is a display set), which decides
  the launch's disclosure line ([§8.3](#83-disclosure)).

### 7.2 Checks, and why the image is re-encoded

| Check | Default | On failure |
| :--- | :--- | :--- |
| Bytes read | at most 32 MiB | refused: too large |
| Dimensions, read from the header before decoding | at most 40,000,000 pixels | refused: too large |
| Full decode as the declared type | must succeed | refused: not an image |
| Output | a **new PNG** encoded from the decoded pixels | — |

**Re-encoding is what makes P4 true.** The file in the jail is pixels the host decoded and wrote,
so EXIF (camera, GPS), embedded profiles, trailing data and anything a crafted file hides do not
cross. The decoders are Go's standard library, memory-safe, so a hostile image on the clipboard
(the jail can put one there through a terminal's OSC 5522 write, where enabled) costs at most the
caps above. No new dependency: `image/png`, `image/jpeg` and `image/gif` are in the standard
library, which is also why webp is refused rather than converted.

### 7.3 The paste store

- **Location**: `<workspace>/.yolo/paste/`, one flat directory per workspace. `.yolo` is ignored by
  git, so a paste is never committed, and the workspace bind makes it readable in the jail on every
  backend.
- **Name**: `<UTC time, to the second>-<8 random hex>.png`. Unique without coordination, so two
  sessions pasting at once never collide.
- **Written atomically** (temporary name, then rename) before the path is typed, so the agent never
  reads a partial file.
- **The in-jail path** is the workspace's mount destination in the jail joined with
  `.yolo/paste/<name>`. The launcher already knows the destination (`/workspace` on a container backend);
  the proxy is handed it, never derives it.
- **Mode and owner**: what a file the user creates in the workspace gets, so the jail reads it
  exactly as it reads the user's own files ([M4](#9-measurements-needed) per backend).
- **Retention**: a file older than **7 days** is deleted, and beyond **200 files** the oldest go
  first, by the launch's housekeeping and by `yolo prune`. Age is the right rule here: these are
  copies of user content the agent has already read, not artifacts a running process holds. pi,
  which reads the file when the model asks rather than at paste time, is covered by the week.
- **Workspace read-only**: a `workspace_readonly` entry covering `.yolo` does not apply, because
  the host writes, not the jail.

## 8. Security model, disclosure, and the terminal around it

### 8.1 Bracketed paste detection

The proxy reads the jail's output stream, which it already copies to the terminal, and tracks one
bit per session:

- `CSI ? <params> h` with 2004 among its parameters sets it; `… l` clears it; `ESC c` (full reset)
  clears it.
- A sequence split across two reads is handled by carrying at most the last 16 bytes of a read
  forward. Output is never delayed or altered; the scan only observes.
- Initial state is off, so a session in which nothing turned it on never consumes Ctrl+V.

### 8.2 tmux, attach, SSH

- **tmux inside the jail**: tmux is the program on the proxy's pty, and it turns bracketed paste on
  toward the proxy exactly when its active pane has it on, then forwards the brackets to that pane.
  So the gate tracks the active pane with no tmux awareness. A tmux prefix bound to Ctrl+V would be
  consumed while an image is on the clipboard; that is the same cost as bash's.
- **yolo inside a host tmux**: the host tmux forwards Ctrl+V as a key; the proxy writes the
  injection to the jail's pty, not back through the host tmux, so nothing changes.
- **Attach**: each attaching session has its own proxy, arm and bracketed-paste bit. Concurrent
  sessions share the store and cannot collide ([§7.3](#73-the-paste-store)).
- **SSH to the host**: with no display the reader is off and the launch says so; with X forwarding,
  `DISPLAY` points at the user's own machine and the paste reads the clipboard they actually
  copied to.

### 8.3 Disclosure

- **At launch**, one line, never suppressible
  ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)):
  - `image paste: Ctrl+V with an image on the host clipboard saves it to .yolo/paste/ and types its path (wl-paste)`
  - or, naming the next step: `image paste: off — no clipboard reader on this host; install wl-clipboard (wl-paste) or xclip`
  - or `image paste: off — no graphical session (DISPLAY and WAYLAND_DISPLAY unset)`
- **Per paste**, a line in `<workspace>/.yolo/launch.log`: time, file name, type read, size,
  dimensions. Never a byte of the image.
- **At session end**, after the terminal is restored, one line when anything happened:
  `image paste: 3 images pasted into .yolo/paste/ this session`, plus one line per refusal naming
  its next step ("an image of 61 MiB is over the 32 MiB cap; crop or scale it, then paste again").
  Nothing is printed mid-session: writing into a raw terminal under a full-screen agent corrupts
  its screen.

### 8.4 Failure paths

| Failure | What the user sees | Key |
| :--- | :--- | :--- |
| No reader at launch | the launch's "off" line | forwarded |
| Bracketed paste off | nothing (the program gets its key) | forwarded |
| Clipboard has text or nothing | the agent's own "no image" handling | forwarded |
| Read past the 2 s deadline, too large, undecodable, unsupported type | end-of-session line with the next step | forwarded |
| Store write fails (disk full, permissions) | end-of-session line naming the store path | forwarded |
| A second Ctrl+V while one is being read | handled after the first, in order | per its own result |

Every failure forwards the original key, so a broken paste arm degrades to today's behavior and can
never eat a keystroke without delivering an image.

## 9. Measurements needed

| ID | Measurement | Why |
| :--- | :--- | :--- |
| M1 | From a macos-user sandbox session, read the pasteboard (`pbpaste`, `osascript` for `«class PNGf»`). `TestMacosUserPasteboardProbe` in [`macosuserpasteboard_test.go`](../../integration/macosuserpasteboard_test.go) takes it on every macOS CI run: it logs one `MEASUREMENT` line per method and reports a host that cannot read its own pasteboard as inconclusive | Decides whether [OQ-PA1](#OQ-PA1)'s hole exists |
| M2 | For each common terminal (kitty, Ghostty, WezTerm, foot, Alacritty, GNOME Terminal, iTerm2, Terminal.app), with only an image on the clipboard: does Ctrl+V reach the program as one chunk, or does the terminal intercept it | [§5.1](#51-only-a-chunk-that-is-exactly-one-key) assumes one chunk |
| M3 | For each of the six agents, in a jail: does it turn bracketed paste on, and does it attach a bracketed paste of `/workspace/.yolo/paste/x.png` (agy and Copilot's details especially) | P1 rests on it |
| M4 | Per backend: can the jail read a file the host writes into `.yolo/paste/` (rootless podman's uid map, Apple Container's virtiofs, the macos-user account) | [§7.3](#73-the-paste-store) |
| M5 | Kitty CSI-u and modifyOtherKeys encodings of Ctrl+V arrive as one chunk | [§5.1](#51-only-a-chunk-that-is-exactly-one-key) |
| M6 | `wl-paste --list-types` and `xclip … TARGETS` for screenshots from the common tools; `«class PNGf»` from a macOS screenshot | [§7.1](#71-reading-the-host-clipboard)'s type order |

M3 and M4 need a real jail and a human at the terminal. M1 runs unattended, but a CI runner is not a Terminal launch by a person, so a human repeats it from a Terminal before [OQ-PA1](#OQ-PA1) is ruled on a "no". None is a synthetic load.

## 10. First build slice

Thinnest first, each step testable alone. Files are where the work lands, not a specification.

1. **Clipboard reader and paste store** — a new `internal/clipimage` package: the reader
   ([§7.1](#71-reading-the-host-clipboard)), the checks and re-encode
   ([§7.2](#72-checks-and-why-the-image-is-re-encoded)), the atomic store write and retention
   ([§7.3](#73-the-paste-store)). Tests with stub `wl-paste`/`xclip` scripts on PATH and fixture
   images, including an oversized header and a truncated PNG.
2. **`yolo paste`** — a host subcommand under `internal/cli` using step 1
   ([§6.4](#64-the-fallback-on-every-backend-yolo-paste)). This alone makes paste work on every
   backend.
3. **The Linux paste arm** — in `internal/ttyproxy`: the exact-chunk matcher beside
   `classifyInput`, the bracketed-paste tracker on the output path, and the arm in the pump, calling
   a callback the run pipeline supplies (the proxy does not import the reader). Tests feed chunks
   through the pump: a lone Ctrl+V with the bit on and off, Ctrl+V inside a larger chunk, a
   bracketed text paste holding `0x16`, a release event, a split `CSI ? 2004 h`.
4. **Wiring** — `internal/cli/run`: hand the callback and the in-jail workspace path to every
   session (fresh and attach), the launch disclosure line, the end-of-session summary, and the
   retention sweep in housekeeping. One integration test asserting the disclosure line, which fails
   if the call site is deleted.
5. **Later, separately**: the darwin port of the proxy's input path
   ([§6.2](#62-darwin-container-backends)), and the macos-user pasteboard deny per
   [OQ-PA1](#OQ-PA1).

No new `cmd/` binary, no loophole, no new dependency in `vendor/`.

## 11. Decision ledger

Mechanism choices made in this design, under the 2026-09-25 delegation of implementation questions.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| PA-D1 | The proxy types the saved image's in-jail path; no clipboard channel into the jail | 2026-10-09 | [§3](#3-the-recommendation-type-the-path-dont-open-a-channel) | — |
| PA-D2 | The armed clipboard loophole (B, B+X) and per-read approval (C) are rejected | 2026-10-09 | [§3.1](#31-alternatives-with-verdicts) | — |
| PA-D3 | Ctrl+V is consumed only for one exact key chunk, with bracketed paste on and an image present; else forwarded | 2026-10-09 | [§4](#4-the-chord-consumed-only-when-it-can-be-honored) | — |
| PA-D4 | Only Ctrl+V triggers; no configurable chord in v1 | 2026-10-09 | [§4](#4-the-chord-consumed-only-when-it-can-be-honored) | — |
| PA-D5 | The host decodes and re-encodes every image as PNG; PNG, JPEG and GIF accepted; 32 MiB and 40 MP caps | 2026-10-09 | [§7.2](#72-checks-and-why-the-image-is-re-encoded) | — |
| PA-D6 | Store at `<workspace>/.yolo/paste/`, atomic writes, 7-day and 200-file retention | 2026-10-09 | [§7.3](#73-the-paste-store) | — |
| PA-D7 | Nothing printed mid-session; per-paste lines go to `launch.log`, refusals to an end-of-session summary | 2026-10-09 | [§8.3](#83-disclosure) | — |
| PA-D8 | Every failure forwards the original key | 2026-10-09 | [§8.4](#84-failure-paths) | — |
| PA-D9 | `yolo paste` is the fallback on every backend and replaces the clipboard with the path | 2026-10-09 | [§6.4](#64-the-fallback-on-every-backend-yolo-paste) | — |
| PA-D10 | Darwin gets the arm by porting the proxy's input path, not by a darwin-only mechanism | 2026-10-09 | [§6.2](#62-darwin-container-backends) | — |

## 12. Open questions

1. 💬 **OQ-PA1: If M1 finds the macos-user pasteboard open, is it closed at once, or together with the darwin paste arm?**

   Closing it stops agents reading the user's whole clipboard, text included, and also stops
   native image paste on macos-user until the ported arm ships; `yolo paste` covers the gap.

   - **A — Close at once.** P3 holds on every backend from the next release; macos-user users paste
     images through `yolo paste` for a while.
   - **B — Close with the arm.** No interim regression; the hole stays open until the port lands.

   <!-- vantage: question id=OQ-PA1 leaning="Close at once: an open pasteboard is a standing read of copied passwords, and yolo paste covers images meanwhile." -->

   _Leaning:_ Close at once: an open pasteboard is a standing read of copied passwords, and yolo paste covers images meanwhile.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-PA2: Is consuming Ctrl+V in bash and vim, while an image is on the clipboard, acceptable?**

   The bracketed-paste gate cannot tell an agent from bash or vim, which also turn bracketed paste
   on ([§4](#4-the-chord-consumed-only-when-it-can-be-honored)).

   - **A — Yes, as designed.** One chord, every agent's own key; the cost is a visible, undoable
     path in a shell or editor, and only with an image on the clipboard.
   - **B — A yolo-only chord instead** (for example Ctrl+Shift+V where the terminal reports it).
     Nothing is ever taken from a program, but users must learn a key no agent documents, and many
     terminals bind it to text paste themselves.
   - **C — Both keys.** Ctrl+V as in A, plus a second chord; more surface for little gain.

   <!-- vantage: question id=OQ-PA2 leaning="A: the key every agent documents is the feature, and the cost needs an image on the clipboard and is undone in one keystroke." -->

   _Leaning:_ A: the key every agent documents is the feature, and the cost needs an image on the clipboard and is undone in one keystroke.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-PA3: Is the paste arm on by default, with a config key to turn it off?**

   Core activates nothing by default for packs; this is not a pack, and it adds no capability the
   user's keypress does not request.

   - **A — On by default**, with `image_paste: false` in config to turn it off, disclosed at every
     launch.
   - **B — Off by default**, opted into with `image_paste: true`. Most users would never learn it
     exists, since Ctrl+V on an image would still silently fail.

   <!-- vantage: question id=OQ-PA3 leaning="A: the keypress is the consent and the launch line discloses it; off by default leaves the broken behavior in place for everyone." -->

   _Leaning:_ A: the keypress is the consent and the launch line discloses it; off by default leaves the broken behavior in place for everyone.

   **Answer:**
   > _(empty — fill in when decided)_
