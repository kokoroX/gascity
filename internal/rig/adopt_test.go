package rig

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestInitRigBeadsStore_AdoptPersistedIdentitySkipsInitStore(t *testing.T) {
	cityPath := t.TempDir()
	rigPath := t.TempDir()
	metadataPath := filepath.Join(rigPath, ".beads", "metadata.json")
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadataPath, []byte(`{"backend":"dolt","dolt_database":"JikeRental_p3a"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := stubDeps(cityPath)
	deps.StoreContract = func(string) bool { return true }
	deps.AdoptExistingStore = func(string, string) (bool, error) { return true, nil }
	deps.InitStore = func(string, string, string) (bool, error) {
		t.Fatal("InitStore called for adopted persisted identity")
		return false, nil
	}
	var steps []ProvisionStep
	_, persisted, err := initRigBeadsStore(deps, ProvisionRequest{Adopt: true}, rigPath, "jikerental", func(step ProvisionStep) {
		steps = append(steps, step)
	})
	if err != nil {
		t.Fatalf("initRigBeadsStore: %v", err)
	}
	if !persisted {
		t.Fatal("persistedStore = false, want true")
	}
	if len(steps) != 1 || steps[0].Detail != "  Adopted existing beads database; provider init skipped; types.custom not registered" {
		t.Fatalf("steps = %#v, want adoption warning", steps)
	}
}

func TestInitRigBeadsStore_AdoptWithoutIdentityCallsInitStore(t *testing.T) {
	cityPath := t.TempDir()
	rigPath := t.TempDir()
	metadataPath := filepath.Join(rigPath, ".beads", "metadata.json")
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadataPath, []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := stubDeps(cityPath)
	deps.StoreContract = func(string) bool { return true }
	called := false
	deps.InitStore = func(string, string, string) (bool, error) {
		called = true
		return false, nil
	}
	_, _, err := initRigBeadsStore(deps, ProvisionRequest{Adopt: true}, rigPath, "jr", func(ProvisionStep) {})
	if err != nil {
		t.Fatalf("initRigBeadsStore: %v", err)
	}
	if !called {
		t.Fatal("InitStore was not called for adopted store without persisted identity")
	}
}

// TestProvision_ReAdoptExistingRigPersistsStoreOwner covers a city created
// before store_owner existed: re-adopting an already-registered rig must write
// the marker so a later start does not reinitialize the adopted store.
func TestProvision_ReAdoptExistingRigPersistsStoreOwner(t *testing.T) {
	const rigName, prefix = "adopted", "ad"
	cityPath := t.TempDir()
	rigPath := t.TempDir()
	tomlPath := filepath.Join(cityPath, "city.toml")
	cityTOML := fmt.Sprintf("[workspace]\nname = \"adoptcity\"\n\n[[rigs]]\nname = %q\nprefix = %q\n", rigName, prefix)
	if err := os.WriteFile(tomlPath, []byte(cityTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	beadsPath := filepath.Join(rigPath, ".beads")
	if err := os.MkdirAll(beadsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsPath, "metadata.json"), []byte(`{"backend":"dolt","dolt_database":"Adopted_db"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsPath, "config.yaml"), []byte("issue_prefix: "+prefix+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := stubDeps(cityPath)
	deps.Cfg = &config.City{Rigs: []config.Rig{{Name: rigName, Path: rigPath, Prefix: prefix}}}
	deps.StoreContract = func(string) bool { return true }
	deps.AdoptExistingStore = func(string, string) (bool, error) { return true, nil }
	deps.InitStore = func(string, string, string) (bool, error) {
		t.Fatal("InitStore called for an adopted store with persisted identity")
		return false, nil
	}
	deps.InitAndHook = func(string, string, string) error {
		t.Fatal("InitAndHook called for an adopted store with persisted identity")
		return nil
	}
	deps.NormalizeScopes = func(string, *config.City) error { return nil }

	got, _, err := Provision(deps, ProvisionRequest{Name: rigName, Path: rigPath, Adopt: true})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !got.StoreOwnedByBd() {
		t.Errorf("returned rig StoreOwner = %q, want %q", got.StoreOwner, "bd")
	}

	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	written, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parsing written city.toml: %v", err)
	}
	if len(written.Rigs) != 1 {
		t.Fatalf("written city.toml has %d rigs, want 1", len(written.Rigs))
	}
	if !written.Rigs[0].StoreOwnedByBd() {
		t.Errorf("written city.toml rig StoreOwner = %q, want %q", written.Rigs[0].StoreOwner, "bd")
	}
}
