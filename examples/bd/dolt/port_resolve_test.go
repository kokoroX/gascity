package dolt_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPortResolveOrDieEnvOverride(t *testing.T) {
	result := runPortResolveOrDie(t, portResolveCase{
		stateFile: filepath.Join(t.TempDir(), "missing-state.json"),
		dataDir:   filepath.Join(t.TempDir(), "data"),
		cityPath:  t.TempDir(),
		env:       []string{"GC_DOLT_PORT=4242"},
	})

	assertPortResolveResult(t, result, 0, "4242\n", "")
}

func TestPortResolveOrDieDiscoverySuccess(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")
	dataDir := filepath.Join(t.TempDir(), "d")
	cityPath := t.TempDir()
	if err := os.WriteFile(stateFile, []byte(fmt.Sprintf(
		`{"running":true,"pid":%d,"port":47823,"data_dir":%q}`,
		os.Getpid(),
		dataDir,
	)), 0o644); err != nil {
		t.Fatalf("write state fixture: %v", err)
	}

	result := runPortResolveOrDie(t, portResolveCase{
		stateFile:   stateFile,
		dataDir:     dataDir,
		cityPath:    cityPath,
		managedPort: "47823",
	})

	assertPortResolveResult(t, result, 0, "47823\n", "")
}

func TestPortResolveOrDieProviderDiscoverySuccess(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")
	providerStateFile := filepath.Join(t.TempDir(), "dolt-provider-state.json")
	dataDir := filepath.Join(t.TempDir(), "d")
	cityPath := t.TempDir()
	if err := os.WriteFile(stateFile, []byte(`{"running":false}`), 0o644); err != nil {
		t.Fatalf("write state fixture: %v", err)
	}
	if err := os.WriteFile(providerStateFile, []byte(fmt.Sprintf(
		`{"running":true,"pid":%d,"port":47824,"data_dir":%q}`,
		os.Getpid(),
		dataDir,
	)), 0o644); err != nil {
		t.Fatalf("write provider state fixture: %v", err)
	}

	result := runPortResolveOrDie(t, portResolveCase{
		stateFile:           stateFile,
		providerStateFile:   providerStateFile,
		dataDir:             dataDir,
		cityPath:            cityPath,
		providerManagedPort: "47824",
	})

	assertPortResolveResult(t, result, 0, "47824\n", "")
}

func TestPortResolveOrDieExplicitStateSkipsProviderFallback(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")
	providerStateFile := filepath.Join(t.TempDir(), "dolt-provider-state.json")
	cityPath := t.TempDir()

	result := runPortResolveOrDie(t, portResolveCase{
		stateFile:           stateFile,
		providerStateFile:   providerStateFile,
		dataDir:             filepath.Join(t.TempDir(), "data"),
		cityPath:            cityPath,
		providerManagedPort: "47824",
		env:                 []string{"GC_DOLT_STATE_FILE=" + stateFile},
	})

	assertPortResolveResult(t, result, 78, "", expectedPortResolveError(stateFile, cityPath, "missing"))
}

func TestPortResolveOrDieMissingState(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")
	cityPath := t.TempDir()

	result := runPortResolveOrDie(t, portResolveCase{
		stateFile: stateFile,
		dataDir:   filepath.Join(t.TempDir(), "data"),
		cityPath:  cityPath,
	})

	assertPortResolveResult(t, result, 78, "", expectedPortResolveError(stateFile, cityPath, "missing"))
}

func TestPortResolveOrDieStatePresentNotRunning(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")
	cityPath := t.TempDir()
	if err := os.WriteFile(stateFile, []byte(`{"running":false}`), 0o644); err != nil {
		t.Fatalf("write state fixture: %v", err)
	}

	result := runPortResolveOrDie(t, portResolveCase{
		stateFile: stateFile,
		dataDir:   filepath.Join(t.TempDir(), "data"),
		cityPath:  cityPath,
	})

	assertPortResolveResult(t, result, 78, "", expectedPortResolveError(stateFile, cityPath, "present but not running"))
}

func TestPortResolveOrDieExit78OnEmptyState(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")
	dataDir := filepath.Join(t.TempDir(), "d")
	cityPath := t.TempDir()
	if err := os.WriteFile(stateFile, []byte(fmt.Sprintf(
		`{"running":true,"pid":99999999,"port":47823,"data_dir":%q}`,
		dataDir,
	)), 0o644); err != nil {
		t.Fatalf("write state fixture: %v", err)
	}

	result := runPortResolveOrDie(t, portResolveCase{
		stateFile: stateFile,
		dataDir:   dataDir,
		cityPath:  cityPath,
	})

	assertPortResolveResult(t, result, 78, "", expectedPortResolveError(stateFile, cityPath, "present but not running"))
}

