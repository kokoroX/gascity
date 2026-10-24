#!/bin/sh
# port_resolve.sh — shared GC_DOLT_PORT discovery helper.
#
# Sourced by:
#   .gc/system/packs/dolt/assets/scripts/runtime.sh
#   .gc/system/packs/bd/dolt/assets/scripts/runtime.sh
#
# Defines:
#   resolve_dolt_endpoint_or_die  state_file  [provider_state_file]  data_dir  city_path
#   resolve_dolt_port_or_die      state_file  [provider_state_file]  data_dir  city_path
#
# Preconditions (caller responsibilities):
#   - managed_runtime_port is in scope (from a sourced runtime.sh above this
#     file in the source chain).
#   - GC_CITY_PATH is set; the caller enforces this at file head.
#
# POSIX /bin/sh only. No bash-isms.

# bd_endpoint_metadata_scalar <metadata.json> <key> prints a top-level key's
# scalar value. It accepts both the bare numbers bd writes
# (`"dolt_server_port": 3306`) and JSON strings (`"dolt_server_port": "3306"`),
# strips surrounding whitespace and quotes, and prints nothing when the key is
# absent or the file is unreadable.
bd_endpoint_metadata_scalar() {
    [ -f "$1" ] || return 0
    sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\{0,1\}\([^\",}[:space:]]*\)\"\{0,1\}.*/\1/p" "$1" 2>/dev/null | head -1 || true
}

# bd_endpoint_mode <city_path> prints the scope's persisted dolt_mode,
# lowercased, or nothing when the scope has no beads metadata.
bd_endpoint_mode() {
    _bem_meta="$1/.beads/metadata.json"
    [ -f "$_bem_meta" ] || return 0
    bd_endpoint_metadata_scalar "$_bem_meta" dolt_mode | tr 'A-Z' 'a-z'
}

# bd_endpoint_server_mode <city_path> succeeds only for dolt_mode "server",
# bd's external direct-connection topology. A proxied-server scope stays on the
# existing bd-owned no-op path (runtime.sh exits before port resolution there),
# and any other mode is not an external endpoint.
bd_endpoint_server_mode() {
    [ "$(bd_endpoint_mode "$1")" = "server" ]
}

# bd_endpoint_port_record <city_path> prints a valid numeric port from
# .beads/dolt-server.port — bd's preferred record for a running server — or
# nothing when the file is absent or malformed.
bd_endpoint_port_record() {
    _bep_file="$1/.beads/dolt-server.port"
    [ -f "$_bep_file" ] || return 0
    _bep_value=$(tr -d ' \t\r\n' < "$_bep_file" 2>/dev/null || true)
    case "$_bep_value" in
        ''|*[!0-9]*) return 0 ;;
    esac
    printf '%s\n' "$_bep_value"
}

# bd_endpoint_resolve <city_path> prints "<host>\t<port>\t<source>" for the
# endpoint bd recorded for an external Dolt server, or returns 1 when the
# record is absent, malformed, or describes some other topology.
#
# Only read-only bd records are consulted:
#   .beads/metadata.json   dolt_mode must be "server"
#   .beads/metadata.json   dolt_server_host (required)
#   .beads/dolt-server.port            port (preferred when present)
#   .beads/metadata.json   dolt_server_port  port (fallback)
#
# Port precedence: dolt-server.port before dolt_server_port, matching bd's own
# preference for the runtime port record. Credentials are never read here; the
# commands keep using GC_DOLT_USER / GC_DOLT_PASSWORD as before.
bd_endpoint_resolve() {
    _ber_city="$1"
    bd_endpoint_server_mode "$_ber_city" || return 1
    _ber_meta="$_ber_city/.beads/metadata.json"
    _ber_host=$(bd_endpoint_metadata_scalar "$_ber_meta" dolt_server_host | tr -d ' \t\r\n')
    [ -n "$_ber_host" ] || return 1

    _ber_port=$(bd_endpoint_port_record "$_ber_city")
    if [ -n "$_ber_port" ]; then
        printf '%s\t%s\t%s\n' "$_ber_host" "$_ber_port" "bd-server-port"
        return 0
    fi

    _ber_port=$(bd_endpoint_metadata_scalar "$_ber_meta" dolt_server_port | tr -d ' \t\r\n')
    case "$_ber_port" in
        ''|*[!0-9]*) return 1 ;;
    esac
    printf '%s\t%s\t%s\n' "$_ber_host" "$_ber_port" "bd-metadata"
}

