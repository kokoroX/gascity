// bd_endpoint_test.go covers the opt-in fallback from a gc-managed Dolt
// runtime to the endpoint bd records for an external Dolt server, plus the
// fail-closed guarantee for commands that must never touch that endpoint.
package dolt_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeBDEndpointMetadataRaw writes a city-scope bd metadata.json with the
// given dolt_mode, host, and raw JSON token for dolt_server_port (so tests can
// exercise numeric, quoted, and invalid forms).
func writeBDEndpointMetadataRaw(t *testing.T, cityPath, mode, host, portToken string) {
	t.Helper()
	beadsDir := filepath.Join(cityPath, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	body := fmt.Sprintf(
		`{"backend":"dolt","dolt_mode":%q,"dolt_server_host":%q,"dolt_server_port":%s}`+"\n",
		mode, host, portToken,
	)
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
}

// writeBDEndpointMetadata writes the external-server record bd creates when
// `bd dolt set-endpoint` points a scope at a Dolt server it supervises itself.
func writeBDEndpointMetadata(t *testing.T, cityPath, host string, port int) {
	t.Helper()
	writeBDEndpointMetadataRaw(t, cityPath, "server", host, strconv.Itoa(port))
}

// writeBDDoltServerPort writes bd's preferred runtime port record.
func writeBDDoltServerPort(t *testing.T, cityPath, port string) {
	t.Helper()
	beadsDir := filepath.Join(cityPath, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "dolt-server.port"), []byte(port+"\n"), 0o644); err != nil {
		t.Fatalf("write dolt-server.port: %v", err)
	}
}

// TestDestructiveCommandsFailClosedOnBDEndpoint pins the safety boundary: when
// the city's beads are served by a bd-managed external Dolt endpoint, every
// command that mutates the server or its databases still refuses to run
// (exit 78) and never opens a SQL connection. The endpoint is deliberately
// fully resolvable, so failure cannot be blamed on missing data.
func TestDestructiveCommandsFailClosedOnBDEndpoint(t *testing.T) {
	root := repoRoot(t)
	commands := []string{"cleanup", "compact", "sync", "start", "pull", "recover"}
	// The command, not the caller's environment, decides whether the bd
	// endpoint may be read. Both an unset variable and an inherited opt-in
	// must leave destructive commands fail-closed.
	envCases := []struct {
		name string
		env  string
	}{
		{name: "unset"},
		{name: "inherited-opt-in", env: "GC_DOLT_ALLOW_BD_ENDPOINT=1"},
	}

	for _, name := range commands {
		for _, envCase := range envCases {
			t.Run(name+"/"+envCase.name, func(t *testing.T) {
				cityPath := t.TempDir()
				writeBDEndpointMetadata(t, cityPath, "dolt", 3306)
				writeBDDoltServerPort(t, cityPath, "3306")

				binDir := t.TempDir()
				argvFile := filepath.Join(binDir, "dolt-argv.log")
				writeExecutable(t, filepath.Join(binDir, "dolt"), fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" >> %q
exit 0
`, argvFile))

				cmd := exec.Command("sh", filepath.Join(root, "commands", name, "run.sh"))
				cmd.Env = append(filteredEnv("PATH", "GC_DOLT_ALLOW_BD_ENDPOINT",
					"GC_DOLT_PORT", "GC_DOLT_STATE_FILE", "GC_DOLT_HOST",
					"GC_DOLT_USER", "GC_DOLT_PASSWORD"),
					"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
					"GC_CITY_PATH="+cityPath,
					"GC_PACK_DIR="+root,
				)
				if envCase.env != "" {
					cmd.Env = append(cmd.Env, envCase.env)
				}
				out, err := cmd.CombinedOutput()
				if err == nil {
					t.Fatalf("%s exited 0 for a bd-managed external Dolt endpoint; want fail-closed exit 78\n%s", name, out)
				}
				exitErr := &exec.ExitError{}
				if !errors.As(err, &exitErr) {
					t.Fatalf("%s returned a non-exit error: %v\n%s", name, err, out)
				}
				if exitErr.ExitCode() != 78 {
					t.Fatalf("%s exit code = %d, want 78\n%s", name, exitErr.ExitCode(), out)
				}
				for _, want := range []string{"bd-managed external Dolt endpoint dolt:3306", "data safety"} {
					if !strings.Contains(string(out), want) {
						t.Fatalf("%s error missing %q; got:\n%s", name, want, out)
					}
				}
				if _, statErr := os.Stat(argvFile); statErr == nil {
					t.Fatalf("%s invoked dolt against a bd-managed external endpoint:\n%s", name, out)
				} else if !os.IsNotExist(statErr) {
					t.Fatalf("stat fake dolt log: %v", statErr)
				}
			})
		}
	}
}

// TestBDEndpointOptInKeepsProxiedScopeNoop pins the other half of the scope
// split: opting a read-only command into the bd-endpoint fallback must not
// disturb the existing proxied-server no-op. The runtime guard runs before
// port resolution, so proxied scopes never reach the new fallback.
func TestBDEndpointOptInKeepsProxiedScopeNoop(t *testing.T) {
	root := repoRoot(t)
	cityPath := t.TempDir()
	writeBDEndpointMetadataRaw(t, cityPath, "proxied-server", "dolt", "3306")
	writeBDDoltServerPort(t, cityPath, "3306")

	cmd := exec.Command("sh", filepath.Join(root, healthScript), "--json")
	cmd.Env = proxiedScopeEnv(t, root, cityPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("health --json failed on a proxied scope: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "bd-owned-proxied-scope") {
		t.Fatalf("proxied scope lost its skip document:\n%s", out)
	}
	if strings.Contains(string(out), "FAIL: dolt was invoked") {
		t.Fatalf("proxied scope reached the dolt endpoint fallback:\n%s", out)
	}
}