func TestRuntimeShUsesPortResolve(t *testing.T) {
	root := repoRoot(t)
	assertScriptSourcesPortResolveOnce(t, filepath.Join(root, "assets", "scripts", "runtime.sh"))
}

func TestPortResolveEndpointBDEndpointMetadataFallback(t *testing.T) {
	cityPath := t.TempDir()
	writeBDEndpointMetadata(t, cityPath, "dolt", 3306)

	result := runDoltEndpointResolve(t, portResolveCase{
		stateFile:       filepath.Join(t.TempDir(), "missing-state.json"),
		dataDir:         filepath.Join(t.TempDir(), "data"),
		cityPath:        cityPath,
		allowBDEndpoint: true,
	})

	assertPortResolveResult(t, result, 0, "dolt\t3306\tbd-metadata\n", "")
}

// TestPortResolveEndpointBDEndpointPrefersDoltServerPort pins the bd-endpoint
// precedence: bd documents .beads/dolt-server.port as the preferred runtime
// port record, so it wins over dolt_server_port in metadata.json.
func TestPortResolveEndpointBDEndpointPrefersDoltServerPort(t *testing.T) {
	cityPath := t.TempDir()
	writeBDEndpointMetadata(t, cityPath, "dolt", 3307)
	writeBDDoltServerPort(t, cityPath, "3308")

	result := runDoltEndpointResolve(t, portResolveCase{
		stateFile:       filepath.Join(t.TempDir(), "missing-state.json"),
		dataDir:         filepath.Join(t.TempDir(), "data"),
		cityPath:        cityPath,
		allowBDEndpoint: true,
	})

	assertPortResolveResult(t, result, 0, "dolt\t3308\tbd-server-port\n", "")
}

// TestPortResolveEndpointManagedStateBeatsBDEndpoint is the regression guard
// for gc-managed cities: a valid managed runtime state file still wins over a
// stale bd endpoint record, even with the opt-in enabled.
func TestPortResolveEndpointManagedStateBeatsBDEndpoint(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")
	dataDir := filepath.Join(t.TempDir(), "d")
	cityPath := t.TempDir()
	if err := os.WriteFile(stateFile, []byte(fmt.Sprintf(
		`{"running":true,"pid":%d,"port":47823,"data_dir":%q}`,
		os.Getpid(),
		dataDir,
	)), 0o644); err != nil {
		t.Fatalf("write state fixture: %v", err)
	}
	writeBDEndpointMetadata(t, cityPath, "dolt", 3306)

	result := runDoltEndpointResolve(t, portResolveCase{
		stateFile:       stateFile,
		dataDir:         dataDir,
		cityPath:        cityPath,
		managedPort:     "47823",
		allowBDEndpoint: true,
	})

	assertPortResolveResult(t, result, 0, "\t47823\t\n", "")
}

// TestPortResolveEndpointEnvPortBeatsBDEndpoint keeps GC_DOLT_PORT as an
// operator seed ahead of the bd endpoint record.
func TestPortResolveEndpointEnvPortBeatsBDEndpoint(t *testing.T) {
	cityPath := t.TempDir()
	writeBDEndpointMetadata(t, cityPath, "dolt", 3306)

	result := runDoltEndpointResolve(t, portResolveCase{
		stateFile:       filepath.Join(t.TempDir(), "missing-state.json"),
		dataDir:         filepath.Join(t.TempDir(), "data"),
		cityPath:        cityPath,
		env:             []string{"GC_DOLT_PORT=4242"},
		allowBDEndpoint: true,
	})

	assertPortResolveResult(t, result, 0, "\t4242\t\n", "")
}

// TestPortResolveEndpointBDEndpointIgnoresProxiedServer pins that the fallback
// is only for bd's external `server` mode. A proxied-server scope keeps its
// existing no-op path and is not treated as a directly reachable endpoint.
func TestPortResolveEndpointBDEndpointIgnoresProxiedServer(t *testing.T) {
	cityPath := t.TempDir()
	writeBDEndpointMetadataRaw(t, cityPath, "proxied-server", "dolt", "3306")
	writeBDDoltServerPort(t, cityPath, "3306")
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")

	result := runDoltEndpointResolve(t, portResolveCase{
		stateFile:       stateFile,
		dataDir:         filepath.Join(t.TempDir(), "data"),
		cityPath:        cityPath,
		allowBDEndpoint: true,
	})

	assertPortResolveResult(t, result, 78, "",
		expectedPortResolveErrorWithBDSources(stateFile, cityPath, "missing"))
}

