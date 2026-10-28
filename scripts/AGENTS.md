# scripts — change guide

## Hermetic Git test config is mirrored

`Makefile`'s `TEST_ENV` and the nested `env -i` wrappers in
`scripts/test-local-parallel`, `scripts/test-go-test-shard`, and
`scripts/test-integration-shard` must all pin `GIT_CONFIG_NOSYSTEM=1` and
`GIT_CONFIG_GLOBAL=/dev/null`. Updating only the Makefile is insufficient
because each nested runner rebuilds the environment and would otherwise
restore user Git configuration through the preserved `HOME`.

## Test-env pane shell is pinned

The same four allowlists pin `SHELL=/bin/sh` and never forward the caller's
`SHELL`. A test that opens a tmux or herdr pane runs that shell in it, and the
invoking user's zsh under a fresh `HOME` (a release gate, CI) opens its new-user
wizard, which swallows the typed text and fails the test by timeout (ga-1wilql,
ga-sux0ij). Change all four together: `TestRunnerTestEnvsPinPaneShell` in
`scripts/git_test_env_test.go` fails when one drifts.
`test/acceptance/helpers/env.go` builds its own environment and still forwards
`SHELL`: its panes start with an explicit command, which tmux runs as
`$SHELL -c`, so they never reach the wizard.

## Git hooks chain to beads

Each `.githooks` hook forwards to `.githooks/lib/beads-chain.sh`. Adding a
hook that beads manages means adding its `.githooks` counterpart too —
`TestGitHooksCoverEveryBeadsManagedHook` in `scripts/` fails otherwise. Hook
ownership is explained in `CONTRIBUTING.md` ("Git hook ownership").

The pre-push Go-change scan must consume the complete diff and fail if the
comparison cannot be read. An early-exiting `grep -q` under `pipefail` can make
a large diff look empty when Git receives SIGPIPE, silently skipping the suite.
A remote tip missing from the local object store is refused first with a
`git fetch <remote> <ref>` remedy: Git still runs the hook for a non-forced push
over an unfetched tip and prints its own "fetch first" hint only if the hook
passes. A forced push over an unfetched tip is refused too, until that fetch.
The remedy names the ref because a plain `git fetch <remote>` can skip the tip
(a single-branch clone, a URL push, a moved tag), and it strips credentials
from a push URL. `githooks_pre_push_stdin_test.go` covers large diffs,
unfetched remote tips, credentialed push URLs and unreadable comparisons.

## Make targets run Bazel; `-go` twins are the escape hatch

`make test`, `check`, `check-all`, `check-docs`, `test-acceptance` and
`test-integration` run the `bazel test` commands `bazel.yml`'s lanes run.
Each has a plain-`go test` twin named `<target>-go`. A workflow that runs a
Go-native suite calls the `-go` name explicitly (`TestWorkflowsNameTheirTestEngine`
in `scripts/` fails on a workflow calling a bazel-backed primary name). A new
test runner gets a Bazel target first; a `go test` recipe is an extra, never
the only route. `TestMakePrimaryTargetsRunBazel` in `scripts/` pins this.

`.githooks/lib/push-suite.sh` runs `bazel test //...` at push time. Its
`make test-fast-parallel` fallback prints a banner with the reason, because
that suite is not what CI enforces; `GC_PREPUSH_SUITE=go` opts in explicitly.
`auto` also falls back when a loader probe says the host cannot resolve the
ICU runtime `MODULE.bazel`'s sysroot pins, or cannot say. The docs side of
`.githooks/pre-commit` makes the same choice between `make check-docs` and
`make check-docs-go` (`GC_PRECOMMIT_DOCS=auto|bazel|go`), again behind a
banner. One shared decision, `.githooks/lib/local-runtime.sh`, serves both
hooks; it derives the soname list from a single ICU-major constant, and
`TestBazelLocalRuntimePinMatchesProbe` fails when that constant, the
`MODULE.bazel` pin and `engdocs/bazel-quickstart.md` disagree. Only `auto`
probes: an explicit mode is never replaced.

## Shell guards walk files with `find -L`, never `grep -R`

Under `bazel test` an `sh_test` reads its sources from the runfiles tree,
where every file is a symlink. GNU grep's `-R` follows them; BSD grep's
(macOS `/usr/bin/grep`, which the pinned test `PATH` resolves) skips them, so
a `grep -R` scan matches nothing on macOS and the guard passes without
checking anything. Linux CI cannot catch that. List files with
`find -L <paths> -type f -print0 | xargs -0 grep -H ...` instead, as
`check-eventexport-isolation.sh` (`grep_surface`) and
`check-core-boundary.sh` check (f) do.

The same BSD/GNU split applies to the shell stubs embedded in Go tests: the
pinned test `PATH` resolves `sed` to macOS's `/usr/bin/sed`, whose `-i`
requires the backup suffix to follow it, so GNU's `sed -i <script> <file>`
makes BSD sed read the file path as its script and fail with
`invalid command code`. Pop a line with
`tail -n +2 "$f" >"$f.next" && mv -f "$f.next" "$f"` instead, as
`worker_env_notify_beads_test.go`'s gh stub does. `scripts/lib/common.sh`'s
`sed_i` wrapper covers the scripts that can source it; a standalone stub
cannot. Such a stub is a Go raw string literal, so nothing in it — a
comment included — may contain a backtick.

BSD `wc` pads its count with leading spaces, so a stub that spells a counter
into a filename or compares it as a string normalizes it with arithmetic
expansion, as `worker_env_notify_beads_test.go`'s gh stub does, or pipes it
through `tr -d ' '`, as `check-go-format.sh` does.
A count used only in a numeric `[ "$n" -gt N ]` test needs no trimming.

## The Go format check runs offline

`//:go_format_test` pins `GOPROXY=off` and `GOTOOLCHAIN=local` in its `env`.
goimports starts `go list -m -e -json ...` once per `xargs` batch, and the
test's `HOME` is a fresh `TEST_TMPDIR`: no module cache, no user `go env`
file, no proxy variables. With network access to `proxy.golang.org`, every
run downloads module metadata; without it, each batch waits for TCP connect
timeouts (660s against a 300s limit on a macOS host whose only route out is a
local proxy). Offline, resolution fails fast and goimports falls back to
path-derived package names, giving the same result in about 30s.

## The tmux test inventory is derived, never counted

The `internal/runtime/tmux` integration inventory is `scripts/runtime-tmux-tests.manifest`
(every test name, in discovery order) plus `scripts/runtime-tmux-integration-files.manifest`
(the `_test.go` files that compile only with `-tags integration`, one basename
per line, ascending). Both are admin-gated through CODEOWNERS `/scripts/`.
`scripts/runtime_tmux_manifest_test.go` and `scripts/runtime_tmux_lane_test.go`
derive their expectations from the source tree, so never type an inventory
count (total, default-lane, integration-only, or per-shard) into a test: two
PRs that each add a test would conflict on it. Adding a test costs manifest
lines only; a new integration-only test file costs one sorted record line.
The shard rule is `i % shard_total == shard_index - 1` in
`scripts/test-go-test-shard` (`TestRuntimeTmuxIntegrationShardUsesCheckedManifestOnLinux`
restates it and runs each shard through the real script).
