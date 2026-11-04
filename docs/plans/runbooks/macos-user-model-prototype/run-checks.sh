#!/usr/bin/env bash
# run-checks.sh: run the macos-user model prototype checks on a Mac.
#
# Loads profile.sb (beside this script) with sandbox-exec and runs each check of
# docs/design/macos-user-model.md section 6 under it, printing PASS/FAIL with the
# command and its output. Exits non-zero if any check FAILs.
#
#   ./run-checks.sh --mode self
#   ./run-checks.sh --mode account --account _yolojail
#   ./run-checks.sh --mode self --profile-from-yolo /path/to/yolo
#
# Options:
#   --mode self|account     who runs the sandbox: you, or the second account (via sudo -u)
#   --account NAME          the account for --mode account (default _yolojail)
#   --with-claude           also install Claude Code into the sandbox's own HOME and run
#                           `claude --version` (network; no login, no API call)
#   --keep                  keep the temp dirs and print the command for a manual sandboxed shell
#   --profile-from-yolo YOLO
#                           run every check against the profile yolo generates instead of
#                           profile.sb: YOLO is a yolo binary, and the script renders the profile
#                           with `YOLO internal macos-user-profile` for its own workspace, home and
#                           temp dirs and its fake host-services dir (check g)
#
# It creates only its own temp dirs, a decoy file in your home, a decoy directory
# (/opt/src/... if creatable, else /Library/Caches/...), one throwaway generic
# password in your login keychain (for the keychain check), and a few processes;
# it removes all of them on exit. First run on a Mac: macOS 26.5, 2026-10-22.

# Single-quoted check scripts are expanded by the bash inside the sandbox, on purpose.
# shellcheck disable=SC2016

set -u

MODE=""
ACCOUNT="_yolojail"
WITH_CLAUDE=0
KEEP=0
YOLO_BIN=""

usage() { sed -n '2,27p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
	case "$1" in
	--mode) MODE="${2:-}"; shift 2 ;;
	--account) ACCOUNT="${2:-}"; shift 2 ;;
	--with-claude) WITH_CLAUDE=1; shift ;;
	--keep) KEEP=1; shift ;;
	--profile-from-yolo) YOLO_BIN="${2:-}"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
	esac
done

case "$MODE" in
self | account) ;;
*) echo "--mode self or --mode account is required (try --help)" >&2; exit 2 ;;
esac

if [ "$(uname -s)" != "Darwin" ]; then
	echo "This prototype needs macOS (sandbox-exec). Run it on a Mac." >&2
	exit 2
fi
if [ ! -x /usr/bin/sandbox-exec ]; then
	echo "/usr/bin/sandbox-exec is missing; this macOS cannot run the prototype." >&2
	exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
