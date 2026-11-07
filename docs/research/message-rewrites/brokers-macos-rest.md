# Message rewrites: Brokers, macOS, storage and everything else

Scope: every non-test `.go` file under `internal/` outside the packages other workers own. The ones covered closely are `ghbroker`, `gitbroker`, `gitremote`, `approvalwait`, `brokerstore`, `brokercore`, `callbroker`, `macosuser`, `prune`, `image`, `runtime`, `containerbuilder`, `nixdiag`, `selfupdate`, `reporoot`, `awsauth`/`awsauthdaemon`, `openaiauthhost`, `mcpauth`/`mcpauthdaemon`, `capture`, `hostrun`, `nixroots`, `durable`, `resourcegroups`, `launchservice`, `journald`, `hostprocesses`, `loopholegrant` and `setup`. A second pass read the smaller packages in context: `brokerscope`, `callbrokerinput`, `callbrokeridentity`, `awscredadapter`, `openaiauthdaemon`, `openaiauthadapter`, `openauthclient`, `oauthterminator`, `packoverlay`, `basehome`, `ctxinject`, `execmonitor`, `clipimage`, `circleciclassify`, `installerbody` and `changetiming`. It also rendered the macOS certificate-trust disclosure and checked every `yolo config` key this doc proposes against the key registry (`internal/config/schema.go`). Messages read: about 1,850 (approximate). Rewrites proposed: about 90, plus 9 patterns that cover about 200 more sites.

Most of this partition is already in good shape. The broker refusals and approval menus (`approvalwait/handoff.go`, `approvalwait/wait.go`, `hostrun`, `setup`) mostly follow the rules already. The problems cluster in four places:

- `callbroker`, the newest code, which talks in headline shorthand.
- The project words "working set", "ceiling", "grant" and "Allow once" in the GitHub and git broker messages.
- Old config file names and design-document IDs left in macOS and auth messages.
- `prune` and `image` report lines that name nix internals.

## Patterns

### P1. "Working set" reaches the user and agent undefined

"Working set" is a project term (rule 6). It means the branches, PRs and issues this workspace may write to without asking each time. Where a message must name it, define it in the same sentence or describe the effect instead.

- **Before:** `PR o/r#12 joined this workspace's working set (w-…): comments, edits, reviews and closing it now run without asking; a merge still asks`
- **After:** `PR o/r#12 (w-…) is now one this workspace may write to without asking: comments, edits, reviews and closing it run unasked. A merge still needs the human's approval.`
- **Sites:**
  - `internal/ghbroker/workingset.go:284`, `:291`, `:351`, `:358`, `:366`, `:374`
  - `internal/ghbroker/evaluate.go:134`, `:139`
  - `internal/gitbroker/policy.go:199`, `:258`, `:261`, `:378`, `:393`
  - `internal/gitbroker/push.go:289`
  - `internal/brokerstore/workingcommit.go:219`
  - `internal/brokerstore/working.go:245`
  - `internal/approvalwait/wait.go:257`
  - `internal/callbroker/boundwrite.go:72`
  - `internal/callbroker/timed.go:31`

### P2. "X asks" used to mean "X needs the human's approval"

"This write asks" and "which asks" use a verb with no object, so the reader cannot tell who asks whom (rules 3 and 4). Say "needs the human's approval".

- **Before:** `github-broker: PR o/r#3 is merged, so it has left this workspace's working set; this write asks`
- **After:** `github-broker: PR o/r#3 is merged, so writes to it now need the human's approval again.`
- **Sites:**
  - `internal/ghbroker/workingset.go:284`, `:291`, `:352`, `:359`
  - `internal/ghbroker/evaluate.go:134`, `:139`
  - `internal/ghbroker/approverun.go:151`, `:155`, `:159`
  - `internal/ghbroker/runs.go:178`, `:181`, `:188`, `:200`, `:208`, `:214`, `:219`, `:223`
  - `internal/ghbroker/menu.go:43`, `:47`

### P3. "Allow once" used as a noun

"Its Allow once outlasts a broker restart" and "its Allow once is already used" treat a button label as a thing (rule 4). Say what the human did: "the human's one-time approval".

- **Before:** `%s was allowed and its Allow once is already used: the command it allowed already ran. Do not run it again; report it.`
- **After:** `The human allowed %s once, and that approval is used up: the command already ran. Do not run it again; report it.`
- **Sites:**
  - `internal/approvalwait/wait.go:134`
  - `internal/ghbroker/menu.go:36`
  - `internal/gitbroker/approval.go:43`, `:66`
  - `internal/gitbroker/push.go:290`
  - `internal/callbroker/http_write.go:227`, `:233`, `:236`
  - `internal/callbroker/boundwrite.go:24`
  - `internal/callbroker/httpfront.go:49` ("a spent one-time allowance")

### P4. Internal-state refusals with "relaunch the jail" and no account of what happened

These name internal objects the agent has never met: "host authority", "broker lifetime", "classification", "launch identity", "selected input", "generation", "seal" and "front" (rules 1, 5 and 6). They are all mid-flight consistency failures. One plain sentence covers each family: say what the user sees, then the step.

- **Before:** `request holder is not this live host launch` / `current classification differs; refile the command` / `broker lifetime lease is not live; relaunch the jail`
- **After:** `The broker that filed this request has stopped or restarted, so nothing ran. Run the same command again; if it fails the same way, restart the jail.` / `What this command would change is different now from when the request was filed, so nothing ran. Run the same command again to file a new request.`
- **Sites:**
  - `internal/ghbroker/authority.go:46`, `:121`, `:131`, `:138`, `:147`
  - `internal/ghbroker/workingset.go:189`
  - `internal/ghbroker/access.go:197`, `:204`, `:239`
  - `internal/ghbroker/window.go:56`, `:63`, `:76`, `:102`
  - `internal/gitbroker/approvalauthority.go:119`, `:122`, `:132`, `:158`
  - `internal/gitbroker/approval.go:180`, `:185`
  - `internal/brokercore/authority.go:94`, `:101`, `:104`
  - `internal/brokerstore/store.go:274`, `:317`, `:1334`
  - `internal/callbroker/authority.go:35` through `:204`, and `internal/callbroker/timed.go:58`–`:90` (about 30 sites)
  - `internal/callbroker/http_seal.go:25`–`:223`
  - `internal/callbroker/handler.go:56`–`:93`
  - `internal/callbroker/daemon.go:38`–`:148`
  - `internal/loopholegrant/live.go:92`, `:99`

### P5. `callbroker` headline shorthand: noun stacks with no subject or verb

Some examples: "selected HTTP prepared identity is invalid", "HTTP native ancestry exceeds three lookup rounds", "canonical body reference does not endorse the original body presence and digest" and "Allow once expired". These break rules 3, 5 and 6 ("cell", "canonical", "native", "front", "generation", "ceiling" and "the gate"). Many also end in "check the selected pack and retry". Retrying cannot fix a bug in a pack, so that next step is wrong. The useful step is to report the problem to the pack's author.

- **Before:** `HTTP native resource has no declared GET mapping; fix the selected pack and retry`
- **After:** `The <pack> pack does not say how to look up this resource, so nothing ran. This is a bug in the pack: report it to the pack's author.`
- **Before:** `call broker %s classifier failed: %w; check the selected pack and retry`
- **After:** `The %s pack could not tell what this command would change (%w), so nothing ran. Report this to the pack's author; running it again will fail the same way.`
- **Sites (about 120):**
  - `internal/callbroker/classifier.go:130`–`:391`
  - `internal/callbroker/http.go:118`–`:793`
  - `internal/callbroker/http_native.go:32`–`:240`
  - `internal/callbroker/handler.go:156`–`:437`
  - `internal/callbroker/decision.go:43`, `:52`
  - `internal/callbroker/http_handler.go:40`–`:279`
  - `internal/callbroker/http_executor.go:89`–`:260`
  - `internal/callbroker/executor.go:35`–`:167`
  - `internal/callbroker/http_schema.go:34`–`:110`
  - `internal/callbroker/httpfront.go:148`–`:792`

The "update the selected client" lines (`handler.go:362`–`:412`, `http_handler.go:197`–`:242`) mean that the jail's forwarder and the host disagree on the protocol. The fix is "Restart the jail so its tools match the host's yolo", not an update to a client the user never chose.

### P6. Old config file names, and advice to hand-edit a file

Config is TOML now, and repo policy says advice names a command, not a file to edit (`AGENTS.md`, "Advice names a verb"). These sites still say `yolo-jail.jsonc` or `config.jsonc`, or ask for a JSON fragment to be pasted in.

- **Before:** `Raise resources.memory in yolo-jail.jsonc, or remove it, if the session needs more.`
- **After:** `If the session needs more, raise the limit with yolo config set machine.resources.memory 16g, or remove it with yolo config unset machine.resources.memory.`
- **Key check (settled):** the readable key is `machine.resources.memory`, a String (`internal/config/schema.go:201`), and validation wants a size such as `8g` or `512m` (`internal/config/validate.go:1675`), not a byte count. `yolo config unset` removes it, since it is a plain scalar. Both verbs edit the project config by default; add `--user` when the limit is set there.
- **Sites:**
  - `internal/macosuser/sessionguard.go:381`, `:430`, `:443`
  - `internal/macosuser/orchestrator.go:1796`
  - `internal/awsauth/settings.go:180`
  - `internal/macosuser/cagodebug.go:151` ("Remove that setting from env_sources"; in TOML the variable is `env.set.GODEBUG`)
  - `internal/macosuser/cabundle.go:615` (JSON `{"SSL_CERT_FILE": …}` in an `env_sources` hint)
  - `internal/hostprocesses/hostprocessescmd.go:56` (seen only by someone starting the daemon by hand; low priority)

