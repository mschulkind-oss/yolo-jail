# CLI Reference

## CLI Commands

### The startup banner

Every `yolo` subcommand opens by writing one line to **stderr** before it does anything:

```console
$ yolo check
yolo-jail 0.8.0+881.ga6f61864 | linux/x86_64 | host
...
```

Version, platform, and which side you are on — `host`, or `in-jail` when the command was
typed inside a jail. Paste it with any bug report; `yolo check 2>&1 | pbcopy` captures it,
and so does any other redirect, because the line is not gated on a terminal.

Two things it deliberately does **not** do. It never touches stdout, so the
machine-readable commands (`config dump`, `describe --json`, `config drift`, `config-ref`)
are byte-for-byte what they always were. And it is absent from `yolo --version`, `yolo
--help`, and the hidden `yolo internal …` family, none of which is a command a bug report
quotes.

To turn it off — for a wrapper whose stderr is a contract, or a harness that diffs
stderr — set `YOLO_NO_BANNER` to any non-empty value:

```bash
YOLO_NO_BANNER=1 yolo config drift
```

Inside a jail, the version shown is the **host** launcher's, because that is what the jail
was built by; this is exactly why the line names the side.

### Color

Commands color what they print to a terminal and print plain text into a pipe or a file.
To turn color off everywhere, set `NO_COLOR` to any non-empty value — the
[NO_COLOR](https://no-color.org) convention:

```bash
NO_COLOR=1 yolo check
```

Every `yolo` command honors it, and so does the jail: a launch carries your `NO_COLOR` into
the jail alongside your terminal's `TERM`, so the jail's prompt, the lines a launch prints
while it provisions, and any tool inside the jail that follows the convention stay plain too.
An empty value (`NO_COLOR=`) counts as unset.

Each `yolo` that attaches to a running jail decides for its own session. Attach with
`NO_COLOR` set and that session is plain. Attach without it and that session is colored,
even if the jail was started with `NO_COLOR` set.

The red kitty tab or tmux pane border that marks a jail's terminal loses its red under
`NO_COLOR`. The `🔒 JAIL` label stays.

One exception to the terminal rule today: the provisioning lines a launch prints from inside
the jail are colored even when you redirect them. `NO_COLOR` turns those off as well.

### `yolo` — Start a Jail

```bash
yolo                       # Interactive shell
yolo -- claude             # Start Claude Code in YOLO mode
yolo -- copilot            # Start Copilot (--yolo auto-injected)
yolo -- agy                # Start Google Antigravity (--dangerously-skip-permissions auto-injected)
yolo -- bash -c "make"     # Run a specific command
```

**Options:**
- (removed) `--new` — use `yolo stop` followed by an ordinary launch instead
- `--network bridge|host` — Override network mode for this run
- `--timing` — Show detailed startup performance timing

### `yolo init` — Initialize a Project

```bash
cd ~/code/my-project
yolo init
```

Creates a `yolo-jail.jsonc` config with documented defaults and adds `.yolo/` to `.gitignore`.

### `yolo check` — Validate Everything

```bash
yolo check              # Full check including nix build
yolo check --no-build   # Quick check (skip nix build)
```

**Run this after every edit to `yolo-jail.jsonc`.** It validates:
- Container runtime availability
- Nix installation and flakes support
- Config file syntax and schema
- Entrypoint dry-run (shims, MCP, LSP generation)
- Nix image build (unless `--no-build`)

Inside a running jail, use `yolo check --no-build` for a fast preflight before asking for a restart.

### `yolo doctor` — Alias for Check

```bash
yolo doctor             # Same as yolo check
```

### `yolo ps` — List Running Jails

```bash
yolo ps
```

Shows container names, status, uptime, and workspace mappings.

### `yolo config-ref` — Full Configuration Reference

```bash
yolo config-ref
```

Prints the complete reference for all `yolo-jail.jsonc` fields with types, defaults, and examples.

### `yolo init-user-config` — Create User Defaults

```bash
yolo init-user-config
```

Creates `~/.config/yolo-jail/config.jsonc` with the same template as `yolo init`.

### `yolo capture` — Record a Vendor Installer, Once Per Machine

```bash
yolo capture claude
```

Some packs install a program by running a vendor's installer script — a URL whose contents run as
a shell script. There is nothing to pin there, because the installer *run* is the resolution, and
`~/.local` is a per-workspace directory, so every workspace downloads its own copy (claude's
versions directory is 1.2 GB).

`yolo capture <bin>` runs that installer ONCE, in a throwaway jail with an empty home, and stores
what it left behind as a content-addressed entry under
`~/.local/share/yolo-jail/captures/entries/<key>/`, with a file manifest and a receipt beside it.
Later jails put that entry in place instead of downloading anything.

`<bin>` must be a program one of your selected packs installs with `via: "installer"`; an
npm-declared program already names a registry version and needs no capture. Captures are
machine-local and are never shared between machines.

#### It usually happens by itself

**You do not normally have to run this command.** A launch checks, before it starts your jail,
whether the machine has ever recorded each `via: "installer"` program your selected packs install.
If one is missing, that launch captures it first and says so:

```
auto-capture  1 program never recorded on this machine: claude
  Each is installed once now, in a jail of its own, so this and every other workspace
  materialize it instead of downloading it. This launch pays one installer download
  per program. Set YOLO_NO_AUTO_CAPTURE=1 to skip.
```

That first launch is slower by roughly one installer download (~205 MiB for claude). Every launch
after it, in this workspace or any other on the machine, is not.

- **It never fails your launch.** A capture that cannot run — no network, a stale installer URL, a
  full disk — warns once, names the program, and gets out of the way; that program then installs
  the ordinary way, one download per workspace, and the next launch retries the capture.
- **`YOLO_NO_AUTO_CAPTURE=1`** turns it off for a launch (any non-empty value works). Use it on a
  metered connection or when you want the launch to start now. `yolo capture <bin>` still works.
- **It is container backends only.** On `macos-user` nothing yet materializes a capture, so a
  launch there captures nothing; run `yolo capture` explicitly if you want the record.
- **Old captures are reclaimed by `yolo prune`.** The store keeps the newest recording of each
  program per platform; anything an install would no longer choose is superseded, and `yolo prune`
  reports it (dry run) or `yolo prune --apply` removes it. Each superseded entry's manifest is kept
  — kilobytes — so a record of what that version contained survives its bytes.

### `yolo programs` — What Is Installed, and What Nothing Asks For Any More

**Run this one INSIDE a jail.** The programs it is about live in that jail's per-workspace home,
and the declarations it compares them against come from its staged pack tree; on the host there is
neither, and the command says so instead of guessing.

```bash
yolo programs ls                     # the report: orphans + record drift
yolo programs remove                 # what removing them would unlink — a DRY RUN
yolo programs remove --apply         # actually remove them
yolo programs remove pyright --apply # ...or just one, by name
```

Dropping a pack removes its launcher and its staged files. **It has never removed the program it
installed** — so a jail is the union of every pack it has ever selected, and an npm package or a
`~/.local/bin` binary can outlive the config line that asked for it by months. `yolo programs ls`
names those *orphans* with their sizes (measured 448.6 MB in this repo's own jail), plus anything
the install receipts now disagree with the disk about. Every boot COUNTS the
same orphans, in one `boot catalog:` line, and writes the list itself to
`<workspace>/.yolo/boot.log`.

Removal is deliberately awkward, in three ways:

- **`remove` is a dry run** unless you pass `--apply`. It prints every path — the package
  directory, the `bin/` symlinks pointing into it, the `@scope` directory it would empty — so what
  you read is exactly what would go.
- **Only an orphan can be removed.** Naming a program a pack or MCP preset still declares is an
  error, not a no-op: drop the declaration first.
- **`~/.local/bin` is also where you may have put things.** yolo cannot tell a tool you installed
  by hand from a dropped pack's leftovers — both are "installed and undeclared". Read the dry run.

To make each launch do it for you, put this in your **user** config
(`~/.config/yolo-jail/config.jsonc` — a workspace config cannot set it):

```jsonc
{
  "programs": { "autoprune": true }
}
```

It is **off by default**, it runs with nobody present, and it is not undoable. `yolo programs ls`
first.

---
