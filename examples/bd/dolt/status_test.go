package dolt_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const statusScript = "commands/status/run.sh"

// writeFakeBeadsBD installs a fake gc-beads-bd.sh at the path the status command
// resolves (GC_CITY_PATH/.gc/scripts/gc-beads-bd.sh) whose `probe` op exits with
// probeExit.
func writeFakeBeadsBD(t *testing.T, cityPath string, probeExit int) {
	t.Helper()
	scriptsDir := filepath.Join(cityPath, ".gc", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(scriptsDir, "gc-beads-bd.sh"),
		fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n  probe) exit %d ;;\nesac\nexit 0\n", probeExit))
}

// writeFakeBeadsBDCheckingEndpoint writes a probe that succeeds only when its
// own environment names the expected endpoint. The probe runs as a child of the
// status command, so it sees GC_DOLT_HOST/GC_DOLT_PORT only when that command
// exported the endpoint it resolved; a missing or different endpoint exits 3 so
// a reachability assertion cannot pass on an unexported value.
func writeFakeBeadsBDCheckingEndpoint(t *testing.T, cityPath, wantHost, wantPort string, probeExit int) {
	t.Helper()
	scriptsDir := filepath.Join(cityPath, ".gc", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(scriptsDir, "gc-beads-bd.sh"), fmt.Sprintf(`#!/bin/sh
case "$1" in
  probe)
    if [ "${GC_DOLT_HOST:-}" != %q ] || [ "${GC_DOLT_PORT:-}" != %q ]; then
      printf 'probe saw GC_DOLT_HOST=%%s GC_DOLT_PORT=%%s, want %s:%s\n' "${GC_DOLT_HOST:-}" "${GC_DOLT_PORT:-}" >&2
      exit 3
    fi
    exit %d
    ;;
esac
exit 0
`, wantHost, wantPort, wantHost, wantPort, probeExit))
}

func runStatus(t *testing.T, cityPath, host, port string) (string, error) {
	t.Helper()
	return runStatusWithEnv(t, cityPath, host, port)
}

// runStatusWithEnv runs the status command with extra environment entries
// appended after the filtered keys, so tests can inject an inherited variable
// without depending on the host environment.
func runStatusWithEnv(t *testing.T, cityPath, host, port string, extraEnv ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("sh", filepath.Join(root, statusScript))
	cmd.Env = append(filteredEnv("GC_CITY_PATH", "GC_PACK_DIR", "GC_DOLT_HOST", "GC_DOLT_PORT", "GC_DOLT_ALLOW_BD_ENDPOINT", "PATH"),
		"GC_CITY_PATH="+cityPath,
		"GC_PACK_DIR="+root,
		"PATH="+os.Getenv("PATH"),
	)
	// An empty host/port models a caller that never had the variables at all.
	// Defining them as empty still exports them: a POSIX shell keeps an
	// inherited variable exported across assignment, which would hide a missing
	// export in the script under test.
	if host != "" {
		cmd.Env = append(cmd.Env, "GC_DOLT_HOST="+host)
	}
	if port != "" {
		cmd.Env = append(cmd.Env, "GC_DOLT_PORT="+port)
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestStatusScriptExternalReachablePrintsEndpointText is the su-deol8 guard for
// `gc dolt status` producing meaningful text for a reachable external endpoint
// instead of exiting 0 silently.
func TestStatusScriptExternalReachablePrintsEndpointText(t *testing.T) {
	cityPath := t.TempDir()
	writeFakeBeadsBD(t, cityPath, 0)

	out, err := runStatus(t, cityPath, "superlzy-dolt", "3306")
	if err != nil {
		t.Fatalf("status exited nonzero for reachable endpoint: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("status produced no text for reachable external endpoint (su-deol8 regression)")
	}
	for _, want := range []string{"external endpoint", "superlzy-dolt:3306", "reachable"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q; got:\n%s", want, out)
		}
	}
}

// TestStatusScriptExternalUnreachablePrintsEndpointText verifies the external
// endpoint failure case still names the remote endpoint (not "not running").
func TestStatusScriptExternalUnreachablePrintsEndpointText(t *testing.T) {
	cityPath := t.TempDir()
	writeFakeBeadsBD(t, cityPath, 2)

	out, err := runStatus(t, cityPath, "superlzy-dolt", "3306")
	if err == nil {
		t.Fatalf("status exited 0 for unreachable endpoint; want nonzero\n%s", out)
	}
	for _, want := range []string{"external endpoint", "superlzy-dolt:3306", "unreachable"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not running") {
		t.Fatalf("status reported a reachable-external failure as a local 'not running' signal:\n%s", out)
	}
}

// TestStatusScriptLocalRunningPrintsManagedText verifies the local managed path
// keeps its own message and does not adopt the external phrasing.
func TestStatusScriptLocalRunningPrintsManagedText(t *testing.T) {
	cityPath := t.TempDir()
	writeFakeBeadsBD(t, cityPath, 0)

	out, err := runStatus(t, cityPath, "127.0.0.1", "3311")
	if err != nil {
		t.Fatalf("status exited nonzero for running managed server: %v\n%s", err, out)
	}
	for _, want := range []string{"running", "managed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "external endpoint") {
		t.Fatalf("local managed status must not use external-endpoint phrasing:\n%s", out)
	}
}

// TestStatusScriptBDEndpointMetadataPrintsExternalEndpoint covers the
// degraded-but-healthy city: no gc-managed runtime state and no GC_DOLT_PORT,
// but bd recorded an external Dolt server. `gc dolt status` opts into that
// record and reports the endpoint it resolved instead of exiting 78.
func TestStatusScriptBDEndpointMetadataPrintsExternalEndpoint(t *testing.T) {
	cityPath := t.TempDir()
	writeFakeBeadsBDCheckingEndpoint(t, cityPath, "dolt", "3306", 0)
	writeBDEndpointMetadata(t, cityPath, "dolt", 3306)

	out, err := runStatus(t, cityPath, "", "")
	if err != nil {
		t.Fatalf("status exited nonzero for bd-recorded external endpoint: %v\n%s", err, out)
	}
	for _, want := range []string{"external endpoint dolt:3306", "reachable"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q; got:\n%s", want, out)
		}
	}
}

// TestStatusScriptBDEndpointFallbackIgnoresEnvOptOut proves the command
// allowlist is authoritative for allowlisted commands too: runtime.sh resets
// any inherited GC_DOLT_ALLOW_BD_ENDPOINT, so an environment opt-out cannot
// stop status from resolving the endpoint bd recorded.
func TestStatusScriptBDEndpointFallbackIgnoresEnvOptOut(t *testing.T) {
	cityPath := t.TempDir()
	writeFakeBeadsBDCheckingEndpoint(t, cityPath, "dolt", "3306", 0)
	writeBDEndpointMetadata(t, cityPath, "dolt", 3306)

	out, err := runStatusWithEnv(t, cityPath, "", "", "GC_DOLT_ALLOW_BD_ENDPOINT=0")
	if err != nil {
		t.Fatalf("status exited nonzero with an inherited opt-out: %v\n%s", err, out)
	}
	for _, want := range []string{"external endpoint dolt:3306", "reachable"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q; got:\n%s", want, out)
		}
	}
}
