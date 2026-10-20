package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beads/contract"
)

func TestApplyResolvedRigDoltEnv_BdOwnedDirectUsesRigEnvPassword(t *testing.T) {
	cityPath, rigPath := bdOwnedDirectCredsFixture(t)
	writeBdOwnedDirectCreds(t, cityPath, "citypw")
	writeBdOwnedDirectCreds(t, rigPath, "rigpw")
	writeScopeBeadsMetadata(t, rigPath, `{"database":"dolt","backend":"dolt","dolt_mode":"server","dolt_server_host":"db.example.test","dolt_server_port":3307,"dolt_database":"JikeRental_p3a"}`)
	writeRigBdConfig(t, rigPath)
	clearBdOwnedDirectCredsEnv(t)

	env := map[string]string{}
	if err := applyResolvedRigDoltEnvContext(t.Context(), env, cityPath, rigPath, nil, false); err != nil {
		t.Fatalf("applyResolvedRigDoltEnvContext: %v", err)
	}
	for key, want := range map[string]string{
		"BEADS_DOLT_PASSWORD":    "rigpw",
		"BEADS_DOLT_SERVER_USER": "p3drill",
		"BEADS_DOLT_SERVER_HOST": "db.example.test",
		"BEADS_DOLT_SERVER_PORT": "3307",
	} {
		if got := env[key]; got != want {
			t.Errorf("env[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestApplyResolvedRigDoltEnv_OtherRigUnaffected(t *testing.T) {
	cityPath, _ := bdOwnedDirectCredsFixture(t)
	cityEnv := filepath.Join(cityPath, ".beads", ".env")
	if err := os.MkdirAll(filepath.Dir(cityEnv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cityEnv, []byte("BEADS_DOLT_PASSWORD=citypw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rigPath := filepath.Join(cityPath, "rigs", "ordinary")
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	clearBdOwnedDirectCredsEnv(t)

	env := map[string]string{}
	if err := applyResolvedRigDoltEnvContext(t.Context(), env, cityPath, rigPath, nil, false); err != nil {
		t.Fatalf("applyResolvedRigDoltEnvContext: %v", err)
	}
	if got := env["BEADS_DOLT_PASSWORD"]; got != "citypw" {
		t.Fatalf("BEADS_DOLT_PASSWORD = %q, want inherited city password", got)
	}
	if got := env["BEADS_DOLT_SERVER_USER"]; got == "p3drill" {
		t.Fatalf("BEADS_DOLT_SERVER_USER = %q, unexpectedly inherited direct-rig user", got)
	}
}

func TestPreflightIdentity_BdOwnedDirectDefersOrProbesWithScopedAuth(t *testing.T) {
	cityPath, rigPath := bdOwnedDirectCredsFixture(t)
	writeBdOwnedDirectCreds(t, rigPath, "rigpw")
	writeRigBdConfig(t, rigPath)
	writeScopeBeadsMetadata(t, rigPath, `{"database":"dolt","backend":"dolt","dolt_mode":"server","dolt_server_host":"db.example.test","dolt_server_port":3307,"dolt_database":"JikeRental_p3a","project_id":"rig-project"}`)
	clearBdOwnedDirectCredsEnv(t)

	previous := preflightDatabaseProjectIDProbe
	t.Cleanup(func() { preflightDatabaseProjectIDProbe = previous })
	var gotUser, gotPassword string
	preflightDatabaseProjectIDProbe = func(_ contract.DoltConnectionTarget, user, password string) (string, bool, error) {
		gotUser, gotPassword = user, password
		return "", false, errors.New("test probe unavailable")
	}
	reader := preflightDatabaseProjectIDReader(cityPath)
	if _, _, err := reader(rigPath); err == nil {
		t.Fatal("preflight database probe error = nil, want injected failure")
	}
	if gotUser != "p3drill" || gotPassword != "rigpw" {
		t.Fatalf("probe credentials = %q/%q, want p3drill/rigpw", gotUser, gotPassword)
	}

	checker := contract.PreflightChecker{
		FS:       nil,
		Provider: "bd",
		BDContext: func(string) (contract.PreflightBDContext, error) {
			return contract.PreflightBDContext{Backend: "dolt", DoltMode: "server", BDVersion: "v1.5.0"}, nil
		},
		DatabaseProjectID:         reader,
		DeferIdentityToNativeOpen: preflightIdentityDeferredReader(cityPath),
	}
	result, err := checker.Check(rigPath)
	if err != nil {
		t.Fatalf("preflight Check: %v", err)
	}
	for _, check := range result.Checks {
		if check.ID != contract.PreflightCheckIdentityMatch {
			continue
		}
		if check.State != contract.PreflightCheckPass || check.Summary != "database identity deferred to native-open verification (external endpoint)" {
			t.Fatalf("identity_match = %s (%s), want deferred Pass", check.State, check.Summary)
		}
		return
	}
	t.Fatal("identity_match check missing")
}

// A bd-owned direct rig's endpoint lives in metadata.json. The
// .beads/dolt-server.port file is runtime state that an earlier managed
// projection can leave behind; it must not shadow the persisted binding.
func TestBdOwnedDirectRigTarget_IgnoresStalePortFile(t *testing.T) {
	_, rigPath := bdOwnedDirectCredsFixture(t)
	writeScopeBeadsMetadata(t, rigPath, `{"database":"dolt","backend":"dolt","dolt_mode":"server","dolt_server_host":"db.example.test","dolt_server_port":3307,"dolt_database":"JikeRental_p3a"}`)
	if err := os.WriteFile(filepath.Join(rigPath, ".beads", "dolt-server.port"), []byte("13307\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearBdOwnedDirectCredsEnv(t)

	target, err := bdOwnedDirectRigTarget(rigPath)
	if err != nil {
		t.Fatalf("bdOwnedDirectRigTarget: %v", err)
	}
	if target.Port != "3307" {
		t.Fatalf("target.Port = %q, want metadata binding port 3307", target.Port)
	}
	if target.Host != "db.example.test" {
		t.Fatalf("target.Host = %q, want db.example.test", target.Host)
	}
}

func bdOwnedDirectCredsFixture(t *testing.T) (string, string) {
	t.Helper()
	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("name = \"fixture\"\n\n[beads]\nprovider = \"bd\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rigPath := filepath.Join(cityPath, "rigs", "direct")
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	return cityPath, rigPath
}

func writeBdOwnedDirectCreds(t *testing.T, scopeRoot, password string) {
	t.Helper()
	beadsDir := filepath.Join(scopeRoot, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := fmt.Sprintf("BEADS_DOLT_PASSWORD=%s\nBEADS_DOLT_SERVER_USER=p3drill\n", password)
	if err := os.WriteFile(filepath.Join(beadsDir, ".env"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRigBdConfig(t *testing.T, rigPath string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(rigPath, ".beads", "config.yaml"), []byte("issue-prefix: JRP\ndolt.auto-start: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func clearBdOwnedDirectCredsEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"GC_DOLT_HOST", "GC_DOLT_PORT", "GC_DOLT_USER", "GC_DOLT_PASSWORD",
		"BEADS_DOLT_SERVER_HOST", "BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_SERVER_USER", "BEADS_DOLT_PASSWORD",
		"BEADS_DOLT_SERVER_SOCKET", "BEADS_CREDENTIALS_FILE",
	} {
		t.Setenv(key, "")
	}
}