SRC_PROFILE="$SCRIPT_DIR/profile.sb"
if [ -n "$YOLO_BIN" ]; then
	# The generated profile: YOLO_BIN renders it once the workspace and temp dirs exist.
	case "$YOLO_BIN" in */*) ;; *) YOLO_BIN="$(command -v "$YOLO_BIN" || echo "$YOLO_BIN")" ;; esac
	if [ ! -x "$YOLO_BIN" ]; then
		echo "--profile-from-yolo needs a yolo binary, and '$YOLO_BIN' is not executable." >&2
		echo "Build one from the checkout with 'go build -o /tmp/yolo ./cmd/yolo' and pass /tmp/yolo." >&2
		exit 2
	fi
	if ! "$YOLO_BIN" internal macos-user-profile --help >/dev/null 2>&1; then
		echo "'$YOLO_BIN' has no 'internal macos-user-profile' verb (a yolo older than the plan's slice 10)." >&2
		echo "Build one from a current checkout with 'go build -o /tmp/yolo ./cmd/yolo' and pass that." >&2
		exit 2
	fi
	SRC_PROFILE=""
else
	[ -r "$SRC_PROFILE" ] || { echo "profile.sb not found beside this script ($SRC_PROFILE)" >&2; exit 2; }
fi

HOST_HOME="$(cd "$HOME" && pwd -P)"
MADE_SHARED=0
NONCE="$$${RANDOM}"
AS=()
# NARROW=1: the Mac grants only yolo's run-as rule, `(ACCOUNT) NOPASSWD: /usr/bin/env`, so every
# crossing goes through /usr/bin/env, the account reaches the workspace through access grants
# (ACLs, settable on your own files without root), and it deletes its own files at cleanup.
NARROW=0

if [ "$MODE" = account ]; then
	if ! id -u "$ACCOUNT" >/dev/null 2>&1; then
		echo "SKIP: account '$ACCOUNT' does not exist on this Mac."
		echo "      Account mode needs a Mac where yolo's macos-setup already created it;"
		echo "      run '--mode self' here instead, or run account mode on a Mac that has it."
		exit 0
	fi
	if sudo -n -u "$ACCOUNT" /usr/bin/env /usr/bin/true 2>/dev/null; then
		NARROW=1
		echo "Account mode runs commands as '$ACCOUNT' through yolo's run-as rule (sudo -u $ACCOUNT /usr/bin/env); no password needed."
	else
		echo "Account mode runs commands as '$ACCOUNT' through sudo; you may be asked for your password."
		sudo -v || { echo "sudo failed; account mode needs sudo. Run '--mode self' instead." >&2; exit 2; }
	fi
	AS=(sudo -n -u "$ACCOUNT" /usr/bin/env)
	if [ ! -d /Users/Shared/yolo ]; then
		MADE_SHARED=1
		mkdir -p /Users/Shared/yolo 2>/dev/null || sudo mkdir -p /Users/Shared/yolo
	fi
	ROOT="/Users/Shared/yolo/prototype-$NONCE"
	if ! mkdir "$ROOT" 2>/dev/null; then
		sudo mkdir "$ROOT" && sudo chown "$(id -un)" "$ROOT"
	fi
else
	# The ruled model allows a workspace in your home, so self mode puts it there.
	ROOT="$HOST_HOME/yolo-prototype-$NONCE"
fi

mkdir -p "$ROOT" || { echo "cannot create $ROOT" >&2; exit 2; }
ROOT="$(cd "$ROOT" && pwd -P)"
WORKSPACE="$ROOT/workspace"
AGENT_HOME="$WORKSPACE/.yolo/home"
SANDBOX_TMP="$ROOT/tmp"
mkdir -p "$WORKSPACE" "$AGENT_HOME" "$SANDBOX_TMP"
PROFILE="$ROOT/profile.sb"
if [ -n "$SRC_PROFILE" ]; then
	cp "$SRC_PROFILE" "$PROFILE"
	chmod 0644 "$PROFILE"
fi
chmod 0755 "$ROOT"

DECOY_SECRET="$HOST_HOME/yolo-prototype-decoy-$NONCE.txt"
printf 'yolo-prototype decoy secret %s\n' "$NONCE" >"$DECOY_SECRET"
chmod 0600 "$DECOY_SECRET"

# A decoy project outside /Users, like /opt/src/clientX.
DECOY_PROJECT=""
DECOY_PROJECT_MADE_PARENT=""
for cand in "/opt/src/yolo-prototype-clientX-$NONCE" "/Library/Caches/yolo-prototype-clientX-$NONCE"; do
	parent="$(dirname "$cand")"
	if [ ! -d "$parent" ] && mkdir "$parent" 2>/dev/null; then
		DECOY_PROJECT_MADE_PARENT="$parent"
	fi
	if mkdir "$cand" 2>/dev/null; then
		DECOY_PROJECT="$cand"
		break
	fi
	[ -n "$DECOY_PROJECT_MADE_PARENT" ] && rmdir "$DECOY_PROJECT_MADE_PARENT" 2>/dev/null && DECOY_PROJECT_MADE_PARENT=""
done
if [ -n "$DECOY_PROJECT" ]; then
	printf 'client source %s\n' "$NONCE" >"$DECOY_PROJECT/main.c"
	chmod 0755 "$DECOY_PROJECT"
	chmod 0644 "$DECOY_PROJECT/main.c"
fi

# Two fake host-services session dirs, shaped as a launch names them (MMP-D15): this
# session's, which the generated profile grants by exact path, and a sibling session's, which
# nothing grants with the shared /tmp closed (OQ-MM8). Readable by the account too.
SERVICES_DIR="/private/tmp/yolo-host-services-$(printf '%08x' "$$")-rc$NONCE"
SIBLING_DIR="/private/tmp/yolo-host-services-$(printf '%08x' "$$")-rcsib$NONCE"
for d in "$SERVICES_DIR" "$SIBLING_DIR"; do
	mkdir -m 0755 "$d" && printf 'ok\n' >"$d/demo.endpoint" && : >"$d/.session.lock" &&
		chmod 0644 "$d/demo.endpoint" "$d/.session.lock" || { echo "cannot create $d" >&2; exit 2; }
done
# Each fake session HOLDS its liveness lock, as a live launch does, for as long as this script
# runs. A launch reaps every session dir whose lock nobody holds (collectDeadServicesSessions,
# internal/cli/run/servicessession.go), so an unheld one made g4 fail whenever a yolo launch ran
# beside this script (the Mac run of 2026-11-03, §7). A holder exits with its parent, so a
# killed script releases them too; cleanup kills them first.
LOCK_PIDS=""
for d in "$SERVICES_DIR" "$SIBLING_DIR"; do
	/usr/bin/perl -MFcntl=:flock -e 'my $pp = getppid(); open(my $f, "<", $ARGV[0]) or die "open $ARGV[0]: $!\n"; flock($f, LOCK_SH | LOCK_NB) or die "flock $ARGV[0]: $!\n"; open(my $r, ">", $ARGV[1]) or die "$ARGV[1]: $!\n"; close $r; sleep 1 while getppid() == $pp' \
		"$d/.session.lock" "$d.held" &
	LOCK_PIDS="$LOCK_PIDS $!"
done
for d in "$SERVICES_DIR" "$SIBLING_DIR"; do
	n=0
	until [ -e "$d.held" ]; do
		n=$((n + 1))
		[ "$n" -gt 100 ] && { echo "could not hold the fake session lock $d/.session.lock" >&2; exit 2; }
		sleep 0.05
	done
	rm -f "$d.held"
done

# A /tmp file of yours, which the closed shared /tmp must hide (b14, OQ-MM8).
TMP_DECOY="/private/tmp/yolo-prototype-tmpdecoy-$NONCE"
printf 'tmp decoy %s\n' "$NONCE" >"$TMP_DECOY"
chmod 0600 "$TMP_DECOY"

# A long-lived process of yours outside the sandbox (b12, b13), with a distinctive
# argv: 600.<nonce> seconds is a number macOS sleep accepts. The first Mac run's
# 86<nonce> was not, so sleep printed its usage and exited before b12 and b13 ran.
DECOY_SLEEP_ARG="600.$NONCE"
/bin/sleep "$DECOY_SLEEP_ARG" &
DECOY_PID=$!

# A throwaway generic password in your login keychain, added and deleted as you in
# both modes, for the keychain check (b4). -T lets /usr/bin/security read it with
# no prompt, so only the sandbox stands between the check and the secret.
LOGIN_KC="$HOST_HOME/Library/Keychains/login.keychain-db"
KC_SERVICE="yolo-prototype-$NONCE"
KC_SECRET="yolo-prototype-secret-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
KC_ADDED=0
if KC_ADD_ERR="$(security add-generic-password -s "$KC_SERVICE" -a yolo -w "$KC_SECRET" -T /usr/bin/security "$LOGIN_KC" 2>&1)"; then
	KC_ADDED=1
fi

# A reader of another process's argv that needs no setuid binary: kern.procargs2
# through sysctl(3), for b13 next to ps (/bin/ps is setuid root).
PROCARGS_PY="$WORKSPACE/.procargs.py"
cat >"$PROCARGS_PY" <<'PY'
import ctypes, sys
libc = ctypes.CDLL(None, use_errno=True)
mib = (ctypes.c_int * 3)(1, 49, int(sys.argv[1]))  # CTL_KERN, KERN_PROCARGS2, pid
size = ctypes.c_size_t(0)
if libc.sysctl(mib, 3, None, ctypes.byref(size), None, 0) != 0:
    sys.exit("sysctl kern.procargs2: errno %d" % ctypes.get_errno())
buf = ctypes.create_string_buffer(size.value)
if libc.sysctl(mib, 3, buf, ctypes.byref(size), None, 0) != 0:
    sys.exit("sysctl kern.procargs2: errno %d" % ctypes.get_errno())
print(buf.raw[4:size.value].replace(b"\0", b" ").decode(errors="replace"))
PY
chmod 0644 "$PROCARGS_PY"

LISTENER_PID=""
G7_PID=""
CLAUDE_LABEL="yolo.prototype.$NONCE"

if [ "$MODE" = account ]; then
	if [ "$NARROW" = 1 ]; then
		# Traverse on ROOT, and full, inherited access below it, for the account; you keep yours.
		chmod +a "$ACCOUNT allow search,list,readattr,readextattr,readsecurity" "$ROOT"
		chmod -R +a "$ACCOUNT allow read,write,execute,delete,append,list,search,add_file,add_subdirectory,delete_child,readattr,writeattr,readextattr,writeextattr,readsecurity,file_inherit,directory_inherit" \
			"$WORKSPACE" "$SANDBOX_TMP"
		chmod +a "$ACCOUNT allow read,readattr,readextattr,readsecurity" "$PROFILE" "$PROCARGS_PY"
	else
		sudo chown -R "$ACCOUNT" "$WORKSPACE" "$SANDBOX_TMP"
	fi
	DARWIN_TMP="$("${AS[@]}" /usr/bin/getconf DARWIN_USER_TEMP_DIR 2>/dev/null)"
else
	DARWIN_TMP="$(/usr/bin/getconf DARWIN_USER_TEMP_DIR 2>/dev/null)"
fi
DARWIN_TMP="${DARWIN_TMP%/}"
if [ -n "$DARWIN_TMP" ] && [ -d "$DARWIN_TMP" ]; then
	DARWIN_TMP="$(cd "$DARWIN_TMP" 2>/dev/null && pwd -P || echo "$DARWIN_TMP")"
else
	# The account's temp dir may be unreadable to you; fall back to its unresolved form.
	DARWIN_TMP="${DARWIN_TMP:-$SANDBOX_TMP}"
	case "$DARWIN_TMP" in /var/*) DARWIN_TMP="/private$DARWIN_TMP" ;; esac
fi

# shellcheck disable=SC2329  # invoked by the EXIT trap
cleanup() {
	[ -n "$LISTENER_PID" ] && kill "$LISTENER_PID" 2>/dev/null
	[ -n "$G7_PID" ] && kill "$G7_PID" 2>/dev/null
	kill "$DECOY_PID" 2>/dev/null
	# shellcheck disable=SC2086  # a list of pids
	[ -n "$LOCK_PIDS" ] && kill $LOCK_PIDS 2>/dev/null
	[ "$KC_ADDED" = 1 ] && security delete-generic-password -s "$KC_SERVICE" -a yolo "$LOGIN_KC" >/dev/null 2>&1
	launchctl remove "$CLAUDE_LABEL" 2>/dev/null
	rm -f "$DECOY_SECRET" "$TMP_DECOY"
	rm -rf "$SERVICES_DIR" "$SIBLING_DIR"
	if [ -n "$DECOY_PROJECT" ]; then
		rm -rf "$DECOY_PROJECT"
		[ -n "$DECOY_PROJECT_MADE_PARENT" ] && rmdir "$DECOY_PROJECT_MADE_PARENT" 2>/dev/null
	fi
	if [ "$KEEP" = 1 ]; then
		echo
		echo "Kept $ROOT (--keep). Remove it with: ${AS[*]:+sudo }rm -rf '$ROOT'"
		return
	fi
	if [ "$MODE" = account ] && [ "$NARROW" = 1 ]; then
		# The account deletes what it made (its own files, under grants you set); then you delete the rest.
		"${AS[@]}" /bin/rm -rf "$WORKSPACE" "$SANDBOX_TMP" 2>/dev/null
		rm -rf "$ROOT"
	elif [ "$MODE" = account ]; then
		sudo rm -rf "$ROOT"
		[ "$MADE_SHARED" = 1 ] && { rmdir /Users/Shared/yolo 2>/dev/null || sudo rmdir /Users/Shared/yolo 2>/dev/null; }
	else
		rm -rf "$ROOT"
	fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# Ancestors of ROOT (everything above the workspace, home and temp dir).
ANCESTORS=()
d="$ROOT"
while [ "$d" != "/" ]; do
	ANCESTORS=("$d" "${ANCESTORS[@]+"${ANCESTORS[@]}"}")
	d="$(dirname "$d")"
done
if [ -z "$YOLO_BIN" ] && [ "${#ANCESTORS[@]}" -gt 8 ]; then
	echo "The temp root $ROOT is more than 8 directories deep; the profile has 8 ancestor slots." >&2
	exit 2
fi

# A free loopback port for check (d), and one for the in-sandbox listener (INFO).
port_free() { ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
pick_port() {
	local p i
	for i in 1 2 3 4 5 6 7 8 9 10; do
		p=$((20000 + RANDOM % 20000))
		if port_free "$p"; then echo "$p"; return 0; fi
	done
	return 1
}
LOOP_PORT="$(pick_port)" || { echo "could not find a free loopback port" >&2; exit 2; }
INNER_PORT="$(pick_port)" || { echo "could not find a free loopback port" >&2; exit 2; }

SB_PATH="/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin:/nix/var/nix/profiles/default/bin:/run/current-system/sw/bin"

# render_profile [FLAG...]: with --profile-from-yolo, render the generated profile for this
# run's workspace, home, temp dirs and services dir, with each extra FLAG of the verb (for
# example --shared-tmp shared), into its own file, and point PROFILE at it. The generated
# profile takes no -D parameters: its paths are rendered into the text (MMP-D1).
render_profile() {
	local name="profile-yolo"
	local args=(internal macos-user-profile --mode "$MODE" --workspace "$WORKSPACE" --home "$AGENT_HOME"
		--host-home "$HOST_HOME" --tmp "$SANDBOX_TMP" --darwin-tmp "$DARWIN_TMP"
		--services-dir "$SERVICES_DIR")
	[ "$MODE" = account ] && args+=(--account "$ACCOUNT")
	if [ $# -gt 0 ]; then
		args+=("$@")
		name="$name$(printf -- '-%s' "$@" | tr -c 'A-Za-z0-9.\n-' '-')"
	fi
	PROFILE="$ROOT/$name.sb"
	if ! "$YOLO_BIN" "${args[@]}" >"$PROFILE"; then
		echo "rendering the profile failed: $YOLO_BIN ${args[*]}" >&2
		exit 2
	fi
	chmod 0644 "$PROFILE"
}

# build_params [FLAG...] -> fills PARAMS for profile.sb, or renders the generated profile
# (PARAMS empty) with --profile-from-yolo, passing each FLAG to the verb.
build_params() {
	local i
	if [ -n "$YOLO_BIN" ]; then
		render_profile "$@"
		PARAMS=()
		return
	fi
	PARAMS=(-D "WORKSPACE=$WORKSPACE" -D "AGENT_HOME=$AGENT_HOME" -D "HOST_HOME=$HOST_HOME"
		-D "SANDBOX_TMP=$SANDBOX_TMP" -D "DARWIN_USER_TEMP_DIR=$DARWIN_TMP")
	for i in 1 2 3 4 5 6 7 8; do
		PARAMS+=(-D "ANCESTOR_$i=${ANCESTORS[$((i - 1))]:-/}")
	done
}
build_params
DEFAULT_PARAMS=(${PARAMS[@]+"${PARAMS[@]}"})
DEFAULT_PROFILE="$PROFILE"

# use_default: the profile and parameters every check runs under unless it says otherwise.
use_default() {
	PARAMS=(${DEFAULT_PARAMS[@]+"${DEFAULT_PARAMS[@]}"})
	PROFILE="$DEFAULT_PROFILE"
}

# Env for every inside (and control) run: a clean environment, HOME = the agent's.
SB_USER="$(id -un)"
[ "$MODE" = account ] && SB_USER="$ACCOUNT"
SB_ENV=(HOME="$AGENT_HOME" PATH="$SB_PATH" TMPDIR="$SANDBOX_TMP/" USER="$SB_USER" LOGNAME="$SB_USER"
	LANG="${LANG:-en_US.UTF-8}" TERM="${TERM:-xterm-256color}"
	HOST_HOME="$HOST_HOME" WORKSPACE="$WORKSPACE" NONCE="$NONCE")

# inside SCRIPT [extra env...]: run SCRIPT with bash under the profile, as the mode's user.
inside() {
	local script="$1"
	shift
	[ "$MODE" = account ] && [ "$NARROW" = 0 ] && sudo -v
	(cd "$WORKSPACE" && "${AS[@]+"${AS[@]}"}" /usr/bin/env -i "${SB_ENV[@]}" "$@" \
		/usr/bin/sandbox-exec -f "$PROFILE" ${PARAMS[@]+"${PARAMS[@]}"} /bin/bash -c "$script") 2>&1
}

# control SCRIPT [extra env...]: the same, without the profile (same user).
control() {
	local script="$1"
	shift
	[ "$MODE" = account ] && [ "$NARROW" = 0 ] && sudo -v
	(cd "$WORKSPACE" && "${AS[@]+"${AS[@]}"}" /usr/bin/env -i "${SB_ENV[@]}" "$@" \
		/bin/bash -c "$script") 2>&1
}

FAILS=0
PASSES=0
SKIPS=0
FAILED_IDS=()

show() { # show OUTPUT: indent captured output
	if [ -n "$1" ]; then printf '%s\n' "$1" | sed 's/^/    | /'; fi
}

result() { # result PASS|FAIL|SKIP|INFO ID DESC
	case "$1" in
	PASS) PASSES=$((PASSES + 1)) ;;
	FAIL) FAILS=$((FAILS + 1)); FAILED_IDS+=("$2") ;;
	SKIP) SKIPS=$((SKIPS + 1)) ;;
	esac
	printf '%-4s %-4s %s\n' "$1" "$2" "$3"
}

# expect_allow ID DESC SCRIPT [env...]: PASS if SCRIPT exits 0 inside the sandbox.
expect_allow() {
	local id="$1" desc="$2" script="$3" out rc
	shift 3
	use_default
	out="$(inside "$script" "$@")"
	rc=$?
	if [ $rc -eq 0 ]; then result PASS "$id" "$desc"; else result FAIL "$id" "$desc (exit $rc)"; fi
	printf '    $ %s\n' "$script"
	show "$out"
}

# expect_deny ID DESC SCRIPT CONTROL(yes|no) [env...]: SCRIPT exits 0 only when the access WORKED.
# PASS if it fails inside. With CONTROL=yes the same script runs first without the profile, so a
# refusal can be attributed to the profile; a control that also fails is reported, not counted.
expect_deny() {
	local id="$1" desc="$2" script="$3" ctl="$4" out rc cout crc note=""
	shift 4
	use_default
	if [ "$ctl" = yes ]; then
		cout="$(control "$script" "$@")"
		crc=$?
		if [ $crc -eq 0 ]; then
			note="control without the profile: allowed, so a refusal is the profile's"
		else
			note="control without the profile: ALSO denied (exit $crc), so a refusal here is not the profile's alone"
		fi
	else
		note="no control run (running it outside the sandbox would act on your session)"
	fi
	out="$(inside "$script" "$@")"
	rc=$?
	if [ $rc -ne 0 ]; then result PASS "$id" "$desc"; else result FAIL "$id" "$desc (the access WORKED inside the sandbox)"; fi
	printf '    $ %s\n' "$script"
	show "$out"
	printf '    (%s)\n' "$note"
	[ "$ctl" = yes ] && [ $crc -ne 0 ] && show "$cout"
}

info() { # info ID DESC SCRIPT: run inside, report the outcome without counting it
	local id="$1" desc="$2" script="$3" out rc
	use_default
	out="$(inside "$script")"
	rc=$?
	result INFO "$id" "$desc: $([ $rc -eq 0 ] && echo "worked" || echo "refused (exit $rc)")"
	printf '    $ %s\n' "$script"
	show "$out"
}

skip() { result SKIP "$1" "$2"; }

echo "== macos-user model prototype: mode=$MODE${AS[*]:+ account=$ACCOUNT}"
echo "   macOS $(sw_vers -productVersion 2>/dev/null) ($(uname -m)); nonce $NONCE"
echo "   WORKSPACE=$WORKSPACE"
echo "   AGENT_HOME=$AGENT_HOME"
echo "   SANDBOX_TMP=$SANDBOX_TMP  DARWIN_USER_TEMP_DIR=$DARWIN_TMP"
echo "   decoy secret: $DECOY_SECRET"
echo "   decoy project: ${DECOY_PROJECT:-none (neither /opt/src nor /Library/Caches was writable)}"
echo "   host-services dir: $SERVICES_DIR (sibling $SIBLING_DIR)"
if [ -n "$YOLO_BIN" ]; then
	echo "   profile: generated by $YOLO_BIN internal macos-user-profile ($DEFAULT_PROFILE)"
else
	echo "   profile: profile.sb (beside this script)"
fi
echo

# --- 0: the profile loads at all -------------------------------------------
use_default
out="$(inside 'echo loaded')"
rc=$?
if [ $rc -ne 0 ] || [ "$out" != "loaded" ]; then
	result FAIL 0 "profile loads and runs /bin/bash (exit $rc)"
	show "$out"
	echo
	echo "The profile did not load or bash could not start under it; no further check is meaningful."
	echo "Paste the output above back. The sandbox log names the denied operation:"
	echo "  log show --last 2m --style compact --predicate 'sender == \"Sandbox\"' | tail -50"
	exit 1
fi
result PASS 0 "profile loads and runs /bin/bash"
echo

# --- (a) the agent's own files and tools -------------------------------------
echo "-- (a) workspace, home, tools"
expect_allow a1 "read and write the workspace" \
	'echo hi >"$WORKSPACE/a1.txt" && cat "$WORKSPACE/a1.txt" && rm "$WORKSPACE/a1.txt"'
expect_allow a2 "read and write AGENT_HOME (\$HOME)" \
	'mkdir -p "$HOME/.config/x" && echo hi >"$HOME/.config/x/f" && cat "$HOME/.config/x/f"'
expect_allow a3 "write TMPDIR" \
	'f=$(mktemp) && echo hi >"$f" && cat "$f" && rm "$f"'
expect_deny a3t "a write to the shared /tmp is refused (OQ-MM8)" \
	'echo hi >/tmp/yolo-prototype-a3t-$NONCE' yes
rm -f "/private/tmp/yolo-prototype-a3t-$NONCE"
if [ -n "$YOLO_BIN" ]; then
	# The macos_user_shared_tmp hatch's profile reopens /tmp, read and write.
	build_params --shared-tmp shared
	script='echo hi >/tmp/yolo-prototype-a3h-$NONCE && cat /tmp/yolo-prototype-a3h-$NONCE && rm /tmp/yolo-prototype-a3h-$NONCE'
	out="$(inside "$script")"
	rc=$?
	if [ $rc -eq 0 ]; then result PASS a3h "with the hatch, a write to /tmp works"; else result FAIL a3h "with the hatch, a write to /tmp works (exit $rc)"; fi
	printf '    $ %s   (under %s)\n' "$script" "$(basename "$PROFILE")"
	show "$out"
	use_default
else
	skip a3h "the shared-/tmp hatch is the generated profile's: add --profile-from-yolo <yolo>"
fi
# A Go client's TLS trust (PS-D15): Go asks macOS's trustd, and the profile reaches only its
# per-user agent (com.apple.trustd.agent), not the system daemon. An x509 or certificate error here,
# on a network whose inspecting proxy's CA the Mac trusts, is the evidence for adding
# com.apple.trustd back; paste it and the sandbox log's deny lines.
if command -v gh >/dev/null 2>&1; then
	info a4t "a Go client (gh) completes a TLS handshake to github.com (no login needed)" \
		'out=$(gh api --hostname github.com /zen 2>&1); echo "$out"; ! printf "%s" "$out" | grep -qiE "x509|certificate"'
else
	skip a4t "a Go client's TLS trust needs gh on PATH (PS-D15)"
fi
expect_allow a4 "signal a process inside the sandbox" \
	'sleep 30 & p=$!; kill $p || exit 1; wait $p; rc=$?; echo "child ended with $rc"; [ $rc -eq 143 ]'

GIT=""
for g in /opt/homebrew/bin/git /usr/local/bin/git /run/current-system/sw/bin/git /nix/var/nix/profiles/default/bin/git; do
	[ -x "$g" ] && { GIT="$g"; break; }
done
if [ -z "$GIT" ] && /usr/bin/xcode-select -p >/dev/null 2>&1; then GIT=/usr/bin/git; fi
if [ -n "$GIT" ]; then
	expect_allow a5 "git init and commit in the workspace ($GIT)" \
		"cd \"\$WORKSPACE\" && mkdir -p repo && cd repo && $GIT init -q && echo x >f && $GIT add f && $GIT -c user.name=p -c user.email=p@example.com commit -qm init && $GIT log --oneline"
else
	skip a5 "git: none found (no Homebrew/nix git, no Command Line Tools)"
fi

if /usr/bin/xcode-select -p >/dev/null 2>&1; then
	expect_allow a6 "a build: compile and run C with the Command Line Tools" \
		'cd "$WORKSPACE" && printf "int main(void){return 0;}\n" >m.c && cc -o m m.c && ./m && echo built'
else
	skip a6 "build: Command Line Tools not installed (xcode-select -p failed)"
fi

BREW_BIN=""
for b in jq rg fd wget gh git curl python3 node; do
	[ -x "/opt/homebrew/bin/$b" ] && { BREW_BIN="/opt/homebrew/bin/$b"; break; }
done
if [ -n "$BREW_BIN" ]; then
	expect_allow a7 "run a Homebrew binary ($BREW_BIN --version)" "$BREW_BIN --version"
else
	skip a7 "Homebrew binary: none of the probed tools under /opt/homebrew/bin"
fi

if [ -x /nix/var/nix/profiles/default/bin/nix ]; then
	expect_allow a8 "run a nix-store binary (nix --version)" '/nix/var/nix/profiles/default/bin/nix --version'
else
	skip a8 "nix: /nix/var/nix/profiles/default/bin/nix not present"
fi

expect_allow a9 "HTTPS with certificate trust (curl -sI https://github.com)" \
	'curl -sSI --max-time 20 https://github.com | head -1'
if [ -x /opt/homebrew/bin/gh ] || [ -x /run/current-system/sw/bin/gh ] || [ -x /nix/var/nix/profiles/default/bin/gh ]; then
	expect_allow a10 "gh runs (gh --version)" 'gh --version'
else
	skip a10 "gh: not installed under Homebrew or nix"
fi
if [ -n "$GIT" ]; then
	expect_allow a11 "git over HTTPS (git ls-remote https://github.com/git/git HEAD)" \
		"$GIT ls-remote https://github.com/git/git HEAD"
else
	skip a11 "git ls-remote: no git"
fi
expect_allow a12 "read the system fonts (/System/Library/Fonts, /Library/Fonts)" \
	'ls /System/Library/Fonts | head -3 && ls /Library/Fonts >/dev/null'
echo

# --- (b) your secrets and your session ----------------------------------------
echo "-- (b) host home, keychain, ssh agent, clipboard, Apple Events, launchd, signals"
expect_deny b1 "read the decoy secret in your home" 'cat "'"$DECOY_SECRET"'"' yes
if [ -d "$HOST_HOME/.ssh" ]; then
	expect_deny b2 "list ~/.ssh" 'ls -la "$HOST_HOME/.ssh"' yes
else
	skip b2 "$HOST_HOME/.ssh does not exist"
fi
expect_deny b3 "write a file in your home" 'touch "$HOST_HOME/yolo-prototype-write-$NONCE" && rm -f "$HOST_HOME/yolo-prototype-write-$NONCE"' yes
# b4: read the throwaway item added to your login keychain above. An exit code cannot
# tell a refusal from "not found" (the first Mac run got 44 either way), so the only
# verdict is whether the secret itself comes out. Tried through the keychain search
# list and by the login keychain's path; in account mode the read runs as the account.
if [ "$KC_ADDED" = 1 ]; then
	KC_READ='security find-generic-password -s "yolo-prototype-$NONCE" -w; security find-generic-password -s "yolo-prototype-$NONCE" -w "$HOST_HOME/Library/Keychains/login.keychain-db"'
	use_default
	cout="$(control "$KC_READ")"
	out="$(inside "$KC_READ")"
	case "$out" in
	*"$KC_SECRET"*) result FAIL b4 "read a login-keychain item (security find-generic-password -w): the secret was PRINTED" ;;
	*) result PASS b4 "read a login-keychain item (security find-generic-password -w)" ;;
	esac
	printf '    $ %s\n' "$KC_READ"
	show "${out//"$KC_SECRET"/<the secret>}"
	case "$cout" in
	*"$KC_SECRET"*) echo "    (control without the profile: printed the secret, so a refusal is the profile's)" ;;
	*)
		echo "    (control without the profile: did NOT print the secret either, so a refusal here is not the profile's alone)"
		show "$cout"
		;;
	esac
else
	skip b4 "could not add a throwaway item to your login keychain (locked?): $KC_ADD_ERR"
fi
expect_deny b5 "read the login keychain file" 'head -c 16 "$HOST_HOME/Library/Keychains/login.keychain-db" >/dev/null' yes
if [ -n "${SSH_AUTH_SOCK:-}" ]; then
	# ssh-add -l: 0 = keys listed, 1 = agent reached but empty, 2 = could not connect.
	expect_deny b6 "reach the ssh agent (\$SSH_AUTH_SOCK)" \
		'ssh-add -l; rc=$?; echo "ssh-add exit $rc"; [ $rc -eq 0 ] || [ $rc -eq 1 ]' yes SSH_AUTH_SOCK="$SSH_AUTH_SOCK"
else
	skip b6 "SSH_AUTH_SOCK is not set in this terminal"
fi
CLIP="$(pbpaste 2>/dev/null | head -c 200)"
if [ -n "$CLIP" ] && [ "$MODE" = self ]; then
	export CLIP
	expect_deny b7 "read your clipboard (pbpaste)" \
		'out="$(pbpaste 2>&1 | head -c 200)"; echo "pbpaste printed ${#out} bytes"; [ -n "$out" ] && [ "$out" = "$CLIP" ]' no CLIP="$CLIP"
elif [ "$MODE" = account ]; then
	expect_deny b7 "read a clipboard (pbpaste, as the account)" \
		'out="$(pbpaste 2>&1)"; rc=$?; echo "pbpaste exit $rc, ${#out} bytes"; [ $rc -eq 0 ] && [ -n "$out" ]' yes
else
	skip b7 "clipboard holds no text: copy any text, then rerun to cover pbpaste"
fi
# count windows, not get name: AppleScript answers an application's name from the specifier
# without sending Finder an event, so `get name` succeeds under any profile (measured 2026-10-23).
expect_deny b8 "drive Finder with Apple Events (osascript)" \
	"osascript -e 'tell application \"Finder\" to count windows'" no
expect_deny b9 "run a shell through System Events (osascript do shell script)" \
	"osascript -e 'tell application \"System Events\" to do shell script \"id\"'" no
expect_deny b10 "open an app through LaunchServices (open -g -a TextEdit)" 'open -g -a TextEdit' no
# launchctl submit: the job is visible from outside only if the submit escaped.
use_default
out="$(inside "launchctl submit -l $CLAUDE_LABEL -- /usr/bin/true; echo \"launchctl exit \$?\"")"
if "${AS[@]+"${AS[@]}"}" launchctl list "$CLAUDE_LABEL" >/dev/null 2>&1; then
	result FAIL b11 "submit a launchd job (launchctl submit): the job exists outside the sandbox"
else
	result PASS b11 "submit a launchd job (launchctl submit)"
fi
printf '    $ launchctl submit -l %s -- /usr/bin/true\n' "$CLAUDE_LABEL"
show "$out"
"${AS[@]+"${AS[@]}"}" launchctl remove "$CLAUDE_LABEL" 2>/dev/null
expect_deny b12 "signal one of your processes (kill -0 $DECOY_PID)" "kill -0 $DECOY_PID" yes
# b13: ps, then kern.procargs2 directly. /bin/ps is setuid root and the first Mac run
# showed the sandbox refusing to exec it at all, which proves nothing about procargs.
B13_PY=""
/usr/bin/xcode-select -p >/dev/null 2>&1 && B13_PY="; /usr/bin/python3 \"\$WORKSPACE/.procargs.py\" $DECOY_PID"
expect_deny b13 "read another process's argv (ps, then sysctl kern.procargs2, of pid $DECOY_PID)" \
	"out=\$( { ps -o command= -p $DECOY_PID$B13_PY; } 2>&1 ); printf '%s\n' \"\$out\"; case \"\$out\" in *$DECOY_SLEEP_ARG*) exit 0 ;; esac; exit 1" yes
expect_deny b14 "read a 0600 file of yours in the shared /tmp (OQ-MM8)" "cat $TMP_DECOY" yes
echo

# --- (c) outside /Users ---------------------------------------------------------
echo "-- (c) a project outside /Users, and the software trees"
if [ -n "$DECOY_PROJECT" ]; then
	expect_deny c1 "read the decoy project ($DECOY_PROJECT)" "cat '$DECOY_PROJECT/main.c'" yes
	expect_deny c2 "list the decoy project's parent ($(dirname "$DECOY_PROJECT"))" "ls '$(dirname "$DECOY_PROJECT")'" yes
else
	skip c1 "decoy project: neither /opt/src nor /Library/Caches is writable without root"
fi
if [ -d /opt/homebrew ]; then
	expect_allow c3 "read /opt/homebrew" 'ls /opt/homebrew/bin >/dev/null && echo ok'
else
	skip c3 "/opt/homebrew not present"
fi
STORE_PATH="$(cd /nix/var/nix/profiles/default 2>/dev/null && pwd -P)"
case "$STORE_PATH" in
/nix/store/*) expect_allow c4 "read /nix/store ($STORE_PATH)" "ls '$STORE_PATH' && echo ok" ;;
*) skip c4 "/nix/store: no default nix profile to find a store path from" ;;
esac
echo

# --- (d) loopback ------------------------------------------------------------------
echo "-- (d) loopback"
/usr/bin/nc -lk 127.0.0.1 "$LOOP_PORT" >/dev/null 2>&1 &
LISTENER_PID=$!
sleep 1
if port_free "$LOOP_PORT" && /usr/bin/xcode-select -p >/dev/null 2>&1; then
	# This nc may not take a bind address; fall back to Python's server.
	kill "$LISTENER_PID" 2>/dev/null
	(cd "$SANDBOX_TMP" && exec /usr/bin/python3 -m http.server --bind 127.0.0.1 "$LOOP_PORT") >/dev/null 2>&1 &
	LISTENER_PID=$!
	sleep 2
fi
# OQ-MM7 (ruled 2026-10-23) allows the whole loopback: no port is listed anywhere.
if port_free "$LOOP_PORT"; then
	skip d1 "could not start a loopback listener on $LOOP_PORT (nc -lk)"
else
	expect_allow d1 "your loopback listener on $LOOP_PORT is reachable (OQ-MM7)" \
		"(exec 3<>/dev/tcp/127.0.0.1/$LOOP_PORT) && echo connected"
fi
kill "$LISTENER_PID" 2>/dev/null
LISTENER_PID=""
expect_allow d3 "an agent's own loopback server (listen and connect inside)" \
	"/usr/bin/nc -lk 127.0.0.1 $INNER_PORT >/dev/null 2>&1 & p=\$!; sleep 1; (exec 3<>/dev/tcp/127.0.0.1/$INNER_PORT) && echo connected; rc=\$?; kill \$p; exit \$rc"
echo

# --- (g) unix sockets and the session's host-services dir ----------------------------
echo "-- (g) unix sockets, host-services dir"
UNIX_PROBE='rm -f "$1"; /usr/bin/perl -MIO::Socket::UNIX -e '"'"'$l = IO::Socket::UNIX->new(Local => $ARGV[0], Listen => 1) or die "bind: $!\n"; IO::Socket::UNIX->new(Peer => $ARGV[0]) or die "connect: $!\n"; $l->accept or die "accept: $!\n"; print "connected\n"'"'"' "$1"; rc=$?; rm -f "$1"; exit $rc'
expect_allow g1 "bind and connect a unix socket under \$TMPDIR (MMP-D16)" \
	"set -- \"\$TMPDIR\"g1.sock; $UNIX_PROBE"
expect_allow g2 "bind and connect a unix socket in the workspace by a relative path" \
	"set -- .g2.sock; $UNIX_PROBE"
# g7, g8: a symlink in the workspace to a socket OUTSIDE the roots must not connect (MMP-D16):
# Seatbelt matches the socket's resolved path, so the link does not carry the unix-socket allow
# out of the workspace. g7 uses a listener of this run's beside the workspace; g8 a real
# container socket (colima, Docker Desktop) when this Mac has one.
CONNECT_PROBE='/usr/bin/perl -MIO::Socket::UNIX -e '"'"'IO::Socket::UNIX->new(Peer => $ARGV[0]) or die "connect: $!\n"; print "connected\n"'"'"' "$1"'
G7_SOCK="$ROOT/outside-g7.sock"
/usr/bin/perl -MIO::Socket::UNIX -e '$l = IO::Socket::UNIX->new(Local => $ARGV[0], Listen => 5) or die "bind: $!\n"; while (my $c = $l->accept) { close $c }' "$G7_SOCK" &
G7_PID=$!
sleep 1
ln -sfn "$G7_SOCK" "$WORKSPACE/g7.sock"
expect_deny g7 "a workspace symlink to a socket outside the roots does not connect ($G7_SOCK)" \
	"set -- g7.sock; $CONNECT_PROBE" yes
kill "$G7_PID" 2>/dev/null
G7_PID=""
rm -f "$WORKSPACE/g7.sock" "$G7_SOCK"
G8_SOCK=""
for s in "$HOST_HOME/.colima/default/docker.sock" "$HOST_HOME/.docker/run/docker.sock"; do
	[ -S "$s" ] && { G8_SOCK="$s"; break; }
done
if [ -n "$G8_SOCK" ]; then
	ln -sfn "$G8_SOCK" "$WORKSPACE/g8.sock"
	expect_deny g8 "a workspace symlink to your container socket does not connect ($G8_SOCK)" \
		"set -- g8.sock; $CONNECT_PROBE" yes
	rm -f "$WORKSPACE/g8.sock"
else
	skip g8 "no colima or Docker Desktop socket on this Mac"
fi
if [ -n "$YOLO_BIN" ]; then
	expect_deny g3 "the session's liveness lock is refused ($SERVICES_DIR/.session.lock)" \
		"cat '$SERVICES_DIR/.session.lock'" yes
	# The shared /tmp is closed (OQ-MM8), so the exact-path grant stands alone (MMP-D15).
	expect_allow g4 "read this session's endpoint file with the shared /tmp closed" \
		"cat '$SERVICES_DIR/demo.endpoint'"
	expect_deny g5 "a sibling session's endpoint file is unreadable" "cat '$SIBLING_DIR/demo.endpoint'" yes
	expect_deny g6 "/private/tmp cannot be listed (that would name every session's dir)" \
		"ls /private/tmp >/dev/null" yes
else
	skip g3 "the host-services grant is the generated profile's: add --profile-from-yolo <yolo>"
fi
echo

# --- (e) Full Disk Access ------------------------------------------------------------
echo "-- (e) Full Disk Access"
TCC_DB="$HOST_HOME/Library/Application Support/com.apple.TCC/TCC.db"
if [ "$MODE" = account ]; then
	skip e1 "Full Disk Access check runs in self mode only"
elif head -c 16 "$TCC_DB" >/dev/null 2>&1; then
	echo "    Full Disk Access APPEARS GRANTED to this terminal (it can read TCC.db). Confirm in"
	echo "    System Settings > Privacy & Security > Full Disk Access."
	expect_deny e1 "with Full Disk Access, a profile-denied file stays unreadable (TCC.db)" \
		'head -c 16 "$HOST_HOME/Library/Application Support/com.apple.TCC/TCC.db" >/dev/null' yes
	expect_deny e2 "with Full Disk Access, the decoy secret stays unreadable" 'cat "'"$DECOY_SECRET"'"' yes
else
	echo "    Full Disk Access appears NOT granted to this terminal (TCC.db is unreadable)."
	skip e1 "grant this terminal Full Disk Access, restart it, and rerun to cover (e)"
fi
echo

# --- (f) Claude, optional -------------------------------------------------------------
echo "-- (f) Claude Code (optional)"
if [ "$WITH_CLAUDE" = 1 ]; then
	expect_allow f1 "install Claude Code into the sandbox's own HOME and run claude --version" \
		'curl -fsSL https://claude.ai/install.sh | bash >"$TMPDIR/claude-install.log" 2>&1; tail -3 "$TMPDIR/claude-install.log"; "$HOME/.local/bin/claude" --version'
	echo "    Manual: Claude login with the keychain denied. Rerun with --with-claude --keep, then run the"
	echo "    printed sandboxed shell, start 'claude', log in, quit, start it again: it must not ask"
	echo "    to log in again, and ~/.claude/.credentials.json must exist under AGENT_HOME. Paste"
	echo "    whether it stayed logged in; never paste the credentials file."
else
	skip f1 "pass --with-claude to cover claude --version (no login, no API call)"
fi
echo

if [ "$KEEP" = 1 ]; then
	echo "Manual sandboxed shell (the same profile and parameters as the checks):"
	printf '  cd %q && %s /usr/bin/env -i' "$WORKSPACE" "${AS[*]:-}"
	printf ' %q' "${SB_ENV[@]}"
	printf ' /usr/bin/sandbox-exec -f %q' "$DEFAULT_PROFILE"
	if [ "${#DEFAULT_PARAMS[@]}" -gt 0 ]; then printf ' %q' "${DEFAULT_PARAMS[@]}"; fi
	printf ' /bin/bash -l\n'
	echo "  (the decoys are removed when this script exits; the workspace and home are kept)"
	echo
fi

echo "== summary: $PASSES PASS, $FAILS FAIL, $SKIPS SKIP (mode=$MODE)"
if [ "$FAILS" -gt 0 ]; then
	echo "   failed: ${FAILED_IDS[*]}"
	echo "   For each FAIL that is a broken tool (not an escape), the sandbox log names the denied operation:"
	echo "     log show --last 5m --style compact --predicate 'sender == \"Sandbox\"' | grep -i deny | tail -50"
	echo "   Paste this whole output and that log excerpt back."
	exit 1
fi
echo "   Paste this whole output back."
exit 0