// TestPortResolveEndpointBDEndpointRejectsInvalidPort keeps the fallback
// fail-closed on a malformed bd record instead of handing dolt a bogus port.
func TestPortResolveEndpointBDEndpointRejectsInvalidPort(t *testing.T) {
	cityPath := t.TempDir()
	writeBDEndpointMetadataRaw(t, cityPath, "server", "dolt", `"not-a-port"`)
	stateFile := filepath.Join(t.TempDir(), "dolt-state.json")

	result := runDoltEndpointResolve(t, portResolveCase{
		stateFile:       stateFile,
		dataDir:         filepath.Join(t.TempDir(), "data"),
		cityPath:        cityPath,
		allowBDEndpoint: true,
	})

	assertPortResolveResult(t, result, 78, "",
		expectedPortResolveErrorWithBDSources(stateFile, cityPath, "missing"))
}

// TestPortResolveOrDieExplainsBDEndpointWithoutOptIn pins the operator-facing
// half of fail-closed: a command that did not opt in must say the endpoint is
// bd-managed and off-limits for data safety, instead of only telling the
// operator to run `gc start` (misleading when the city is already up).
func TestPortResolveOrDieExplainsBDEndpointWithoutOptIn(t *testing.T) {
	cityPath := t.TempDir()
	writeBDEndpointMetadata(t, cityPath, "dolt", 3306)
	writeBDDoltServerPort(t, cityPath, "3306")

	result := runPortResolveOrDie(t, portResolveCase{
		stateFile: filepath.Join(t.TempDir(), "dolt-state.json"),
		dataDir:   filepath.Join(t.TempDir(), "data"),
		cityPath:  cityPath,
	})

	if result.code != 78 {
		t.Fatalf("exit code = %d, want 78\nstderr:\n%s", result.code, result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("stdout = %q, want empty", result.stdout)
	}
	for _, want := range []string{
		"gc dolt: cannot resolve runtime port",
		"bd-managed external Dolt endpoint dolt:3306",
		"does not operate on it",
		"data safety",
		"read-only command",
	} {
		if !strings.Contains(result.stderr, want) {
			t.Fatalf("stderr missing %q; got:\n%s", want, result.stderr)
		}
	}
	if strings.Contains(result.stderr, "run `gc start` to bring up the city") {
		t.Fatalf("stderr kept the misleading `gc start` remediation for a bd-managed endpoint:\n%s", result.stderr)
	}
}

type portResolveCase struct {
	stateFile           string
	providerStateFile   string
	dataDir             string
	cityPath            string
	managedPort         string
	providerManagedPort string
	env                 []string
	// allowBDEndpoint sets GC_DOLT_ALLOW_BD_ENDPOINT=1 in the driver's
	// environment. These cases exercise the resolver directly; whether a
	// command may fall back is decided in runtime.sh from the command name,
	// never from the caller's environment.
	allowBDEndpoint bool
}

type portResolveResult struct {
	code   int
	stdout string
	stderr string
}

func runPortResolveOrDie(t *testing.T, tc portResolveCase) portResolveResult {
	t.Helper()
	return runPortResolveDriver(t, tc, "resolve_dolt_port_or_die")
}

// runDoltEndpointResolve drives the three-field endpoint form so tests can
// assert the resolved host and the record it came from, not just the port.
func runDoltEndpointResolve(t *testing.T, tc portResolveCase) portResolveResult {
	t.Helper()
	return runPortResolveDriver(t, tc, "resolve_dolt_endpoint_or_die")
}

func runPortResolveDriver(t *testing.T, tc portResolveCase, resolver string) portResolveResult {
	t.Helper()
	root := repoRoot(t)
	driver := fmt.Sprintf(`
managed_runtime_port() {
    if [ "$1" = "$STATE_FILE" ] && [ -n "${TEST_MANAGED_PORT:-}" ]; then
        printf '%%s\n' "$TEST_MANAGED_PORT"
        return 0
    fi
    if [ -n "${PROVIDER_STATE_FILE:-}" ] && [ "$1" = "$PROVIDER_STATE_FILE" ] && [ -n "${TEST_PROVIDER_MANAGED_PORT:-}" ]; then
        printf '%%s\n' "$TEST_PROVIDER_MANAGED_PORT"
        return 0
    fi
    return 0
}
. %s
if [ -n "${PROVIDER_STATE_FILE:-}" ]; then
    %s "$STATE_FILE" "$PROVIDER_STATE_FILE" "$DATA_DIR" "$CITY_PATH"
else
    %s "$STATE_FILE" "$DATA_DIR" "$CITY_PATH"
fi
`, shellQuote(filepath.Join(root, "assets", "scripts", "port_resolve.sh")), resolver, resolver)

	cmd := exec.Command("sh", "-c", driver)
	cmd.Env = filteredEnv("GC_DOLT_PORT", "GC_DOLT_STATE_FILE", "GC_DOLT_ALLOW_BD_ENDPOINT", "STATE_FILE", "PROVIDER_STATE_FILE", "DATA_DIR", "CITY_PATH", "TEST_MANAGED_PORT", "TEST_PROVIDER_MANAGED_PORT")
	cmd.Env = append(cmd.Env,
		"STATE_FILE="+tc.stateFile,
		"PROVIDER_STATE_FILE="+tc.providerStateFile,
		"DATA_DIR="+tc.dataDir,
		"CITY_PATH="+tc.cityPath,
		"TEST_MANAGED_PORT="+tc.managedPort,
		"TEST_PROVIDER_MANAGED_PORT="+tc.providerManagedPort,
	)
	if tc.allowBDEndpoint {
		cmd.Env = append(cmd.Env, "GC_DOLT_ALLOW_BD_ENDPOINT=1")
	}
	cmd.Env = append(cmd.Env, tc.env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr := &exec.ExitError{}
		ok := errors.As(err, &exitErr)
		if !ok {
			t.Fatalf("port_resolve driver failed to run: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		code = exitErr.ExitCode()
	}
	return portResolveResult{
		code:   code,
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

func assertPortResolveResult(t *testing.T, got portResolveResult, wantCode int, wantStdout, wantStderr string) {
	t.Helper()
	if got.code != wantCode {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", got.code, wantCode, got.stdout, got.stderr)
	}
	if got.stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q\nstderr:\n%s", got.stdout, wantStdout, got.stderr)
	}
	if got.stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", got.stderr, wantStderr)
	}
}

func expectedPortResolveError(stateFile, cityPath, stateStatus string) string {
	return fmt.Sprintf(`gc dolt: cannot resolve runtime port
  state_file: %s (%s)
  city_path:  %s
  consulted:  GC_DOLT_PORT (unset), GC_DOLT_STATE_FILE
  remediation: run `+"`"+`gc start`+"`"+` to bring up the city, or set
               GC_DOLT_PORT explicitly to an already-running
               server.
`, stateFile, stateStatus, cityPath)
}

func expectedPortResolveErrorWithProvider(stateFile, cityPath, stateStatus string) string {
	return fmt.Sprintf(`gc dolt: cannot resolve runtime port
  state_file: %s (%s)
  city_path:  %s
  consulted:  GC_DOLT_PORT (unset), GC_DOLT_STATE_FILE, dolt-provider-state.json
  remediation: run `+"`"+`gc start`+"`"+` to bring up the city, or set
               GC_DOLT_PORT explicitly to an already-running
               server.
`, stateFile, stateStatus, cityPath)
}

func expectedPortResolveErrorWithBDSources(stateFile, cityPath, stateStatus string) string {
	return fmt.Sprintf(`gc dolt: cannot resolve runtime port
  state_file: %s (%s)
  city_path:  %s
  consulted:  GC_DOLT_PORT (unset), GC_DOLT_STATE_FILE, .beads/dolt-server.port, .beads/metadata.json
  remediation: run `+"`"+`gc start`+"`"+` to bring up the city, or set
               GC_DOLT_PORT explicitly to an already-running
               server.
`, stateFile, stateStatus, cityPath)
}

func assertScriptSourcesPortResolveOnce(t *testing.T, scriptPath string) {
	t.Helper()
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read %s: %v", scriptPath, err)
	}
	re := regexp.MustCompile(`(?m)^\.\s+.*port_resolve\.sh`)
	matches := re.FindAllString(string(data), -1)
	if len(matches) != 1 {
		t.Fatalf("%s port_resolve.sh source count = %d, want 1\nmatches: %s", scriptPath, len(matches), strings.Join(matches, "\n"))
	}
}