# resolve_dolt_endpoint_or_die — resolve the Dolt port and, when the endpoint
# came from bd's records, its host.
#
# Arguments:
#   $1  state_file           Absolute path to .gc/runtime/packs/dolt/dolt-state.json
#                            (or the equivalent for the caller's pack layout).
#   $2  provider_state_file  Optional provider fallback state file.
#   $3  data_dir             Absolute path the caller expects the running server
#                            to be serving from.
#   $4  city_path            Absolute path of the city root, used for the bd
#                            endpoint records and in the error message.
#
# The legacy three-argument form remains supported:
#   resolve_dolt_endpoint_or_die  state_file  data_dir  city_path
#
# Resolution order:
#   1. managed runtime state ($1), validated against $3.
#   2. provider state ($2), unless GC_DOLT_STATE_FILE forced a state file.
#   3. GC_DOLT_PORT as an operator seed.
#   4. Only when GC_DOLT_ALLOW_BD_ENDPOINT=1: the read-only endpoint bd records
#      for an external `server` scope ($4/.beads/*). runtime.sh resets any
#      inherited value and sets this flag itself from the command name, so an
#      environment variable cannot opt a destructive command in; those keep
#      exiting 78 instead of touching a server gc does not own.
#
# Output contract:
#   - Success: one line "<host>\t<port>\t<source>". Host and source are empty
#     for the managed/provider/GC_DOLT_PORT paths; the bd fallback fills them
#     (source is "bd-server-port" or "bd-metadata"). Exit 0.
#   - Failure: nothing on stdout. Structured stderr (§3 template, extended with
#     the bd-endpoint safety explanation when the scope is a bd-managed
#     external endpoint). Exit 78 (whole shell exits, not return).
resolve_dolt_endpoint_or_die() {
    _rdp_state_file="$1"
    _rdp_provider_state_file=""
    if [ "$#" -ge 4 ]; then
        _rdp_provider_state_file="$2"
        _rdp_data_dir="$3"
        _rdp_city_path="$4"
    else
        _rdp_data_dir="$2"
        _rdp_city_path="$3"
    fi

    _rdp_resolved=$(managed_runtime_port "$_rdp_state_file" "$_rdp_data_dir" 2>/dev/null)
    if [ -n "$_rdp_resolved" ]; then
        printf '\t%s\t\n' "$_rdp_resolved"
        return 0
    fi

    _rdp_consulted="GC_DOLT_PORT (unset), GC_DOLT_STATE_FILE"
    if [ -n "$_rdp_provider_state_file" ] && [ -z "${GC_DOLT_STATE_FILE:-}" ]; then
        _rdp_consulted="$_rdp_consulted, dolt-provider-state.json"
        _rdp_resolved=$(managed_runtime_port "$_rdp_provider_state_file" "$_rdp_data_dir" 2>/dev/null)
        if [ -n "$_rdp_resolved" ]; then
            printf '\t%s\t\n' "$_rdp_resolved"
            return 0
        fi
    fi

    if [ -n "${GC_DOLT_PORT:-}" ]; then
        printf '\t%s\t\n' "$GC_DOLT_PORT"
        return 0
    fi

    _rdp_bd_server_mode=false
    if bd_endpoint_server_mode "$_rdp_city_path"; then
        _rdp_bd_server_mode=true
    fi

    if [ "${GC_DOLT_ALLOW_BD_ENDPOINT:-0}" = "1" ]; then
        _rdp_consulted="$_rdp_consulted, .beads/dolt-server.port, .beads/metadata.json"
        if [ "$_rdp_bd_server_mode" = true ]; then
            _rdp_resolved=$(bd_endpoint_resolve "$_rdp_city_path" 2>/dev/null || true)
            if [ -n "$_rdp_resolved" ]; then
                printf '%s\n' "$_rdp_resolved"
                return 0
            fi
        fi
    fi

    _rdp_state_status="missing"
    if [ -f "$_rdp_state_file" ]; then
        _rdp_state_status="present but not running"
    fi

    printf 'gc dolt: cannot resolve runtime port\n' >&2
    printf '  state_file: %s (%s)\n' "$_rdp_state_file" "$_rdp_state_status" >&2
    printf '  city_path:  %s\n' "$_rdp_city_path" >&2
    printf '  consulted:  %s\n' "$_rdp_consulted" >&2

    if [ "$_rdp_bd_server_mode" = true ] && [ "${GC_DOLT_ALLOW_BD_ENDPOINT:-0}" != "1" ]; then
        _rdp_bd_endpoint=$(bd_endpoint_resolve "$_rdp_city_path" 2>/dev/null || true)
        if [ -n "$_rdp_bd_endpoint" ]; then
            _rdp_bd_host=$(printf '%s\n' "$_rdp_bd_endpoint" | cut -f1)
            _rdp_bd_port=$(printf '%s\n' "$_rdp_bd_endpoint" | cut -f2)
            printf '  bd endpoint: bd-managed external Dolt endpoint %s:%s\n' "$_rdp_bd_host" "$_rdp_bd_port" >&2
        else
            printf '  bd endpoint: bd-managed external Dolt endpoint (metadata.json dolt_mode=server)\n' >&2
        fi
        printf '  safety:      this command does not operate on it (data safety)\n' >&2
        printf '  remediation: use a read-only command (status, health, health-check,\n' >&2
        printf '               list, logs, sql) to query this endpoint; `gc start`\n' >&2
        printf '               only manages a gc-managed local Dolt server.\n' >&2
    else
        printf '  remediation: run `gc start` to bring up the city, or set\n' >&2
        printf '               GC_DOLT_PORT explicitly to an already-running\n' >&2
        printf '               server.\n' >&2
    fi
    exit 78
}

# resolve_dolt_port_or_die — compatibility wrapper for callers that only need
# the port. It shares the resolution and error behavior above and prints just
# the second field (the port).
resolve_dolt_port_or_die() {
    _rdp_line=$(resolve_dolt_endpoint_or_die "$@") || exit $?
    printf '%s\n' "$_rdp_line" | cut -f2
}