### P7. Design-document IDs and file paths in printed text

Rule 1 and rule 6.

- **Before:** `…whose part yolo's managed codex launch plays at the host with an adapter of its own (docs/design/host-notch-services.md HS-D20), and that launch did not start…`
- **After:** see the entry under [OpenAI login on the host](#openai-login-on-the-host).
- **Sites:**
  - `internal/openaiauthhost/host.go:335`
  - `internal/macosuser/seatbeltcapture.go:237` ("SeatbeltSealedCaptureProfile is the one to use", a Go symbol)
  - `internal/capture/store.go:279` ("admission is an os.Rename … (use Store.Stage)")
  - `internal/capture/inner.go:418` ("(Store.Stage)")
  - `internal/runtime/machineshares.go:353` ("See userguide/guides/macos.md". Point at the published docs URL instead, since a Homebrew user has no checkout.)

### P8. Em-dash fragments in `capture` errors

`program capture: X — fix the selected recipe` puts a fragment before the dash and an order with no object after it (rules 3 and 6: "selected recipe", "sealed Linux runtime", "producer", "surface").

- **Before:** `program capture: dangling link %q — fix the selected recipe`
- **After:** `yolo could not capture this program: its install left the link %q pointing at nothing. Fix the pack's install steps, then run it again.`
- **Sites:**
  - `internal/capture/artifacts.go:18`–`:181`
  - `internal/capture/artifactwalk_linux.go:36`–`:188`
  - `internal/capture/programinputs.go:28`, `:32`
  - `internal/capture/supervisor_linux.go:51`, `:112`, `:116`, `:211`
  - `internal/capture/supervisor_other.go:15`–`:26`
  - `internal/capture/inner.go:372`

### P9. `resourcegroups` errors are noun stacks that end in a blanket "run yolo check --no-build"

Some examples: "selected unit is not a delegatable scope or service", "child finite I/O ceiling missing at required start" and "assigned I/O split changed under lock" (rules 3, 5 and 6).

- **Before:** `selected group is not on cgroup v2; run yolo check --no-build`
- **After:** `The resource group this project uses is not on cgroup v2, so yolo cannot limit the jail's disk I/O. Run yolo check --no-build to see which setting to change.`
- **Sites:**
  - `internal/resourcegroups/pinnedio_linux.go:50`–`:431`
  - `internal/resourcegroups/pinned_linux.go:42`–`:190`
  - `internal/resourcegroups/resolve.go:20`–`:148`
  - `internal/resourcegroups/iomax.go:146`–`:155`
  - `internal/resourcegroups/authority_other.go:10`

---

## GitHub and git approval (what an agent meets when a write needs the human)

### Pending GitHub write: the lead line above the menu
- **Where:** `internal/ghbroker/menu.go:36`. The agent sees this when a `gh` write files a request and the human has not answered yet.
- **Before:**
  ```text
  r-… remains pending; nothing ran. Its Allow once outlasts a broker or jail restart. A merge always asks, per PR.
  ```
- **After:**
  ```text
  Request r-… is waiting for the human; nothing ran. If they allow it once, that approval still works after the jail or broker restarts. Every merge needs its own approval.
  ```
- **Why:** 3, 4 ("Allow once" as a noun, "always asks").

### Pending GitHub write: runs-grant clauses
- **Where:** `internal/ghbroker/menu.go:43` and `:45`. These are added to the lead line for `gh workflow run` and `gh run rerun`.
- **Before:**
  ```text
  A runs grant covers a run on the default branch only; a run on any other ref still asks.
  ```
- **After:**
  ```text
  A lasting permission to start workflow runs covers the default branch only. A run on any other branch or tag still needs the human's approval.
  ```
- **Why:** 5 ("runs grant"), 6, P2.

### Approved, but the 15-minute window closed
- **Where:** `internal/ghbroker/menu.go:60`, with the same text at `internal/gitbroker/approval.go:51`. The agent sees this when it runs the command again after the one-time approval has expired.
- **Before:**
  ```text
  the host allowed r-…, but its 15-minute window closed before this command ran, so it covers nothing now and no new request was filed; nothing ran. Tell the human, and only if they still want it, run the same command again: that files a new request for them to answer
  ```
- **After:**
  ```text
  The human allowed r-…, but that approval lasts 15 minutes and expired before this command ran. Nothing ran, and no new request was filed. Tell the human. If they still want it, run the same command again to file a new request.
  ```
- **Why:** 7.

### Approved, but no grant found
- **Where:** `internal/ghbroker/window.go:241`. The agent sees this when the human allowed a request but the broker cannot find the approval.
- **Before:**
  ```text
  the host allowed r-…, but this broker finds no grant or working-set entry of that answer covering this command (revoked or expired since, or recorded by a newer yolo than this running broker); nothing ran. Next: run the same command again, which files a new request, and ask the human to run `yolo approve <its id>` on the host; if this repeats right after an answer, relaunch the jail so its host broker matches the host's yolo
  ```
- **After:**
  ```text
  The human allowed r-…, but the approval no longer covers this command: it was revoked or has expired. Nothing ran. Run the same command again to file a new request, and ask the human to run `yolo approve <its id>` on the host. If this happens again right after they answer, restart the jail so it runs the host's current yolo.
  ```
- **Why:** 6 (working set), 7, 8.

### Too many pending requests
- **Where:** `internal/brokerstore/limits.go:14`, which `internal/ghbroker/window.go:193` extends.
- **Before:**
  ```text
  pending request limit (3) reached for this jail: r-a, r-b, r-c; nothing ran. `yolo wait` lists them; ask the human to answer them with `yolo approve` on the host before retrying
  ```
- **After:**
  ```text
  This jail already has 3 requests waiting for the human (r-a, r-b, r-c), so no new one was filed and nothing ran. Ask the human to answer them with `yolo approve` on the host, then run this command again.
  ```
- **Why:** 3, 5.

### Denied, so the broker files nothing for a while
- **Where:** `internal/brokerstore/limits.go:25`, which `internal/ghbroker/window.go:200` extends.
- **Before:**
  ```text
  command denied by r-…; quiet until 2026-11-07T15:04:05Z; nothing ran and no new notification was filed; ask the host user before retrying
  ```
- **After:**
  ```text
  The human denied this same command as r-…, so the broker will not ask again until 2026-11-07T15:04:05Z. Nothing ran. Ask the human before you try it again.
  ```
- **Why:** 3, 5 ("quiet until").

### Request filing failed
- **Where:** `internal/ghbroker/window.go:202`, with near-copies at `:76`, `:114` and `:161`.
- **Before:**
  ```text
  request filing failed: <err>; repair the host policy/audit and retry
  ```
- **After:**
  ```text
  The broker could not file a request for the human (<err>), so nothing ran. Ask the human to run `yolo check` on the host, which names what is wrong, then run this command again.
  ```
- **Why:** 3, 5 ("policy/audit"). The old step names nothing the agent can do.

### Notice delivery failed
- **Where:** `internal/ghbroker/window.go:206`.
- **Before:**
  ```text
  r-… remains pending; delivery failed; run `yolo approve r-…` on the host
  ```
- **After:**
  ```text
  Request r-… is waiting, but the human was not notified. Ask them to run `yolo approve r-…` on the host.
  ```
- **Why:** 3 ("delivery failed" has no subject); the old line gives the agent an instruction meant for the human.

### Push refused: the general explanation
- **Where:** `internal/gitbroker/push.go:279`. The agent sees this when a `git push` is refused and no request was filed.
- **Before:**
  ```text
  this push was refused whole, so nothing reached <dest> and the remote is unchanged; each ref's reason is on its `! [remote rejected]` line. Unasked, the broker pushes only a new branch under a create-branch grant and the branches in this workspace's working set: those it created that way or under an Allow once, and those a branch grant in the workspace config declares (`yolo approve` on the host lists both). Any other branch or tag asks for approval, but nothing approves a ref outside refs/heads/ and refs/tags/, nor a push that holds one. Push that from the host instead (`git push` in this workspace there), where `yolo audit --service git` shows what was refused.
  ```
- **After:**
  ```text
  Nothing was pushed to <dest>; each ref's reason is on its `! [remote rejected]` line. Without asking, the broker pushes only new branches (when the project allows that) and branches this workspace already owns. Any other branch or tag needs the human's approval. Refs outside refs/heads/ and refs/tags/ can only be pushed from the host: run `git push` there, in this workspace.
  ```
- **Why:** 5, 6 (working set, create-branch grant), 7, 8. The `yolo audit` clause is for the human, not for this reader.

### Push needs approval
- **Where:** `internal/gitbroker/approval.go:43`.
- **Before:**
  ```text
   It needs approval. Allow once covers exactly this push, once, within 15 minutes; a different push asks again.
  ```
- **After:**
  ```text
   It needs the human's approval. If they allow it once, the approval covers this exact push for 15 minutes; any other push needs a new approval.
  ```
- **Why:** P2, P3.

### Grant not in effect, three variants
- **Where:** `internal/gitbroker/policy.go:169`, `:171` and `:173`. The agent sees these when the project asks for a branch or tag grant that the user's config does not allow.
- **Before (`:171`):**
  ```text
  this workspace's branch grant on o/r is not in effect: the host user config's canonical `allow.git` does not authorize it under the selected broker's declared capabilities. To let the broker push to the branches it declares, run `yolo allow …` on the host, then push again; the broker rereads that config on each push, or push from the host.
  ```
- **After:**
  ```text
  This project asks to push to its declared branches of o/r without approval, but your user config does not allow that. To allow it, run `yolo allow …` on the host, then push again. Or push from the host.
  ```
- **Before (`:169`):**
  ```text
  …is not in effect: the host user config's canonical `allow.git` write authority could not be validated, so the broker covers no such write. Repair `allow.git.always` or `allow.git.on_request` on the host…
  ```
- **After:**
  ```text
  This project's branch permission on o/r does not apply right now: yolo could not read `allow.git` in your user config. Run `yolo check` on the host to see what is wrong with it, then push again. Or push from the host.
  ```
- **Why:** 5, 6 ("canonical", "write authority", "declared capabilities"), 8 ("rereads that config on each push").

### Ref not covered
- **Where:** `internal/gitbroker/policy.go:258`.
- **Before:**
  ```text
  refs/heads/x: not in this workspace's working set: the broker did not create it, and no branch grant declares it; the paths that cover it are listed below.
  ```
- **After:**
  ```text
  refs/heads/x: this workspace did not create this branch, and the project does not list it, so pushing it needs the human's approval. The ways to allow it are listed below.
  ```
- **Why:** 3, 6.

### Dead `--add` refusals (git and GitHub)
- **Where:**
  - `internal/gitbroker/approvalauthority.go:210` and `:216`
  - `internal/ghbroker/approvalstore.go:53`, `:59` and `:72`
  - `internal/callbroker/timed.go:31`
  - `internal/brokerstore/store.go:1123` ("choose Add, …")
- **Before:**
  ```text
  --add is not available for a merge: a merge asks per PR, even for a PR in the working set. Next: …
  ```
- **After:** Delete these branches, or reword them without "--add". See [Wrong today](#wrong-today).
- **Why:** they name a flag that `yolo approve` now refuses.

### `yolo approve --for` beyond what the request allows
- **Where:** `internal/brokerstore/store.go:1033`, `:1038`, `:1045` and `:1144`. The human sees these when they answer with a longer `--for`, a session or more uses than the request allows.
- **Before:**
  ```text
  duration exceeds this set's policy ceiling
  ```
- **After:**
  ```text
  That is longer than this kind of request may be allowed for (at most %s). Run `yolo approve <id> --for %s`, or `yolo approve <id>` to allow it once.
  ```
- **Why:** 3, 6 ("set", "policy ceiling"); there was no next step. The `:1139` variant already does this well; copy its shape.

### Answer a request with no hook
- **Where:** `internal/brokerstore/store.go:1107`.
- **Before:**
  ```text
  this source's requests take Allow once and Deny only
  ```
- **After:**
  ```text
  This kind of request can only be allowed once or denied: run `yolo approve <id>` or `yolo approve <id> --deny`.
  ```
- **Why:** 6 ("source"), P3; no next step.

### `yolo wait` while another waiter runs
- **Where:** `internal/approvalwait/wait.go:86`.
- **Before:**
  ```text
  another `yolo wait r-…` is already waiting on this request; its exit wakes whoever started it, so this one stops. Do not retry the command for this one.
  ```
- **After:**
  ```text
  Another `yolo wait r-…` is already waiting for this request, so this one stops. Rely on that one, and do not run the command again because this one stopped.
  ```
- **Why:** 8.

### `yolo wait` list footer
- **Where:** `internal/approvalwait/wait.go:257`.
- **Before:**
  ```text
  One allowed for a time or added to the working set waits on nothing: a command it covers runs without asking.
  ```
- **After:**
  ```text
  A request allowed for a period of time needs nothing more from you: commands it covers run without asking.
  ```
- **Why:** P1. "Added to the working set" is a removed answer (see `--add` above).

## GitHub from the jail (`gh`)

### Repository outside scope: how to widen it
- **Where:** `internal/ghbroker/scope.go:155`.
- **Before:**
  ```text
  A repository joins this jail's scope only when the user approves it on the host. To ask for o/r, run `yolo request github repos o/r`, which adds it to the `repos` list in this project's config. Then run `yolo check --no-build`, and ask the user to run `yolo config apply` on the host, in this workspace, and approve the repository scope there; this jail's running broker takes it at its next call. Nothing sent through gh adds a repository.
  ```
- **After:**
  ```text
  Only the user can let this jail reach another repository. To ask for o/r, run `yolo request github repos o/r`, then `yolo check --no-build`. Then ask the user to run `yolo config apply` on the host, in this workspace, and approve it. The jail can use it right after that.
  ```
- **Why:** 7, 8.

### Unknown flag
- **Where:** `internal/ghbroker/parse.go:195`.
- **Before:**
  ```text
  flag --foo is not one `gh pr view` has in the gh version this broker was built against, and a flag the broker cannot name is refused
  ```
- **After:**
  ```text
  `gh pr view` has no flag --foo in the gh version yolo knows, so the command was refused. Check the spelling, or leave the flag out.
  ```
- **Why:** 8; no next step.

### Flag before the subcommand
- **Where:** `internal/ghbroker/parse.go:114`. Already fine; skip. It is listed only to show that it was read.

### Write outcome when the item was created but not recorded
- **Where:** `internal/ghbroker/workingset.go:358`.
- **Before:**
  ```text
  PR o/r#5 was created, but could not be recorded in this workspace's working set (<err>); the next write to it asks
  ```
- **After:**
  ```text
  PR o/r#5 was created, but yolo could not record it as this workspace's (<err>). The next write to it will need the human's approval.
  ```
- **Why:** P1, P2.

### Approve-run hint
- **Where:** `internal/ghbroker/approverun.go:136`.
- **Before:**
  ```text
   A lasting right to approve run 123, and every run the same actor triggers from o/r itself, is an approve-run grant naming that actor's login (`yolo request github approve_runs <actor>`, …, then `yolo config apply` on the host); a run from a fork still asks.
  ```
- **After:**
  ```text
   To let the agent approve this and later runs that the same account starts in o/r, run `yolo request github approve_runs <actor>`, then have the human run `yolo config apply` on the host. Runs from forks still need the human's approval.
  ```
- **Why:** 4 ("a lasting right … is a grant"), 5, 7.

### Broker endpoint missing
- **Where:** `internal/ghbroker/forward.go:60`, with a twin at `internal/gitremote/helper.go:144`.
- **Before:**
  ```text
  this jail has no github-broker endpoint, so `gh` cannot reach the host's GitHub login: the broker
    did not start for this launch. It runs whenever the `github` pack is selected; run `yolo check` on
    the host, in '/x', to see why, then start a fresh jail. Apple Container runs no host
    services, so there it never starts. …
  ```
- **After:**
  ```text
  `gh` cannot reach the host's GitHub login, because the GitHub broker did not start with this jail.
    Ask the human to run `yolo check` on the host, in '/x', which says why, then start a new jail.
    On Apple Container the broker never runs. The image's own gh still works by its full path, but it has no GitHub login.
  ```
- **Why:** 6 ("endpoint", "pack is selected"), 7.

### Audit failure on a write
- **Where:** `internal/ghbroker/workingset.go:212`.
- **Before:**
  ```text
  authorization or required audit failed: <err>; nothing ran; run the command again
  ```
- **After:**
  ```text
  The broker could not check or record this write (<err>), so nothing ran. Run the command again; if it fails again, ask the human to run `yolo check` on the host.
  ```
- **Why:** 3; the step did not cover the case where the failure repeats.

### Host policy marks writes as standing
- **Where:** `internal/ghbroker/authority.go:98`.
- **Before:**
  ```text
  host brokered.github.sets makes GitHub writes standing; remove the read-write entry to require Allow once before retrying
  ```
- **After:**
  ```text
  Your host config lets GitHub writes run without asking, which yolo no longer allows, so nothing ran. Ask the human to remove the read-write entry from allow.github.sets in the user config (~/.config/yolo-jail/config.toml), then run the command again.
  ```
- **Why:** 4 ("GitHub writes"), 6 ("standing"), P3. The original names `brokered.github.sets`, the legacy JSONC spelling; a TOML user config holds the same list as `allow.github.sets` (`internal/config/brokered.go`, `brokeredSetsRaw`, reads both).
- **Key check (settled):** no verb can do this today. `allow.github.sets` matches the registry row `allow.*.*` (type Any), and `yolo config unset` refuses Any, Object and List keys ("this key has no scalar removal policy", `internal/cli/configeditverbs.go`, `configEditUnsetMutation`). So the rewrite has to name the file. See Wrong today.

### No permission set admits the command
- **Where:** `internal/ghbroker/authority.go:127`.
- **Before:**
  ```text
  no host permission set admits this command; configure its set on the host
  ```
- **After:**
  ```text
  Your host config defines its own GitHub permission sets (allow.github.sets), and none of them allows this command, so nothing ran. Ask the human to add a set that allows it to the user config, or to remove allow.github.sets to go back to yolo's default sets.
  ```
- **Why:** 6; the step was vague.
- **Settled:** `yolo allow github always read` would be wrong here. The read grant is checked before this line (`internal/ghbroker/authority.go:65`), and with no `sets` key the broker uses its defaults (read-only standing, read-write windowed), which cover every command. So this line prints only when the user wrote their own `sets` and none of them admits the command.

## `callbroker` (commands an agent runs through a pack's host CLI or HTTP broker)

P4 and P5 cover the bulk. These are the ones whose next step is wrong or uses internal words:

### Writes not active yet
- **Where:** `internal/callbroker/handler.go:207` and `:210`, and `internal/callbroker/decision.go:83`.
- **Before:**
  ```text
  pack approval effects are not active — Wait for pack broker approval policy and host activation before retrying
  Pack requests cannot be filed until the approval policy is settled; ask the human to perform this operation on the host
  Ask a human on the host to review pack broker access after its approval policy is settled
  ```
- **After:**
  ```text
  This pack cannot make changes through the host yet; yolo does not support that for packs. Nothing ran. Ask the human to do this on the host.
  ```
- **Why:** 5, 6. "Wait for … policy and host activation" is not a step anyone can take.

### Table refusal
- **Where:** `internal/callbroker/decision.go:80`.
- **Before:**
  ```text
  write is not permitted by its declared table — Change the request with `yolo request` and approve its scope at the gate
  ```
- **After:**
  ```text
  This pack does not allow this change, so nothing ran. If the project should allow it, run the `yolo request` command for it, then ask the human to run `yolo config apply` on the host.
  ```
- **Why:** 6 ("table", "the gate").

### Possible transmission
- **Where:** `internal/callbroker/httpfront.go:49`.
- **Before:**
  ```text
  The host may have transmitted this request; the remote effect or credential use may have occurred, and a spent one-time allowance is not refunded. Inspect remote state and approval before resubmitting.
  ```
- **After:**
  ```text
  The request may have reached the server before the connection failed, so it may have taken effect. Its one-time approval is used up. Check whether the change happened before you send it again.
  ```
- **Why:** 5, 7, P3.

### Unknown signer
- **Where:** `internal/callbroker/http_write.go:76` and `internal/callbroker/http_handler.go:157`.
- **Before:**
  ```text
  custom HTTP signer has no approved execution protocol — Use a core HTTP executor pack or wait for the approved signer contract
  ```
- **After:**
  ```text
  yolo cannot yet send requests that this pack signs in its own way, so nothing ran. Use a pack that lets yolo sign the request, or ask the human to do this on the host.
  ```
- **Why:** 5, 6.

### Forwarder: broker unavailable
- **Where:** `internal/callbroker/forward.go:20`.
- **Before:**
  ```text
  call broker unavailable; select the pack, run `yolo check` on the host, and relaunch
  ```
- **After:**
  ```text
  This command's host broker is not running in this jail. Ask the human to run `yolo check` on the host, which says why, then start a new jail.
  ```
- **Why:** 3, 6. The agent cannot select a pack.

## macOS (separate-account and run-as-you sessions)

### Memory guard, three messages
- **Where:** `internal/macosuser/sessionguard.go:381`, `:430` and `:443`.
- **Before (`:443`):**
  ```text
  yolo: this session holds 9.1 GB resident, over resources.memory (8 GB); stopping pid 123, its largest process (4 GB). Raise resources.memory in yolo-jail.jsonc, or remove it, if the session needs more.
  ```
- **After:**
  ```text
  yolo: this session is using 9.1 GB of memory, over its 8 GB limit (machine.resources.memory), so yolo is stopping pid 123, its largest process (4 GB). If the session needs more, run yolo config set machine.resources.memory 16g (any size, such as 12g), or yolo config unset machine.resources.memory.
  ```
- **Before (`:430`):**
  ```text
  yolo: the memory guard cannot read the process table (…), so resources.memory is not being checked; it will keep trying. Remove resources.memory from yolo-jail.jsonc to silence this.
  ```
- **After:**
  ```text
  yolo: cannot read the process list (…), so the memory limit is not enforced right now. yolo keeps trying. To stop this warning, remove the limit with yolo config unset machine.resources.memory.
  ```
- **Before (`:381`):**
  ```text
  yolo: the memory guard could not start <prog> (<err>). `yolo run --dry-run` prints the launch argv it was given; to launch without the guard, remove resources.memory from yolo-jail.jsonc.
  ```
- **After:**
  ```text
  yolo: could not start <prog> (<err>). `yolo run --dry-run` prints the command yolo tried to run. To launch without the memory limit, run yolo config unset machine.resources.memory.
  ```
- **Why:** P6 (old file name, file editing), 6 ("memory guard", "resident", "argv").

### Missing CA bundle in the tool profile
- **Where:** `internal/macosuser/cabundle.go:478`.
- **Before:**
  ```text
  Warning: the sandbox's tool profile has no CA bundle at <path>, so no TLS variable is set and each tool in the sandbox uses its own default roots. This is a yolo bug rather than a config error; `yolo run --dry-run` prints the plan.
  ```
- **After:**
  ```text
  Warning: yolo's own certificate bundle is missing (<path>), so each tool in the sandbox trusts only its built-in certificate authorities. This is a yolo bug. Please report it with the output of `yolo run --dry-run`.
  ```
- **Why:** 6 ("tool profile", "TLS variable"). The old line says it is a bug but not what to do about it.

### Keychain unreadable: name a CA through `env_sources`
- **Where:** `internal/macosuser/cabundle.go:615`.
- **Before:**
  ```text
  …A certificate authority your network needs can be named in env_sources: put a bundle holding it and the public roots in the workspace and set {"SSL_CERT_FILE": "<that file>"}, which the other four follow, and {"NODE_EXTRA_CA_CERTS": "<that file>"} for Node.
  ```
- **After:**
  ```text
  …If your network needs its own certificate authority, put a bundle with it and the public roots in the workspace. Then run `yolo config set env.set.SSL_CERT_FILE <that file>`, and the same for NODE_EXTRA_CA_CERTS if Node needs it, and launch again.
  ```
- **Why:** P6 (JSON fragment in a TOML world), 7.
- **Key check (settled):** `env_sources` is the legacy list of env *files*. In TOML, `env.sources` is that list, and a single static variable is `env.set.<NAME>`, a String (`internal/config/schema.go:224`–`:225`). So `yolo config set env.set.SSL_CERT_FILE <path>` is the one-line form, and `yolo config add env_sources …` would have been wrong. Owner to confirm: `env.set` is one of the "launch's own env layers" that `applyCATrust` reads as `follows`, so the other four variables follow it the way the original promises.

### Guest stage refusal: disable a loophole
- **Where:** `internal/macosuser/orchestrator.go:1796`.
- **Before:**
  ```text
  set `"loopholes": {"host-processes": {"enabled": false}}` in the workspace config (yolo-jail.jsonc) to go without yolo-ps
  ```
- **After:**
  ```text
  deselect the host-processes pack to launch without yolo-ps (`yolo pack ls` shows where it is listed; `yolo pack remove host-processes` removes it from your user config)
  ```
- **Why:** P6.
- **Key check (settled):** there is no `yolo config set` form. `loopholes` is not a key in the readable schema, and neither `host-processes` nor `serial` has an off switch: only five loopholes do (`internal/loopholedecl/offswitch.go`: claude-oauth-broker, openai-auth-broker, mcp-auth-broker, aws-auth, cgroup-delegate), so `allow.host-processes.off` is refused too (`AllowNudgeOffNoSwitch`). Deselecting the pack is the only way past. `yolo pack remove` edits only the user config (`internal/cli/packadd.go`). The same sentence is built for both guest clients, `yolo-ps` (host-processes) and `yolo-serial` (serial), in `guestWayPast`.

### Context-mount refusals: mechanism clauses
- **Where:** `internal/macosuser/ctxlinks.go:336`, `:400` and `:427`, and the "brief" strings at `:329`, `:356`, `:389`, `:403`, `:412`, `:419`, `:446`, `:454` and `:462`.
- **Before (`:427`):**
  ```text
  : with no mount namespace one path carries one mode here, so the mount would either be writable or make part of what the sandbox writes read-only
  ```
- **After:**
  ```text
  : on macOS a folder cannot be read-only in one place and writable in another, so yolo cannot add this mount here
  ```
- **Before (brief, `:403`):** `macos-user can't reach another volume yet`
- **After:** `on macOS, yolo cannot yet share a folder from another disk`
- **Why:** 6 ("macos-user" is a setup name the reader may not have met; "mount namespace"), 8.

### Capture bootstrap failed
- **Where:** `internal/macosuser/recordedrun.go:68`.
- **Before:**
  ```text
  capture bootstrap failed (exit 2) — the staging home has no generated launcher, so there is nothing for the capture to run. Aborting.
  ```
- **After:**
  ```text
  Preparing the install failed (exit 2), so yolo stopped before it ran the installer.
  ```
  (The existing follow-up lines already give the next step.)
- **Why:** 3, 6 ("bootstrap", "staging home", "generated launcher"), 8.

### Leftover pack copy
- **Where:** `internal/macosuser/packtreeproof.go:244`, `:270`, `:283` and `:292`.
- **Before (`:244`):**
  ```text
  Could not claim guest pack tree /x (<err>); do not run `yolo prune --apply` until this launch has ended.
  ```
- **After:**
  ```text
  yolo could not mark this session's copy of its packs (/x) as in use (<err>). Do not run `yolo prune --apply` until this session ends.
  ```
- **Why:** 6 ("claim", "guest pack tree").

### Font sharing through a link
- **Where:** `internal/macosuser/fontsconf.go:289`. Mostly fine; one clause is mechanism.
- **Before:**
  ```text
  Fonts in the sandbox: <dir> is a link to another folder, which the sandbox profile does not open, so it is not listed. Keep your fonts in the folder itself to …
  ```
- **After:**
  ```text
  Fonts in the sandbox: <dir> is a link to another folder, and the sandbox cannot follow it, so those fonts are not shared. Keep your fonts in the folder itself to …
  ```
- **Why:** 6 ("sandbox profile does not open").

## OpenAI login on the host

### Refresh URL not served
- **Where:** `internal/openaiauthhost/host.go:335`. This appears in the launch's list of variables that were not set.
- **Before:**
  ```text
  whose part yolo's managed codex launch plays at the host with an adapter of its own (docs/design/host-notch-services.md HS-D20), and that launch did not start: there is no OpenAI login, and this is not an interactive terminal to log in at (<remedy>)
  ```
- **After:**
  ```text
  because there is no OpenAI login, and this terminal is not interactive, so yolo cannot ask you to log in (<remedy>)
  ```
- **Why:** 1, 6 (design ID, "plays at the host", "adapter"), 8.

### Host credential socket unset
- **Where:** `internal/openaiauthhost/host.go:415`.
- **Before:**
  ```text
  the launch handed the doorway no host credential socket ($YOLO_… is unset)
  ```
- **After:**
  ```text
  the OpenAI login service was started without its connection to the host ($YOLO_… is unset); restart the session
  ```
- **Why:** 6 ("doorway"); no next step.

## AWS / Bedrock (`aws-auth`)

### No AWS profile configured
- **Where:** `internal/awsauth/settings.go:180`.
- **Before:**
  ```text
  no AWS profile is configured: set loopholes.aws-auth.settings.profile in your USER config (~/.config/yolo-jail/config.jsonc) to the profile this service should resolve. It is user-scope on purpose — a workspace yolo-jail.jsonc is a file the jail's own agent can rewrite
  ```
- **After:**
  ```text
  No AWS profile is set for Bedrock. Set it in your user config with yolo config set allow.aws-auth.profile '"<profile>"' --user. Only your user config can set this, so the agent in the jail cannot change it.
  ```
- **Why:** P6, 8.
- **Key check (settled):** `settingsScope(SettingProfile)` prints `loopholes.aws-auth.settings.profile` (`SettingsScope` at `internal/awsauth/settings.go:54`), the legacy JSONC path. The TOML key is `allow.aws-auth.profile`, and the daemon's own startup refusal already spells the command that way, quotes included (`internal/awsauthdaemon/main.go`, `safeSettingsRefusal`). The quotes matter: `allow.*.*` is an Any key, so the value is parsed as JSON.
- **Reach (settled):** the daemon's startup path never prints this `Message`; it maps the refusal's `Kind` through `safeSettingsRefusal`. The self-check does print it: `SelfCheck` writes `"FAIL: "+err.Error()` (`internal/awsauthdaemon/main.go:483`), and `ResolveRefusal.Error()` returns `Message`. `yolo check` shows that line. *Fix:* route `SelfCheck` through `safeSettingsRefusal` too, then this text can go.

### Session policy without a role
- **Where:** `internal/awsauth/settings.go:191`.
- **Before:**
  ```text
  <policy key> is set but <role key> is not: an inline session policy is an argument to AssumeRole, so without a role there is nothing to attach it to
  ```
- **After:**
  ```text
  <policy key> is set but <role key> is not, and a session policy only applies to a role. Set <role key>, or remove <policy key>.
  ```
- **Why:** 8; no next step.

### Conflicting narrowing
- **Where:** `internal/awsauth/settings.go:201`.
- **Before:**
  ```text
  <a> is true AND <b> is set — drop one: the role is a narrowing and <a> asks for none, so which wins is not something this service should guess
  ```
- **After:**
  ```text
  <a> is true and <b> is also set, and they contradict each other. Remove one of them.
  ```
- **Why:** 6 ("narrowing"), 8.

### Invalid policy document
- **Where:** `internal/awsauth/settings.go:210`.
- **Before:**
  ```text
  <key> is not a JSON policy document: <err> — STS would refuse every mint, so this is refused at spawn instead
  ```
- **After:**
  ```text
  <key> is not a valid JSON policy document (<err>). Fix it; until then, Bedrock requests fail.
  ```
- **Why:** 6 ("mint", "spawn"), 8.

### Launch credential warning
- **Where:** `internal/awsauthdaemon/launchcheck.go:149` and `:166`.
- **Before (`:166`):**
  ```text
  cannot mint a Bedrock credential for this launch: <what>. The launch continues; Bedrock requests fail until that is fixed. A fix made outside yolo is picked up with no relaunch; a change to loopholes.aws-auth.settings, which the service reads only when it starts, takes effect at the next launch, since each jail starts its own
  ```
- **After:**
  ```text
  yolo could not get a Bedrock credential (<what>). The jail starts anyway, but Bedrock requests fail until this is fixed. A fix outside yolo, such as `aws sso login`, takes effect right away. A change to the aws-auth settings in your user config (allow.aws-auth) takes effect at the next launch.
  ```
- **Why:** 6 ("mint"), 7, 8.

### Wire-bridge: no answer from the credential service
- **Where:** `internal/wirebridged/signing.go:129`.
- **Before:**
  ```text
  wire-bridge: the aws-auth credential service did not answer (<err>). Is the aws-auth loophole enabled for this jail? If your SSO session lapsed, run `aws sso login --profile <your profile>` on the host; the next request picks it up.
  ```
- **After:**
  ```text
  The AWS credential service did not answer (<err>). If your AWS login expired, run `aws sso login --profile <your profile>` on the host; the next request uses it. If that does not help, run `yolo check` on the host to see whether the service is running.
  ```
- **Why:** 6 ("loophole", "wire-bridge"), and a question is not a step.

## MCP sign-ins

### One-sign-in state unavailable
- **Where:** `internal/mcpauthdaemon/once.go:110`.
- **Before:**
  ```text
  one-sign-in state is unavailable; repair host state permissions and relaunch: <err>
  ```
- **After:**
  ```text
  yolo cannot read its shared MCP sign-ins (<err>), so each jail signs in on its own for now. Run `yolo check` on the host to find the file, fix its permissions, then start a new jail.
  ```
- **Why:** 5, 6; the step did not say which permissions.

### Share and replica errors
- **Where:** `internal/mcpauth/share.go:50`, `:94`, `:99`, `:103`, `:107`, `:111` and `:117`.
- **Before:**
  ```text
  mcp-auth: unsafe replica component; retry with a valid pack declaration
  ```
- **After:**
  ```text
  A pack's MCP sign-in setting names an unsafe path, so the sign-in is not shared. This is a bug in the pack: report it to the pack's author.
  ```
- **Why:** 5, 6 ("replica", "declaration"); P5 (a retry cannot fix it).

## Disk cleanup (`yolo prune`, `yolo stores`)

### Build-link section
- **Where:** `internal/prune/buildstatereport.go:16`, `:24`, `:30`, `:33`, `:39` and `:42`.
- **Before:**
  ```text
  Retired and aged build links  (roots nothing reads any more)
    would remove: 3 image-copier link(s) dangling or unused for 168h0m0s
    retired jail-prefix out-links skipped — <why>; declining to sweep
    package-profile roots skipped — <why>; declining to sweep
  ```
- **After:**
  ```text
  Old build links  (they keep build output that nothing uses any more)
    would remove: 3 links to image copy tools, unused for 7 days
    skipped old links to jail binaries: <why>, so yolo cannot tell which are unused
    skipped old package links: <why>, so yolo cannot tell which are unused
  ```
- **Why:** 5, 6 ("out-link", "package-profile root", "image-copier", "sweep"). The duration should print as days, not Go's `168h0m0s`.

### Running image cannot be mapped
- **Where:** `internal/prune/liveimageroots.go:112`, which `:66`, `:70`, `:78`, `:82`, `:152` and `:157` feed.
- **Before:**
  ```text
  a running jail's image cannot be mapped to its GC root: <ref> (a tag that names no store path)
  ```
- **After:**
  ```text
  yolo cannot tell which saved build a running jail's image (<ref>) uses, so it keeps every saved image build this run. Stop that jail and launch its workspace again on the host, then run yolo prune again.
  ```
- **Why:** 6 ("GC root", "store path", "closure"); no next step.
- **Settled:** the caller declines only the image-root step, not the whole store pass. `internal/prune/prunecmd.go:1020` prints it as `skipped — <why>; declining to reap image roots` under "Orphaned image GC roots". The store GC later runs its own check (`UnrootedRunningImages`, `prunecmd.go:1441`), which skips the store GC for the same jail and already names the fix: stop the jail and launch its workspace again on the host, because attaching loads nothing. The rewrite now says only what this step keeps, and borrows that fix.

### Unused tool versions skipped in a jail
- **Where:** `internal/prune/miseversionsreport.go:25` and `:30`. Fine apart from the dash and "container VM … the sandbox account's, on macos-user". Optional.

### Leftover session homes
- **Where:** `internal/prune/hostsessionhomes.go:99` and `:107`.
- **Before:**
  ```text
  an interrupted launch's, with no lease
  host session-home capture is unavailable; source retained
  ```
- **After:**
  ```text
  left by a launch that did not finish, and nothing is using it
  kept: yolo could not save this session's files first
  ```
- **Why:** 3, 6 ("lease", "capture", "source").

### Embedded-tree reasons
- **Where:** `internal/prune/embeddedtrees.go:171`–`:178`, `:255`, `:258`, `:260`, `:369` and `:371`.
- **Before:**
  ```text
  no lease, so nothing can prove it unused / fallback tree whose process is gone (lease free)
  ```
- **After:**
  ```text
  no running yolo has it marked as in use / a temporary copy whose yolo process has exited
  ```
- **Why:** 6 ("lease", "fallback tree").

### Dev-bundle OS note
- **Where:** `internal/prune/devbundles.go:161`.
- **Before:**
  ```text
  this OS has no process table yolo can read every bundle user from (a macos-user session runs as another account)
  ```
- **After:**
  ```text
  on macOS, yolo cannot see which processes use each development bundle, because some sessions run as another account
  ```
- **Why:** 5, 6.

## Image build, delivery and the container runtime (launch lines)

### GC-root warnings
- **Where:** `internal/image/prefixroot.go:67` and `internal/image/gcroot.go:65`.
- **Before:**
  ```text
  could not register a GC root for the jail's own binaries (a nix-collect-garbage could delete the prefix this jail runs from)
  could not register GC root for the running image (a nix-collect-garbage could reclaim it)
  ```
- **After:**
  ```text
  could not protect this jail's yolo programs from Nix garbage collection, so running nix-collect-garbage while the jail runs could break it. Avoid running it until this jail exits.
  could not protect the jail image from Nix garbage collection, so running nix-collect-garbage could delete it and the next launch would rebuild it.
  ```
- **Why:** 6 ("GC root", "prefix"); no next step.

### Image store line
- **Where:** `internal/image/storespec.go:347`, `:355`, `:366`, `:372`, `:377` and `:384`.
- **Before (`:377`):**
  ```text
    Image store: the copier's own storage.conf lookup — podman is rootful, and root resolves the same system store in both, so the copy names none.
  ```
- **After:**
  ```text
    Image store: podman's system store (podman runs as root).
  ```
- **Before (`:372`):**
  ```text
    Image store: yolo could not read podman's store (<why>), so the copier picks one from its own storage.conf lookup. If that is not the store podman reads, the copy below fails; a ~/.config/containers/storage.conf naming podman's …
  ```
- **After:**
  ```text
    Image store: yolo could not find where podman keeps images (<why>), so it guesses. If the copy below fails, set the store podman uses in ~/.config/containers/storage.conf.
  ```
- **Why:** 6 ("copier", "store spec", "lookup"), 8.

### Nested builder network
- **Where:** `internal/containerbuilder/session.go:115`.
- **Before:**
  ```text
  could not choose a supported private network for the nested Linux builder: <err>
  Fix: inspect `podman info --format json` and use Podman with its supported private-network helper reported there
  ```
- **After:**
  ```text
  could not set up networking for the Linux build container: <err>
  Fix: install pasta (podman 6 or later) or slirp4netns (older podman) where podman can find it; `podman info --format json` shows which one podman looks for.
  ```
- **Why:** 6; the old fix names no action.
- **Settled:** `internal/containerbuilder/containerbuilder.go:122`–`:133` picks by podman's major version: podman 6 or later needs an absolute `host.pasta.executable`, and older podman needs `host.slirp4netns.executable`. The wrapped error already says which one is missing ("podman 6.0.1 does not report host.pasta.executable"), so the rewrite can name the one helper the version needs.

### Linux builder remedy (macOS)
- **Where:** `internal/nixdiag/nixdiag.go:254`.
- **Before:** an 18-line paragraph. It explains automatic offload, uses capitals ("AUTOMATICALLY", "CONTAINER") and says "no sudo, no VM … zero idle RAM".
- **After:**
  ```text
  A package in the jail image is not in the Nix binary cache, so it must be built on Linux. yolo normally does that in a short-lived Linux container, but that build failed or could not start.
    1. Make sure your container runtime is running: <runtime check>.
    2. Run `yolo` again.
  If you added a custom `packages` entry with a {version,url,hash} override, it is never cached, so it always needs this build.
  ```
- **Why:** 7, 8. Cut the explanation of how offload works and the remarks about a linux-builder you may already run.

### Podman machine OOM
- **Where:** `internal/runtime/display.go:183`.
- **Before:** `…the VM's OOM-killer fired — '<m>' has only N MB (below the M MB recommended floor…`
- **After:** `…the Podman machine '<m>' ran out of memory: it has N MB, below the M MB yolo recommends…`
- **Why:** 6 ("OOM-killer", "floor").

### Launch-record warning
- **Where:** `internal/image/launchrecord.go:146`. Fine; skip.

## Install and update

### Binary-only install cannot update
- **Where:** `internal/selfupdate/apply.go:103`. A user who installed with `uv tool` (or another binary-only channel) sees this when they run `yolo update`.
- **Before:**
  ```text
  uv-tool installs only the host binary and has no matched flake/helper bundle; YOLO_REPO_ROOT is obsolete and cannot supply the missing runtime pair. Updating one half would make launches incomplete. Build a matched development bundle with `just build-dev-bundle` and invoke its printed launcher path, or install both halves together with `just install` (uv tool upgrade yolo-jail)
  ```
- **After:**
  ```text
  This yolo was installed with <go install | pipx | uv tool>, which installs only the yolo program and not the files a jail needs, so `yolo update` cannot update it. Reinstall yolo from <releases page>.
  ```
- **Why:** 1, 6 ("flake/helper bundle", "runtime pair", `YOLO_REPO_ROOT`), 8. The `just …` steps assume a checkout this user does not have.
- **Settled:** three install kinds reach it (`internal/selfupdate/apply.go:77`–`:82`): `go-install`, `pipx` and `uv`. Each passes its own upgrade command, which the original prints in parentheses at the end. That reads as a suggestion to run it, though the sentence before says updating one half breaks launches. Drop it. `ReleasesPage` already exists in the package, and the archive and unknown cases use it.

### Launcher and bundle mismatch
- **Where:** `internal/reporoot/reporoot.go:115`, and the variants at `:106`, `:109`, `:120` and `:123`.
- **Before:**
  ```text
  no complete adjacent yolo-jail bundle was found, and this launcher is not marked installed with a snapshot identity. Build a matched development bundle with `just build-dev-bundle` and invoke its printed launcher path, or install yolo with `just install`
  ```
- **After:**
  ```text
  This yolo cannot find the jail files that belong with it. Reinstall yolo. From a yolo-jail checkout, run `just install`, or run `just build-dev-bundle` and use the launcher it prints.
  ```
- **Why:** 5, 6 ("adjacent bundle", "snapshot identity").

### Dirty checkout during update
- **Where:** `internal/selfupdate/apply.go:160`. Long but clear; optionally split it into three sentences (rule 7).

## Host-run, nix roots, durable dir

### Nix root watcher off Linux
- **Where:** `internal/nixroots/watch_other.go:14`.
- **Before:**
  ```text
  the root watcher runs only in a Linux container jail; on macos-user, `nix-store --add-root` already makes a root the host honors
  ```
- **After:**
  ```text
  this only runs in a Linux jail. On macOS, `nix-store --add-root` already keeps a build from being garbage-collected.
  ```
- **Why:** 6 ("root watcher", "macos-user").

### Registry lock held
- **Where:** `internal/nixroots/registry.go:292`. Fine; skip.

### Durable dir unavailable
- **Where:** `internal/durable/durable.go:154`.
- **Before:** `Durable dir: unavailable this launch: <reason>.`
- **After:** `The durable directory (.yolo/durable, for files that should survive a restart) is unavailable for this launch: <reason>.`
- **Why:** 6 ("durable dir" undefined at first use), 3.

## Pack services and loophole daemons

### Fetched pack's host part does not run
- **Where:** `internal/launchservice/launchservice.go:187`, `:218` and `:237`.
- **Before:**
  ```text
  a fetched pack's doorway does not run on your machine outside the sandbox …
  a fetched pack's host half does not run on your machine …
  ```
- **After:**
  ```text
  yolo does not run the host-side program of a pack it downloaded. To run it, select a local copy of the pack by its path …
  ```
- **Why:** 6 ("doorway", "host half").

### Service started without a listen address
- **Where:** `internal/launchservice/listener.go:52`.
- **Before:** `no --listen address: the launch hands a doorway the port it picked`
- **After:** `no --listen address given; yolo starts this service itself and passes one`
- **Why:** 6, 8.

### Git push preflight (`yolo access -- git push`)
- **Where:**
  - `internal/gitremote/preflight.go:159`, `:164`, `:178`, `:184`, `:188`, `:193`, `:206` and `:211`
  - `internal/gitremote/access_push.go:19` and `:69`
- **Before:**
  ```text
  git's dry-run porcelain is incomplete
  an up-to-date ref cannot become a push command
  ```
- **After:**
  ```text
  git's trial push did not report every ref, so yolo cannot predict this push. Run the push itself to get the broker's answer.
  this ref is already up to date, so there is nothing to push.
  ```
- **Why:** 6 ("porcelain", "push command"); no next step. Several of these say nothing the user can act on. Give them the same closing step that `preflight.go:40` has: "run `git push` with an explicit source:refs/heads/name".

### Host-processes and journal grant files
- **Where:** `internal/hostprocesses/grants.go:111` and `internal/journald/grant.go:68`. Fine; skip.

### macOS log bridge: no sandbox account
- **Where:** `internal/journald/macoslogcmd.go:136`. It runs six clauses together.
- **After:** split the clauses into separate sentences, one command each (rule 7). Name "seatbelt-yolo-account" only after "a launch that runs the agent as the separate yolo account".

## Smaller packages (second pass)

Most of these strings are integrity checks that a launch or a broker wraps into a refusal it words itself. `callbrokerinput` (about 50 sites), `callbrokeridentity` (about 26), `execmonitor` (about 59) and most of `brokerscope` and `ctxinject` are in that group. `execmonitor`'s errors are dropped by its only caller (`internal/cli/stopmonitor_linux.go:25` keeps just the boolean), so none of them reach a person. `clipimage`, `installerbody`, `changetiming` and `openauthclient/pi.go:102` already state what happened and name the next step; no changes. The rest:

### Relay errors in the jail's OAuth terminator
- **Where:** `internal/oauthterminator/client.go:74`, `:84` and `:152`.
- **Before (`:74`):**
  ```text
  relay auth rejected — this jail's endpoint file token does not match the relay (<path>)
  ```
- **After:**
  ```text
  the host's sign-in relay refused this jail's token (<path>), so the agent cannot refresh its login. Restart the jail; if this repeats, run `yolo check` on the host.
  ```
- **Before (`:84`):** `relay unreachable — the host-side relay for this jail is down (<path>: <err>)`
- **After:** `the host's sign-in relay for this jail is not running (<path>: <err>). Restart the jail; if this repeats, run yolo check on the host.`
- **Before (`:152`):** `host broker unreachable through the relay (connection closed without an exit frame)`
- **After:** `the relay answered, but the host's sign-in service behind it did not (the connection closed early). Run yolo check on the host, then try again.`
- **Why:** 6 ("endpoint file token", "exit frame"), P8; none names a next step. The code keeps the three layers apart on purpose, and the rewrites keep that.

### OpenAI client: host-only operations
- **Where:** `internal/openauthclient/main.go:91`.
- **Before:**
  ```text
  openai-auth-client login: this is a MACHINE-WIDE operation and runs only on the host's private credential socket. Set $<HostSocketEnv> or pass --host-socket <path>.
  ```
- **After:**
  ```text
  openai-auth-client login: this changes the OpenAI login for every jail on this machine, so it runs only on the host. Run `yolo openai-auth <status|import|logout>` on the host instead.
  ```
- **Why:** 5 (capitals), 6 ("private credential socket"). The old step tells an agent how to aim at the host socket, which is the thing the refusal exists to stop. `yolo openai-auth` is a real top-level verb (`internal/cli/dispatch.go:48`).

### OpenAI import of a yolo-written file
- **Where:** `internal/openaiauthdaemon/import.go:118`.
- **Before:**
  ```text
  refusing to import a yolo broker view: its refresh_token is a generation marker, not a credential, and installing it would replace the canonical grant with something nothing can redeem. Import the auth.json Codex wrote from its own login
  ```
- **After:**
  ```text
  this auth.json was written by yolo inside a jail and holds no real login, so importing it would break the login every jail shares. Import the auth.json Codex wrote when you signed in to it outside yolo.
  ```
- **Why:** 6 ("broker view", "generation marker", "canonical grant").

### OpenAI self-check
- **Where:** `internal/openaiauthdaemon/main.go:209`.
- **Before:** `FAIL: OpenAI refused to refresh the machine-wide grant (<code>), so every jail borrowing it fails — log in again with `yolo -- codex`, or `yolo openai-auth import --from <auth.json>``
- **After:** `FAIL: OpenAI refused to renew the shared login (<code>), so Codex fails in every jail. Sign in again with yolo -- codex, or run yolo openai-auth import --from <auth.json>.`
- **Why:** 6 ("grant", "borrowing"), P8. `:193` and `:200` are fine apart from the dash.

### Pack overlays with no effect
- **Where:** `internal/packoverlay/packoverlay.go:117`, `:131` and `:134`, and `internal/packoverlay/conventional.go:107`.
- **Before (`:134`):**
  ```text
  no effect — <target> has no owner among the selected packs (no pack declares that surface — check the identity)
  ```
- **After:**
  ```text
  has no effect: no selected pack writes <target>. Check the file name against `yolo pack footprint`, or select the pack that owns it.
  ```
- **Before (`:131`):** `no effect — <target> is one of yolo's OWN surfaces, not a pack's; a posture list contributes to a surface a pack owns`
- **After:** `has no effect: yolo writes <target> itself, and a pack's overlay can only change a file a pack writes.`
- **Before (`conventional.go:107`):** `cannot resolve surface identity "<base>"; select its owner pack and use agent-surface spelling`
- **After:** `no selected pack writes a file called "<base>". Select the pack that writes it, and name the overlay <agent>-<file>.<ext>.`
- **Why:** 5 ("OWN"), 6 ("surface", "identity", "posture list", "agent-surface spelling"), P8. *Owner to confirm* the exact overlay naming rule before shipping the `conventional.go:107` wording; this pass did not trace it.

### Base-home legacy state report
- **Where:** `internal/basehome/report.go:120`, `:124` and `:205`.
- **Before (`:120`):** `Base home holds 3 entries of legacy per-workspace state, all EMPTY directories (no data to preserve): …`
- **After:** `The shared jail home has 3 leftover folders from older yolo versions, all empty: ….`
- **Before (`:124`):** `Legacy per-workspace state in the base home: <parts> — 1.2 GiB in 40 entries total.`
- **After:** `The shared jail home holds 1.2 GiB in 40 entries left over from older yolo versions: <parts>.`
- **Why:** 5, 6 ("base home", "legacy per-workspace state"), P8. The next step is in `CleanupLines`, below these lines, so it is not repeated.

### Live context mounts in an older jail
- **Where:** `internal/ctxinject/ctxinject.go:51`.
- **Before:** `/ctx in this jail is not the tmpfs a live-mounts launch makes (the jail started before live mounts existed); restart the jail to mount there`
- **After:** `this jail started before yolo could add context mounts to a running jail, so the mount waits for a restart. Restart the jail to get it.`
- **Why:** 6 ("tmpfs", "live-mounts launch").

### Clipboard write tool missing
- **Where:** `internal/clipimage/reader.go:295`.
- **Before:** `no clipboard reader for writing (wl-copy, xclip or pbcopy)`
- **After:** `no clipboard tool to write with: install wl-clipboard (wl-copy) or xclip, then try again.`
- **Why:** "reader for writing" contradicts itself, and the line names no step. Every other `clipimage` error is fine.

### CircleCI classifier
- **Where:** `internal/circleciclassify/classifier.go:38` and `:138`.
- **Before (`:38`):** `CircleCI classifier protocol is unsupported; rebuild yolo and retry`
- **After:** `the circleci pack and this yolo disagree on how to talk; restart the jail so its tools match the host's yolo.`
- **Before (`:138`):** `run list requires --project because host git-remote discovery is not used by the broker`
- **After:** `circleci run list needs --project <slug> here: the broker does not read your git remotes to find it.`
- **Why:** P5; a user cannot "rebuild yolo". Same fix as P5's "update the selected client" lines.

### Scope-file refusals that say "relaunch"
- **Where:** `internal/brokerscope/file.go:314` and `:299`.
- **Before (`:314`):** `repository no longer approved; relaunch the jail to approve its scope`
- **After:** `this repository left the workspace's approved list while the command was running, so nothing ran. To add it back, run yolo request github repos <OWNER/REPO> here and ask the human to run yolo config apply on the host.`
- **Why:** P4. The brokers check scope first and refuse an out-of-scope repository with their own wording (`internal/gitbroker/broker.go:461`), so this line is the race where scope shrinks between two reads. On a backend with live scope, `yolo config apply` changes the running broker's list, so "relaunch" is not the step there (see Wrong today). `:299` (identity changed) is right to say relaunch.

## macOS certificate-trust disclosure (rendered)

`printCATrust` (`internal/macosuser/cabundle.go:476`–`:631`) builds one to four lines from fragments, plus `SSLCertFileNotes` (`:265`), `goCAFileLine` (`internal/macosuser/cagodebug.go:123`) and `catrust.SourcesPhrase` (`internal/catrust/catrust.go:198`). These are the combinations rendered by hand from the code. `P` is the tool profile's bundle, `B` the session bundle, `E` the Node extras file, and the five variables are `NIX_SSL_CERT_FILE, SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, GIT_SSL_CAINFO` (written `<five>` below).

Rendered:

1. No caller bundle, one source, Node file set, three left out:
   ```text
   Trusting 2 certificate authorities from this Mac's System keychain: Corp Root, Proxy CA. <five> name B, the tool profile's public roots plus them. NODE_EXTRA_CA_CERTS names E, those alone. 3 more in it were left out: expired, not for TLS servers, or not trusted by macOS for TLS.
   ```
2. Two sources: `Trusting 3 certificate authorities, 2 from this Mac's System keychain (Corp Root, Proxy CA) and 1 from your login keychain (Dev CA). …`
3. A host bundle as a source: `Trusting 2 certificate authorities, 1 from this Mac's System keychain (Corp Root) and 1 from this host's CA bundle (/etc/ssl/corp.pem) (Proxy CA). …`
4. Nothing kept, no caller bundle: `TLS in the sandbox: <five> name the public roots in its tool profile (P); this Mac's System keychain adds no certificate authority. 4 more in it were left out: …`
5. Caller bundle, CAs kept, Node not ours: `This Mac's System keychain trusts 2 certificate authorities for TLS (Corp Root, Proxy CA), and the sandbox does not add them. Your configured environment sets SSL_CERT_FILE, so <five> name /mine.pem. NODE_EXTRA_CA_CERTS keeps your own value too. 3 more in it were left out: …`
6. Caller bundle, Node file ours: `Trusting 2 certificate authorities from this Mac's System keychain in Node only: Corp Root, Proxy CA. NODE_EXTRA_CA_CERTS names E, those alone. Your configured environment sets SSL_CERT_FILE, so <five> name /mine.pem. Every other tool trusts only what those variables name.`
7. Caller bundle, nothing kept: `TLS in the sandbox: your configured environment sets SSL_CERT_FILE, so <five> name /mine.pem. This Mac's System keychain adds no certificate authority.`
8. System keychain unreadable, login keychain read, CAs kept: a warning line `Warning: could not read this Mac's System keychain (<err>), so the sandbox adds only your login keychain's certificate authorities.`, then the main line.

Defects found (each a rewrite target; the general rewrite for the whole function is "one sentence per fact, and name the count's source in the same sentence"):

- **"N more in it/them were left out" points at the wrong noun** (renders 1 and 5). The pronoun stands for the keychain, but the sentence before it ends on a file path or on `NODE_EXTRA_CA_CERTS`. With both keychains read it is "in them", which reads as "in those certificate authorities". *After:* `<N> more in <holder> were left out: …`, spelling the holder out.
- **"more" after none** (render 4, and 7 with a left-out count). "adds no certificate authority. 4 more … were left out" counts up from zero. *After:* `4 certificate authorities in this Mac's System keychain were left out: …` when `n == 0`.
- **Verb agreement for one variable.** `"%s name"` (`:616`, `:622` and `:630`) and the tail `"… keep the empty value …"` (`:611`) never use `plural`. When the user's own environment empties some variables, this prints `SSL_CERT_FILE name B` or `CURL_CA_BUNDLE keep the empty value`. When it empties all five, the default case prints `no variable name B`, starting a sentence in lowercase. `caVarsClause` (`:639`) already gets this right; use it.
- **Double parentheses for a host bundle source** (render 3): `this host's CA bundle (/etc/ssl/corp.pem) (Proxy CA)`. `SourcesPhrase` wraps names in parentheses, and `HostBundleSource` already has some. *After:* `1 from /etc/ssl/corp.pem (Proxy CA)`, or a colon form.
- **"those alone"** (renders 1 and 6) is a fragment. *After:* `NODE_EXTRA_CA_CERTS names E, which holds only these.`
- **An unreadable keychain still credited** (render 5 with a System keychain read error). The subject is built from `LoginRead` and `bundleRead` alone, so it prints "This Mac's System keychain and your login keychain trust 2 certificate authorities" when the System keychain was not read. The warning line above says otherwise. *Fix:* leave the System keychain out of `holder` when `ReadError` is set.
- **Elliptical possessives in the warnings:** `this Mac's System keychain's still are` (`:495`) and `this Mac's keychains' still are` (`SSLCertFileNotes`, `:271`). *After:* `the ones in this Mac's System keychain are still trusted.`
- **`certificate(s)`** (`:273`) instead of `plural`.
- **Legacy key in advice:** `env_sources` at `:615` (the JSON fragment, already listed) and at `cagodebug.go:151`. In TOML both are `env.set.<NAME>`.

Checked and true: the `GODEBUG` line's claims (Go 1.27, `x509sslcertoverrideplatform=1`) match `cagodebug.go:38`–`:43`. The login-keychain note and the System-keychain warning are each printed only when the main line would otherwise overstate, as the comment at `:489` says.

---

## Wrong today

- **`--add` refusal text is unreachable and names a removed flag.**
  - `yolo approve` refuses `--add` before any store call (`internal/cli/approveremoved_test.go` pins this), so `Decision.Add` is never true. These branches can never print:
    - `internal/ghbroker/approvalstore.go:53`, `:59` and `:72` ("--add is not available for …")
    - `internal/gitbroker/approvalauthority.go:210` and `:216`
    - `internal/callbroker/timed.go:31` ("a pack call never joins the working set by an answer")
  - If they ever did print, they would tell the human about a flag that no longer exists.
  - `internal/brokerstore/store.go:1123` ("choose Add, or a positive duration") is reachable through `--deny`, `--session` or `--uses` on a per-resource request, and it tells the human to "choose Add".
  - *Fix:* delete the `add` branches, and reword `:1123` as "this request takes `yolo approve <id>`, `--for <duration>` or `--deny`".
- **macOS memory guard names `yolo-jail.jsonc`.** These lines tell users to edit `yolo-jail.jsonc`. A converted workspace has `yolo-jail.toml` instead, so the advice points at a file that is not there.
  - `internal/macosuser/sessionguard.go:381`, `:430` and `:443`
  - The key there is `machine.resources.memory`, and its value is a size such as `16g`, not a byte count.
- **The AWS profile refusal names `~/.config/yolo-jail/config.jsonc` and a legacy key.** A migrated user config is `config.toml`, and the key there is `allow.aws-auth.profile`, not `loopholes.aws-auth.settings.profile`. The daemon's startup path already avoids this text, but `yolo check` prints it through the self-check.
  - `internal/awsauth/settings.go:180`, printed by `internal/awsauthdaemon/main.go:483`
- **The CA hint gives JSON syntax for `env_sources`.** It writes `{"SSL_CERT_FILE": "<that file>"}`, a JSON fragment, which a TOML config cannot take as written. The right form is `yolo config set env.set.SSL_CERT_FILE <file>`. `cagodebug.go:151` names `env_sources` the same way.
  - `internal/macosuser/cabundle.go:615`
  - `internal/macosuser/cagodebug.go:151`
- **The GitHub sets refusals name the legacy key, and no verb can carry out their advice.** `internal/ghbroker/authority.go:98` says `brokered.github.sets`; a TOML user config spells it `allow.github.sets`. Removing an entry from that list, or the whole list, has no command: `yolo config unset` refuses `allow.*.*` keys, whose type is Any. That breaks "advice names a verb" (`AGENTS.md`). *Owner decision:* a removal verb for `allow.<pack>.<setting>` lists, or an accepted exception that names the file.
- **macOS guest refusal names a config key that does not exist.** `internal/macosuser/orchestrator.go:1796` says to set `"loopholes": {"host-processes": {"enabled": false}}`. `loopholes` is not in the readable schema, and `host-processes` and `serial` have no off switch, so the only way past is to deselect the pack.
- **The macOS CA disclosure credits a keychain it could not read.** In the caller-bundle case with a System keychain read error, the main line says "This Mac's System keychain and your login keychain trust N certificate authorities". See the rendered section.
- **Read grant: "then relaunch" is wrong where scope is live (settled).** `internal/gitbroker/access.go:112` tells the agent to ask for `yolo allow git fetch`, "then relaunch". On Linux podman and on macos-user no relaunch is needed:
  - The broker re-reads its scope file, grants included, on every call (`brokercore.Live` in `internal/brokercore/authority.go`; the git broker's fetch path reads it fresh at `internal/gitbroker/broker.go:461`–`:470`).
  - Run interactively, `yolo allow` ends by running `yolo config apply`'s gate (`allowApprovalGate` in `internal/cli/allow.go`). That gate rewrites the running broker's scope file through `brokerscope.ReplaceScopeContext` (`internal/cli/run/liveapply.go`).
  - It does so only where the launch recorded `LiveScope`: `rt == "podman" && !o.IsMacOS || rt == "macos-user"` (`internal/cli/run/mechanicsfacts.go:68`). On podman on macOS (and Apple Container) a restart is still needed.
  - The broker's own refusal already gets this right. `config.BrokerNoReadRefusal(…, f.LiveScope)` (`internal/config/brokered.go:380`) says "This jail's running broker takes the grant at its next call" when live, and "restart the jail afterwards" when not. Only `yolo access`'s `Next` line ignores the flag.
  - *Fix:* build `access.go:112`'s `Next` from the same `f.LiveScope` switch, or call `BrokerNoReadRefusal`'s advice directly.
  - `internal/ghbroker/access.go:97` and `:99` ("…then relaunch") are correct as written. They fire when the GitHub broker started with no usable `gh`, and that is decided once, when the broker starts.
  - `internal/brokerscope/file.go:314` ("relaunch the jail to approve its scope") has the same flaw for a repository that left the scope. It is a narrow race path; see "Scope-file refusals that say relaunch".
- **`just install` advice for non-checkout installs.** These lines tell every user to "reinstall yolo with `just install`". A Homebrew or release-archive user has no checkout, so `just` is not available to them. The advice is only true for developers.
  - `internal/reporoot/reporoot.go:70` and `:90` ("cannot locate the running yolo executable")
  - `internal/selfupdate/apply.go:103` (`binaryOnlyUpdateError`) also mentions the retired `YOLO_REPO_ROOT`, which a uv user never set.
- **`callbroker` "retry" steps that cannot succeed.** Many lines end in "check the selected pack and retry" or "fix the selected pack and retry", in `classifier.go`, `http.go`, `http_native.go` and `handler.go`. They fire on a pack's classifier answering wrongly. That fails the same way every time, so "retry" is misleading. See P5.
- **`callbroker/handler.go:207`: "Wait for pack broker approval policy and host activation before retrying".** Nothing the user or agent can wait for will turn this on in the current build. The writes path is hard-off, so the step is false as written.

## Not covered (honest)

- `internal/setupcensus` (about 380 strings) was not reviewed line by line. Its strings feed the generated `settings-per-setup.md` reference table, not the terminal.
- Left out on purpose, as the coordinator asked: the `svcendpoint`/`hostservice` `Logger.Printf` lines and the `oauthbroker` log lines. They are debug and audit logs that reach a log file, not a terminal.
- The integrity checks in `callbrokerinput`, `callbrokeridentity`, `brokerscope` (`grant.go`, `live.go`) and `ctxinject_linux.go` were read but not rewritten. I did not trace every wrapper to confirm the user never sees them raw. If a wrapper does pass them through as-is, they need P4's treatment.
- Two rewrites still need an owner to confirm a fact this pass did not trace: the overlay naming rule behind `packoverlay/conventional.go:107`, and whether `env.set.SSL_CERT_FILE` counts as `follows` for the CA disclosure.
- The CA disclosure was rendered by hand from the code, not by running it. Eight main combinations are written out; mixed cases (for example a caller bundle plus a host-bundle source plus a login read error) were checked only clause by clause.
- Line numbers point at the line where each message's string starts. For a multi-line call, that is the first line of the literal, which can sit one line below the `fmt.Errorf(` call.
