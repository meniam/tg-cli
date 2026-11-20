#!/usr/bin/env bash
# tg-doctor.sh: one-call check of the tg environment. Read only: it starts,
# stops and changes nothing, and never prints secrets. It calls only commands
# that do not touch configuration or session files: tg version, tg config path
# and tg daemon status.
#
# Prints: tg path and version, the configuration file path, Daemon state,
# login state, and the next command to run.
#
# Usage:
#   bash tg-doctor.sh [--profile <name>]
# Without --profile, tg picks the profile itself (the configured default).
# Requires Bash 3.2+ and python3 (JSON parsing); Linux or macOS.
#
# Exit codes:
#   0  ready: the Daemon runs and the account is logged in
#   1  the Daemon is not running (start it, then log in if this is a first setup),
#      or it runs but is not connected yet (wait and run the check again)
#   2  cannot work: no tg or python3, an unreadable status, or no login

set -uo pipefail

PROFILE=''

usage() { sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
    case "$1" in
        --profile) PROFILE="${2:?--profile needs a value}"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) printf 'unknown option: %s\n' "$1" >&2; exit 2 ;;
    esac
done

if [ -n "$PROFILE" ] && [[ ! "$PROFILE" =~ ^[a-z0-9][a-z0-9_-]{0,31}$ ]]; then
    printf 'invalid profile name: use 1-32 chars of a-z, 0-9, _ and -\n' >&2
    exit 2
fi

section() { printf '\n== %s ==\n' "$1"; }
note() { printf '   %s\n' "$*"; }

# json_get <json> <dotted.path> [type]: print a scalar; exit 1 when absent,
# when ok is false, or when the value has a different type (boolean, string, number).
json_get() {
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
    printf '%s' "$1" | python3 -I -c '
import json, sys
try:
    print(json.load(sys.stdin)["error"]["message"])
except Exception:
    sys.exit(1)
'
}

TG=(tg)
profile_flag=''
if [ -n "$PROFILE" ]; then
    TG+=(--profile "$PROFILE")
    profile_flag=" --profile $PROFILE"
fi

section "CLI"
if ! command -v tg >/dev/null 2>&1; then
    note 'tg not found in PATH'
    note 'build and install the binary from the tg-cli repository: just install (it also stops and starts the default profile Daemon)'
    exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
    note 'python3 not found: it parses the JSON answers'
    exit 2
fi
note "binary:   $(command -v tg)"
version_json=$(tg version 2>/dev/null)
note "version:  $(json_get "$version_json" data.version string || echo unknown), revision $(json_get "$version_json" data.revision string | cut -c1-12)"
config_json=$(tg config path 2>/dev/null)
note "config:   $(json_get "$config_json" data.path string || echo unknown)"
note "profile:  ${PROFILE:-the configured default}"

section "Daemon"
status_json=$("${TG[@]}" daemon status 2>/dev/null)
status_rc=$?
state=unreadable
case "$status_rc" in
    0)
        if [ "$(json_get "$status_json" data.connected boolean)" != '' ]; then
            state=running
        fi
        ;;
    3) state=stopped ;;
esac

case "$state" in
    running)
        note "running:    pid $(json_get "$status_json" data.pid), profile $(json_get "$status_json" data.profile), connection $(json_get "$status_json" data.connection)"
        note "transport:  $(json_get "$status_json" data.transport) (active $(json_get "$status_json" data.active_transport)), dc $(json_get "$status_json" data.dc)"
        note "active requests: $(json_get "$status_json" data.active_requests)"
        note "access filter: enabled=$(json_get "$status_json" data.access.enabled boolean), write list size $(json_get "$status_json" data.access.write number)"
        ;;
    stopped) note 'not running (exit code 3)' ;;
    *)
        note "cannot read the Daemon status (exit code $status_rc)"
        error_text=$(json_error "$status_json" 2>/dev/null)
        [ -n "$error_text" ] && note "tg says: $error_text"
        ;;
esac

section "Login"
result=unreadable
if [ "$state" = running ]; then
    connection=$(json_get "$status_json" data.connection string)
    auth_lost=$(json_get "$status_json" data.auth_lost string 2>/dev/null)
    if [ -n "$auth_lost" ]; then
        note "the login was lost (auth_lost: $auth_lost); the Daemon keeps running"
        result=no-login
    elif [ -n "$(json_get "$status_json" data.login_state string 2>/dev/null)" ] && [ "$(json_get "$status_json" data.login_state string)" != authorized ]; then
        note "a login is in progress (login_state: $(json_get "$status_json" data.login_state string))"
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
    ready)   note 'ready: run commands'; exit 0 ;;
    connecting)
        note 'wait a few seconds and run this check again'
        exit 1
        ;;
    stopped)
        note "start the Daemon: tg${profile_flag} daemon start"
        note 'first setup: it needs credentials (--client desktop, or TG_API_ID and TG_API_HASH); then the human logs in:'
        note "  ! tg${profile_flag} auth login   (in Claude Code)"
        exit 1
        ;;
    pending)
        note "the human finishes the pending login (code, password or QR scan) in the terminal where it runs, or with tg${profile_flag} auth code or auth password if it was started with --no-input; do not start another login"
        exit 2
        ;;
    no-login)
        note "the human logs in again in the same Daemon: ! tg${profile_flag} auth login   (in Claude Code)"
        exit 2
        ;;
    *)
        note 'fix the problem above, then run this check again'
        exit 2
        ;;
esac
