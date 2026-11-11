# Run a Host Command for the Agent

Sometimes an agent needs something only your machine can do: run a release script with your
own tools, inspect a host service, run `yolo check` on the host. With `yolo host-run` the agent
writes one line, you run it in a terminal on your host, and the command's output, errors and exit
status go straight back to the agent. You never copy the output back by hand.

## The line

```bash
yolo host-run [-C DIR] [--name LABEL] -- COMMAND [ARG...]
```

For example:

```bash
yolo host-run -C /home/you/code/widgets --name merge-42 -- gh pr merge 42 --squash
```

- **`-C DIR`** is where the command runs. yolo finds the project from there, the same way
  `yolo check` does. Leave it out when your terminal is already inside the project:
  `yolo host-run -- brew upgrade`.
- **`--name LABEL`** is optional. It lets the agent wait for exactly this run. A label is 1 to 40
  characters of `a-z`, `0-9`, `.`, `_` and `-`.
- **After `--`** comes the command, written as you would type it.

An agent learns this line from its briefing, and writes `-C` from `$YOLO_HOST_DIR`, the host's
path for the project. You can write the same line yourself, with no agent involved.

Inside a jail, typing a `yolo host-run` line runs nothing. It prints the line, with `-C` filled
in, ready for you to paste on the host.

## What you see on the host

Before anything runs, yolo prints what it will run, the program's full path, the directory, and
where the result goes. Then the command runs as you, with your terminal as its input, so a
password prompt works and Ctrl-C stops it. Its output shows in your terminal as it arrives. When
it finishes, the last line says what was delivered:

```text
yolo host-run: exit 0 after 3.2 s. Delivered 812 B stdout, 0 B stderr to run 20261023T143307Z-k4f2qa. Nothing to paste back.
```

yolo exits with the command's own exit status. Running the line is your approval: there is no
extra prompt.

## Getting the result in the jail

- **`yolo host-run wait --name LABEL`** waits for the run with that label, prints it, and exits 0.
  Without `--name`, it takes the next run that starts after it does. An agent starts it as a
  background task, so it wakes when the result arrives.
- **`yolo host-run show`** prints the newest run without waiting. `show RUN-ID` and
  `show --name LABEL` pick a particular one.

The printed result holds the command, its exit status and up to 16 KiB from the start and the end
of each stream. The command's own exit status is in the printed text; `wait` and `show` exit with
their own codes:

| Exit | Meaning |
| :--- | :--- |
| 0 | A result arrived and was printed, whatever the command's own exit |
| 64 | No project here (add `-C`), or `show` found no runs yet |
| 69 | The host refused the line, stopped without a result, or the jail cannot read the files |
| 75 | Nothing arrived within `--timeout` (default 100 minutes), or `show` found the run still running; run `wait` again |

## What gets refused

Each refusal runs nothing, exits 64 and says what to do instead. The refusal of a `gh` command
that prints your token reaches the agent too; every other refusal is printed for you alone.

- **Run inside a jail.** It prints the ready line instead of running.
- **A redirect or a pipe.** Your terminal must be stdin, stdout and stderr, so nothing hides the
  output from you. Run the line as written.
- **No project at or above the directory.** Add `-C` with the project's path.
- **A project where no jail has run yet.** Launch `yolo` there first.
- **Invisible or control characters in the command.** They could make what yolo prints differ
  from what runs. yolo names the word and the character, never the raw text.
- **A `gh` command that prints your GitHub token**, such as `gh auth token`.
- **Not your `PATH`.** No entry is refused: a project's own `.venv/bin`, a `bin/` inside the
  project, and your mise or direnv entries are where a program normally comes from. yolo prints the file it resolved as the disclosure's `program:` line, and
  it looks past its own folder of generated host programs (`~/.local/share/yolo-jail/bin`,
  including the wrapper folder `bin/wrap`), so `gh` runs your real `gh`, not a wrapper.
- **A link the jail planted where yolo writes.** yolo will not write through it.

## Where results live

Each run gets its own folder in the project, at `.yolo/host-run/<run id>/`, with `stdout`,
`stderr`, `request.json` and `result.json`, or `refused.json` for a refusal. Inside the jail that
is `/workspace/.yolo/host-run/`. yolo writes none of the command's output there until the command
has finished, so a secret on screen does not reach the jail before you can press Ctrl-C. Each stream keeps up to
16 MiB; anything beyond that was shown only in your terminal, and the file says so. The next run
removes folders older than 24 hours. Every run and refusal is also recorded on the host:
`yolo audit --set human-run` lists the runs, and `yolo audit --set refused` the refusals.

## What it does not do

- **It runs nothing on its own.** Only you running the line starts a command.
- **Full-screen programs do not work.** The output is a pipe, so there is no color, pager or
  full-screen display.
- **It does not restart the jail.** It can deliver the `yolo config check` after a config edit; the
  restart is still yours.
- **It cannot stop what your shell does first.** Your shell reads the line before yolo does, so
  start it with bare `yolo host-run`, with no `;`, `&&`, `|`, `>` or `$(` anywhere.
- **An older yolo on the host answers "unknown command".** Nothing runs; update yolo on the host.

## Where it works

It works the same with podman on Linux, Apple Container and `macos-user`, because the result is
plain files in the project folder the jail already shares. It has been tried on Linux with podman;
Apple Container and `macos-user` are not tried yet. If the jail cannot read the files on your
setup, `wait` exits 69 and says to copy the output from your terminal instead.

## See also

- [Approving What the Agent Asks For](approvals.md): a `git push` or `gh` write the jail is
  refused asks you through `yolo approve`, not through `host-run`; the refusal lists its answers,
  each run directly in a host terminal.
- [Your Own Host Service](host-services.md): a program that answers the jail without you running
  anything.
