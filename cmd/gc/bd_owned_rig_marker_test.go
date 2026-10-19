package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
)

func TestNormalizeCanonicalBdScopeFiles_SkipsBdOwnedRig(t *testing.T) {
	cityPath := t.TempDir()
	t.Setenv("GC_BEADS", "bd")
	writeSchema2RigCity(t, cityPath, "test-city", "[workspace]\n", "")
	rigPath := filepath.Join(cityPath, "rigs", "adopted-rig")
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := `{"name":"adopted-rig","issue_prefix":"jikerental","dolt_database":"JikeRental_p3a","project_id":"2c3e06b7-0000-0000-0000-000000000000","dolt_mode":"server","backend":"dolt"}`
	configYaml := "issue-prefix: JikeRental\n" +
		"gc.endpoint_origin: explicit\n" +
		"gc.endpoint_status: unverified\n" +
		"dolt.host: host.docker.internal\n" +
		"dolt.port: 3307\n" +
		"dolt.user: p3drill\n"
	metadataPath := filepath.Join(rigPath, ".beads", "metadata.json")
	configPath := filepath.Join(rigPath, ".beads", "config.yaml")
	if err := os.WriteFile(metadataPath, []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(configYaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.City{Rigs: []config.Rig{{Name: "adopted-rig", Path: rigPath, StoreOwner: "bd"}}}
	if err := normalizeCanonicalBdScopeFiles(cityPath, cfg); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{metadataPath: metadata, configPath: configYaml} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s changed:\n got: %q\nwant: %q", path, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(rigPath, ".beads", "dolt-server.port")); !os.IsNotExist(err) {
		t.Errorf("dolt-server.port stat error = %v, want not-exist", err)
	}
}

func TestStartBeadsLifecycle_BdOwnedRigSkipsInit(t *testing.T) {
	cityPath := t.TempDir()
	bdRigPath := filepath.Join(cityPath, "bd-owned")
	managedRigPath := filepath.Join(cityPath, "managed")
	for _, path := range []string{bdRigPath, managedRigPath} {
		if err := os.MkdirAll(filepath.Join(path, ".beads"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.City{Rigs: []config.Rig{
		{Name: "bd-owned", Path: bdRigPath, StoreOwner: "bd"},
		{Name: "managed", Path: managedRigPath},
	}}
	origInit := startBeadsLifecycleInitRigDir
	t.Cleanup(func() { startBeadsLifecycleInitRigDir = origInit })
	var initCalls []string
	startBeadsLifecycleInitRigDir = func(_, dir, _ string) error {
		initCalls = append(initCalls, dir)
		return nil
	}

	if err := initRigScopesForStart(cityPath, cfg); err != nil {
		t.Fatal(err)
	}
	if len(initCalls) != 1 || initCalls[0] != managedRigPath {
		t.Fatalf("init calls = %v, want only managed rig %q", initCalls, managedRigPath)
	}
}

func TestRigStoreOwnerRoundTrip(t *testing.T) {
	cityPath := t.TempDir()
	writeSchema2RigCity(t, cityPath, "test-city", `[[rigs]]
name = "adopted-rig"
path = "rigs/adopted-rig"
store_owner = "bd"
`, "")

	cfg, err := config.Load(fsys.OSFS{}, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Rigs) != 1 || !cfg.Rigs[0].StoreOwnedByBd() {
		t.Fatalf("loaded rigs = %+v, want one bd-owned rig", cfg.Rigs)
	}
}
