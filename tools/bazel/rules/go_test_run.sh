#!/usr/bin/env bash
# go_test_run.bzl's action: run a go_test binary once, its log and outputs
# recorded. Usage: go_test_run.sh <exe> <log> <timeout> <test flags...>, with
# OUTS the "VAR=path ..." outputs the run gets the absolute paths of.
#
# A failing run fails the action, so no failure is cached. What it prints:
# the failing tests' summary lines (--- FAIL, a simulator seed's violated:
# and replay: lines), which a long report would push out of any tail, then
# the Go test timeout's panic block (it names the running test) when the run
# timed out, or else the log's tail; the simulator cache's reconcile lines
# are left out of both.
set -u
exe=$1 log=$2 timeout=$3
shift 3
# The action's env names no PATH: bash resolves commands through its built-in
# default but exports none, so the test binary could exec nothing by name
# (cmd/gc's dolt leak guard runs ps on macOS). Give it the PATH `bazel test`
# gives the same binary (.bazelrc test --test_env=PATH).
export PATH="${PATH:-/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin}"
export TEST_SRCDIR="$PWD/$exe.runfiles" TEST_TMPDIR="$PWD/$log.tmp"
export HOME="$TEST_TMPDIR" TMPDIR="$TEST_TMPDIR"
mkdir -p "$TEST_TMPDIR"
outs=${OUTS:-}
unset OUTS
for out in $outs; do
  export "${out%%=*}=$PWD/${out#*=}"
done
# run_with_timeout runs the test into $log as `timeout -k 10` would: TERM to
# its process group after $timeout seconds, KILL 10s later, and status 124
# when the timeout fired. macOS ships no GNU timeout, so a watchdog does the
# same there; it polls once a second and exits within a second of the run.
# `set -m` puts the test in its own process group so the whole tree gets the
# signal; a sandbox may refuse the setpgid (and say so on stderr), so the
# watchdog signals the lone process when the group is not there.
run_with_timeout() {
  if command -v timeout >/dev/null 2>&1; then
    timeout -k 10 "$timeout" "$exe" "$@" >"$log" 2>&1
    return
  fi
  local fired="$log.timedout" pid watchdog i status=0
  rm -f "$fired"
  { set -m; "$exe" "$@" >"$log" 2>&1 & } 2>/dev/null
  pid=$!
  set +m
  (
    signal() { kill "-$1" -- "-$pid" 2>/dev/null || kill "-$1" "$pid" 2>/dev/null; }
    for ((i = 0; i < timeout; i++)); do
      sleep 1
      kill -0 "$pid" 2>/dev/null || exit 0
    done
    : >"$fired"
    signal TERM
    for ((i = 0; i < 10; i++)); do
      sleep 1
      kill -0 "$pid" 2>/dev/null || exit 0
    done
    signal KILL
  ) </dev/null >/dev/null 2>&1 &
  watchdog=$!
  wait "$pid" || status=$?
  wait "$watchdog" 2>/dev/null
  if [ -e "$fired" ]; then
    rm -f "$fired"
    status=124
  fi
  return "$status"
}
rc=0
run_with_timeout "$@" || rc=$?
rm -rf "$TEST_TMPDIR"
if [ "$rc" -ne 0 ]; then
  filtered() { grep -v 'beads cache: reconciled' "$log"; }
  summary=$(filtered | grep -E -- '--- FAIL|violated:|replay:')
  if [ -n "$summary" ]; then
    printf 'go_test_run: failing:\n%s\n\n' "$summary" >&2
  fi
  if grep -q '^panic: test timed out' "$log"; then
    sed -n '/^panic: test timed out/,$p' "$log" | head -n 120 >&2
    echo "go_test_run: $exe $* outlived its -test.timeout" >&2
  else
    filtered | tail -n 400 >&2
    case $rc in
    124 | 137) echo "go_test_run: $exe $* outlived its timeout (${timeout}s), killed" >&2 ;;
    *) echo "go_test_run: $exe $* failed (exit $rc)" >&2 ;;
    esac
  fi
  exit 1
fi
for out in $outs; do
  [ -e "${out#*=}" ] || : >"${out#*=}"
done
