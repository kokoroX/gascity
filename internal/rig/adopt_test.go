package rig

import (
	"os"
	"path/filepath"
	"testing"
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
