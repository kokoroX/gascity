package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeProviderOwnedCityWithStaleRigRecord builds the shape `gc rig remove`
// leaves behind: a provider-owned proxied city, a journal that still carries a
// ready record for the rig's directory, and a rig directory with no bd binding
// at all — the store went with the removed rig. city.toml declares the rig
// again, the way re-adding a rig at the same path does.
func writeProviderOwnedCityWithStaleRigRecord(t *testing.T, detached bool) (cityPath, rigPath string) {
	t.Helper()
	cityPath, rigPath, _ = writeLegacyManagedCity(t)
	writeScopeBeadsMetadata(t, cityPath, `{"backend":"dolt","database":"dolt","dolt_mode":"proxied-server","dolt_database":"hq"}`)
	if err := os.MkdirAll(filepath.Join(cityPath, ".beads", "dolt"), 0o755); err != nil {
		t.Fatal(err)
	}
	rigKey := "rig:fresh"
	if detached {
		// The key `gc rig remove` writes before city.toml declares the path
		// again; the re-key pass moves it back to rig:fresh.
		rigKey = "path:" + normalizePathForCompare(rigPath)
	}
	journal := providerScopeOwnershipJournal{Version: 1, Scopes: map[string]providerScopeOwnershipEntry{
		"city": {
			ScopePath:      normalizePathForCompare(cityPath),
			LifecycleOwner: providerScopeLifecycleOwner,
			State:          providerScopeReady,
		},
		rigKey: {
			ScopePath:      normalizePathForCompare(rigPath),
			LifecycleOwner: providerScopeLifecycleOwner,
			State:          providerScopeReady,
		},
	}}
	if err := writeProviderScopeOwnershipJournal(cityPath, journal); err != nil {
		t.Fatal(err)
	}
	return cityPath, rigPath
}

// TestStaleReadyRigRecordIsRearmedForInit covers the failure JikeRental-gascity.14
// reported: `gc rig remove` keeps the scope's ownership record and only re-keys
// it, so re-adding a rig at that path found a ready record, took the ready ->
// start branch instead of initializing, and died in bd with "no beads database
// found". A ready record whose scope has no bd binding left on disk claims
// something that is not there, so the add has to re-arm it.
func TestStaleReadyRigRecordIsRearmedForInit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		detached bool
	}{
		{name: "detached path record", detached: true},
		{name: "already re-keyed to the rig", detached: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cityPath, rigPath := writeProviderOwnedCityWithStaleRigRecord(t, tc.detached)
			cfg, err := loadCityConfig(cityPath, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if err := ensureFreshRigProviderOwnership(cityPath, cfg); err != nil {
				t.Fatalf("ensureFreshRigProviderOwnership: %v", err)
			}
			key, entry, owned, err := providerScopeOwnershipRecord(cityPath, rigPath)
			if err != nil || !owned {
				t.Fatalf("rig ownership = (%+v, %t, %v), want provider-owned", entry, owned, err)
			}
			if entry.State != providerScopeInitializing {
				t.Fatalf("rig state = %q, want %q so init runs instead of start", entry.State, providerScopeInitializing)
			}
			if want := (providerScopeIntent{Transport: "proxied", Target: "local"}); entry.Intent != want {
				t.Fatalf("rig intent = %+v, want %+v", entry.Intent, want)
			}
			if key != "rig:fresh" {
				t.Fatalf("rig record key = %q, want %q", key, "rig:fresh")
			}
		})
	}
}

// TestStaleReadyRigRecordIsRearmedBeforeInit is the same repair at the init
// front door, which resolves ownership for one scope rather than for every
// configured rig.
func TestStaleReadyRigRecordIsRearmedBeforeInit(t *testing.T) {
	cityPath, rigPath := writeProviderOwnedCityWithStaleRigRecord(t, true)
	if err := ensureProviderScopeOwnershipBeforeInit(cityPath, rigPath); err != nil {
		t.Fatalf("ensureProviderScopeOwnershipBeforeInit: %v", err)
	}
	_, entry, owned, err := providerScopeOwnershipRecord(cityPath, rigPath)
	if err != nil || !owned {
		t.Fatalf("rig ownership = (%+v, %t, %v), want provider-owned", entry, owned, err)
	}
	if entry.State != providerScopeInitializing {
		t.Fatalf("rig state = %q, want %q so init runs instead of start", entry.State, providerScopeInitializing)
	}
	if want := (providerScopeIntent{Transport: "proxied", Target: "local"}); entry.Intent != want {
		t.Fatalf("rig intent = %+v, want %+v", entry.Intent, want)
	}
}

// TestReadyRigRecordWithABindingStaysReady fences the repair off from the case
// the design deliberately refuses: a clone carries .beads/metadata.json with
// dolt_mode proxied-server and no store, and serving it would create an empty
// store that reads as an empty tracker. That scope still has a bd binding, so
// its ready record is a live claim and must not be re-armed — the store
// validation ahead of init is what reports it.
func TestReadyRigRecordWithABindingStaysReady(t *testing.T) {
	cityPath, rigPath := writeProviderOwnedCityWithStaleRigRecord(t, true)
	writeScopeBeadsMetadata(t, rigPath, `{"backend":"dolt","database":"dolt","dolt_mode":"proxied-server","dolt_database":"fresh"}`)
	cfg, err := loadCityConfig(cityPath, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureFreshRigProviderOwnership(cityPath, cfg); err != nil {
		t.Fatalf("ensureFreshRigProviderOwnership: %v", err)
	}
	_, entry, owned, err := providerScopeOwnershipRecord(cityPath, rigPath)
	if err != nil || !owned {
		t.Fatalf("rig ownership = (%+v, %t, %v), want provider-owned", entry, owned, err)
	}
	if entry.State != providerScopeReady {
		t.Fatalf("rig state = %q, want %q: a bound scope's claim is live", entry.State, providerScopeReady)
	}
}
