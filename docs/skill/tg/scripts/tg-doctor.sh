#!/usr/bin/env bash
# tg-doctor.sh: one-call check of the tg environment. Read only: it starts,
# stops and changes nothing, and never prints secrets. It calls only commands
# that do not touch configuration or session files: tg version, tg config path,
# tg config show and tg daemon status. It lists the profile directories by name
# and never opens their files.
#
# Prints: tg path and version, the configuration file path, every profile found
# on disk with its Daemon state, the details of the selected profile, and a last
# line "verdict: <word> profile=<name>".
#
# Usage:
#   bash tg-doctor.sh [--profile <name>]
# Without --profile the selected profile is the configured default
# (default_profile in config.toml). Every other profile is listed, never guessed.
# Requires Bash 3.2+; python3 (JSON parsing) is optional; Linux or macOS.
#
# Verdicts and exit codes. Read the verdict, not the code: the codes are not tg codes.
#   ready                       0  the Daemon of the profile runs and the account is logged in
#   daemon_stopped              1  no Daemon answers for the profile (start it)
#   daemon_connecting           2  the Daemon runs but is not connected yet (wait, check again)
#   login_pending               2  a login is in progress (the human finishes it)
#   login_missing               2  the Daemon runs, no account is logged in or the login was lost
#   daemon_running_login_unknown 2 the Daemon runs; python3 is missing, so the login is not read
#   status_unreadable           2  tg could not answer (not "stopped": do not start a Daemon)
#   no_tg                       2  tg is not in PATH
#   bad_arguments               2  wrong doctor arguments

set -uo pipefail

PROFILE=''

usage() { sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
    case "$1" in
        --profile) PROFILE="${2:?--profile needs a value}"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) printf 'unknown option: %s\n' "$1" >&2; printf 'verdict: bad_arguments profile=-\n'; exit 2 ;;
    esac
done

PROFILE_RE='^[a-z0-9][a-z0-9_-]{0,31}$'
if [ -n "$PROFILE" ] && [[ ! "$PROFILE" =~ $PROFILE_RE ]]; then
    printf 'invalid profile name: use 1-32 chars of a-z, 0-9, _ and -\n' >&2
    printf 'verdict: bad_arguments profile=-\n'
    exit 2
fi

section() { printf '\n== %s ==\n' "$1"; }
note() { printf '   %s\n' "$*"; }

HAVE_PY=0
if command -v python3 >/dev/null 2>&1 && python3 -I -c 'pass' >/dev/null 2>&1; then
    HAVE_PY=1
fi

# json_get <json> <dotted.path> [type]: print a scalar; exit 1 when absent,
# when ok is false, when python3 is missing, or when the value has a different
# type (boolean, string, number).
json_get() {
    [ "$HAVE_PY" = 1 ] || return 1
    printf '%s' "$1" | python3 -I -c '
import json, sys
try:
    doc = json.load(sys.stdin)
    if doc.get("ok") is False:
        sys.exit(1)
    v = doc
    for k in sys.argv[1].lstrip(".").split("."):
        v = v[k]
    want = {"boolean": bool, "string": str, "number": (int, float)}.get(sys.argv[2])
    if want and not isinstance(v, want):
        sys.exit(1)
    print(str(v).lower() if isinstance(v, bool) else v)
except Exception:
    sys.exit(1)
' "$2" "${3:-}"
}

# json_error <json>: print error.message from an error envelope, if there is one.
json_error() {
    [ "$HAVE_PY" = 1 ] || return 1
    printf '%s' "$1" | python3 -I -c '
import json, sys
try:
    print(json.load(sys.stdin)["error"]["message"])
except Exception:
    sys.exit(1)
'
}

verdict() { printf '\nverdict: %s profile=%s\n' "$1" "${2:--}"; }

section "CLI"
if ! command -v tg >/dev/null 2>&1; then
    note 'tg not found in PATH'
    note 'build and install the binary from the tg-cli repository: just install (it also stops and starts the default profile Daemon)'
    verdict no_tg
    exit 2
fi
note "binary:   $(command -v tg)"
version_json=$(tg version 2>/dev/null)
note "version:  $(json_get "$version_json" data.version string || echo unknown), revision $(json_get "$version_json" data.revision string | cut -c1-12)"
config_json=$(tg config path 2>/dev/null)
note "config:   $(json_get "$config_json" data.path string || echo unknown)"
[ "$HAVE_PY" = 1 ] || note 'python3 not usable: the state comes from exit codes only, the login is not read (tg auth status shows it)'

# probe <profile>: sets P_RC, P_JSON and P_STATE (running, stopped, unreadable).
# The state comes from the exit code alone: 0 the Daemon answered, 3 nobody
# answers on the socket of this profile. Any other code is "unreadable".
probe() {
    P_JSON=$(tg --profile "$1" daemon status 2>/dev/null)
    P_RC=$?
    case "$P_RC" in
        0) P_STATE=running ;;
        3) P_STATE=stopped ;;
        *) P_STATE=unreadable ;;
    esac
}

config_show=$(tg config show 2>/dev/null)
default_profile=$(json_get "$config_show" data.default_profile string || echo default)
home="${TG_CLI_HOME:-$(json_get "$config_show" data.home string || true)}"
session_file=$(json_get "$config_show" data.files.session string || echo session.json)
SELECTED="${PROFILE:-$default_profile}"

section "Profiles"
note "data directory: ${home:-unknown}"
note "default profile: $default_profile (selected: $SELECTED${PROFILE:+, by --profile})"
found=''
if [ -n "$home" ] && [ -d "$home" ]; then
    for dir in "$home"/*/; do
        [ -d "$dir" ] || continue
        name=$(basename "$dir")
        # db is the database root, models holds the speech engine: neither is a profile.
        [ "$name" = db ] || [ "$name" = models ] && continue
        [[ "$name" =~ $PROFILE_RE ]] || continue
        if [ -e "$dir/daemon.sock" ] || [ -e "$dir/$session_file" ]; then
            found="$found $name"
        fi
    done
fi
case " $found " in
    *" $SELECTED "*) ;;
    *) found="$found $SELECTED" ;;
esac
running_others=''
for name in $found; do
    mark=' '
    [ "$name" = "$SELECTED" ] && mark='*'
    probe "$name"
    case "$P_STATE" in
        running)
            line="Daemon running, pid $(json_get "$P_JSON" data.pid number || echo '?'), connection $(json_get "$P_JSON" data.connection string || echo '?')"
            user=$(json_get "$P_JSON" data.user.username string 2>/dev/null)
            [ -n "$user" ] && line="$line, @$user"
            [ "$name" != "$SELECTED" ] && running_others="$running_others $name"
            ;;
        stopped) line='no Daemon' ;;
        *) line="status unreadable (exit code $P_RC)" ;;
    esac
    note "$mark $name: $line"
done
note '(* marks the selected profile; give --profile to check another one)'
if [ -n "$running_others" ] && [ -z "$PROFILE" ]; then
    note "other profiles have a Daemon:$running_others. Ask the user which account the task is about and pass --profile <name> to every tg command; do not guess."
fi

section "Daemon of profile $SELECTED"
probe "$SELECTED"
status_json=$P_JSON
state=$P_STATE
case "$state" in
    running)
        note "running:    pid $(json_get "$status_json" data.pid), profile $(json_get "$status_json" data.profile), connection $(json_get "$status_json" data.connection)"
        note "transport:  $(json_get "$status_json" data.transport) (active $(json_get "$status_json" data.active_transport)), dc $(json_get "$status_json" data.dc)"
        note "active requests: $(json_get "$status_json" data.active_requests)"
        note "access filter: enabled=$(json_get "$status_json" data.access.enabled boolean), write list size $(json_get "$status_json" data.access.write number)"
        ;;
    stopped) note 'not running (exit code 3: nothing answers on the socket of this profile)' ;;
    *)
        note "cannot read the Daemon status (exit code $P_RC); this does not mean the Daemon is stopped"
        error_text=$(json_error "$status_json" 2>/dev/null)
        [ -n "$error_text" ] && note "tg says: $error_text"
        ;;
esac

section "Login"
result=unreadable
if [ "$state" = running ]; then
    connection=$(json_get "$status_json" data.connection string)
    auth_lost=$(json_get "$status_json" data.auth_lost string 2>/dev/null)
    login_state=$(json_get "$status_json" data.login_state string 2>/dev/null)
    if [ "$HAVE_PY" != 1 ]; then
        note 'unknown: python3 is not usable; run tg --profile '"$SELECTED"' auth status'
        result=login-unknown
    elif [ -n "$auth_lost" ]; then
        note "the login was lost (auth_lost: $auth_lost); the Daemon keeps running"
        result=no-login
    elif [ -n "$login_state" ] && [ "$login_state" != authorized ]; then
        note "a login is in progress (login_state: $login_state)"
        result=pending
    elif [ "$(json_get "$status_json" data.connected boolean)" != true ]; then
        note "the Daemon is not connected yet (connection: ${connection:-unknown}): the login state is unknown"
        result=connecting
    elif [ "$(json_get "$status_json" data.authorized boolean)" = true ]; then
        note "logged in as @$(json_get "$status_json" data.user.username) (id $(json_get "$status_json" data.user.id))"
        result=ready
    else
        note 'the Daemon runs but no account is logged in'
        result=no-login
    fi
elif [ "$state" = stopped ]; then
    note 'unknown: the login state is known only while the Daemon runs'
    result=stopped
else
    note 'unknown'
fi

section "Result"
case "$result" in
    ready)
        note "ready: run commands with --profile $SELECTED"
        verdict ready "$SELECTED"
        exit 0
        ;;
    connecting)
        note 'wait a few seconds and run this check again; do not start a second Daemon'
        verdict daemon_connecting "$SELECTED"
        exit 2
        ;;
    login-unknown)
        note "the Daemon runs; check the login: tg --profile $SELECTED auth status"
        verdict daemon_running_login_unknown "$SELECTED"
        exit 2
        ;;
    stopped)
        note "start the Daemon: tg --profile $SELECTED daemon start"
        note 'first setup uses the desktop preset by default (or TG_API_ID and TG_API_HASH); then the human logs in:'
        note "  ! tg --profile $SELECTED auth login   (in Claude Code)"
        verdict daemon_stopped "$SELECTED"
        exit 1
        ;;
    pending)
        note "the human finishes the pending login (code, password or QR scan) in the terminal where it runs, or with tg --profile $SELECTED auth code or auth password if it was started with --no-input; do not start another login"
        verdict login_pending "$SELECTED"
        exit 2
        ;;
    no-login)
        note "the human logs in again in the same Daemon: ! tg --profile $SELECTED auth login   (in Claude Code)"
        verdict login_missing "$SELECTED"
        exit 2
        ;;
    *)
        note 'fix the problem above, then run this check again; do not start a Daemon on an unreadable status'
        verdict status_unreadable "$SELECTED"
        exit 2
        ;;
esac
