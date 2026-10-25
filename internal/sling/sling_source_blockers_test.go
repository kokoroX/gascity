package sling

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// writeTwoStepGraphFormula writes the graph.v2 formula these tests attach. The
// second step needs the first, so the workflow has an entry step and a
// genuinely dependent step: projecting a source blocker onto every step is
// observable on both, and the entry step stays identifiable as the one with no
// in-workflow blocker.
func writeTwoStepGraphFormula(t *testing.T, dir, name string) {
	t.Helper()
	body := `formula = "` + name + `"
version = 2
contract = "graph.v2"

[[steps]]
id = "first"
title = "First"

[[steps]]
id = "second"
title = "Second"
needs = ["first"]
`
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s formula: %v", name, err)
	}
}

// workflowBeads returns the workflow root plus every bead stamped with its
// gc.root_bead_id in store.
func workflowBeads(t *testing.T, store beads.Store, rootID string) []beads.Bead {
	t.Helper()
	root, err := store.Get(rootID)
	if err != nil {
		t.Fatalf("Get(workflow root %s): %v", rootID, err)
	}
	all, err := store.List(beads.ListQuery{AllowScan: true})
	if err != nil {
		t.Fatalf("listing beads: %v", err)
	}
	out := []beads.Bead{root}
	for _, b := range all {
		if b.ID == rootID || b.Metadata[beadmeta.RootBeadIDMetadataKey] != rootID {
			continue
		}
		out = append(out, b)
	}
	return out
}

// blockingDepIDs returns the ids beadID's ready-blocking dependencies point
// at. Other dependency types (parent-child, tracks) constrain nothing here.
func blockingDepIDs(t *testing.T, store beads.Store, beadID string) []string {
	t.Helper()
	deps, err := store.DepList(beadID, "down")
	if err != nil {
		t.Fatalf("DepList(%s): %v", beadID, err)
	}
	var out []string
	for _, dep := range deps {
		if beads.IsReadyBlockingDependencyType(dep.Type) {
			out = append(out, dep.DependsOnID)
		}
	}
	return out
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func sourceBlockerSlingDeps(t *testing.T) SlingDeps {
	t.Helper()
	formulaDir := t.TempDir()
	writeTwoStepGraphFormula(t, formulaDir, "graph-work")
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	return deps
}

func attachGraphWorkflow(t *testing.T, deps SlingDeps, sourceID string) SlingResult {
	t.Helper()
	s, err := New(deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := s.AttachFormula(context.Background(), "graph-work", sourceID, config.Agent{Name: "worker", MaxActiveSessions: intPtr(1)}, FormulaOpts{})
	if err != nil {
		t.Fatalf("AttachFormula: %v", err)
	}
	if result.WorkflowID == "" {
		t.Fatal("WorkflowID = empty, want a live workflow root")
	}
	return result
}

// TestSlingAttachGraphFormulaWaitsOnSourceBlockers pins the constraint a
// graph.v2 attach inherits from its source bead: a source that is blocked
// cannot start work until that blocker closes, so every step of the workflow it
// launches must carry the same blocks edge. Without it the entry step is
// immediately Ready and a worker can claim it before the source's blocker
// clears.
func TestSlingAttachGraphFormulaWaitsOnSourceBlockers(t *testing.T) {
	t.Run("open blocker is projected onto every workflow bead", func(t *testing.T) {
		deps := sourceBlockerSlingDeps(t)
		source, err := deps.Store.Create(beads.Bead{Title: "source", Type: "task", Status: "open"})
		if err != nil {
			t.Fatalf("creating source: %v", err)
		}
		blocker, err := deps.Store.Create(beads.Bead{Title: "blocker", Type: "task", Status: "open"})
		if err != nil {
			t.Fatalf("creating blocker: %v", err)
		}
		if err := deps.Store.DepAdd(source.ID, blocker.ID, "blocks"); err != nil {
			t.Fatalf("DepAdd(source, blocker): %v", err)
		}

		result := attachGraphWorkflow(t, deps, source.ID)
		beadsInWorkflow := workflowBeads(t, deps.Store, result.WorkflowID)
		if len(beadsInWorkflow) < 3 {
			t.Fatalf("workflow beads = %d, want root plus two steps", len(beadsInWorkflow))
		}
		for _, b := range beadsInWorkflow {
			if !containsID(blockingDepIDs(t, deps.Store, b.ID), blocker.ID) {
				t.Errorf("workflow bead %s has no blocks edge to source blocker %s; deps=%v",
					b.ID, blocker.ID, blockingDepIDs(t, deps.Store, b.ID))
			}
		}
	})

	t.Run("closed blocker releases the entry step", func(t *testing.T) {
		deps := sourceBlockerSlingDeps(t)
		source, err := deps.Store.Create(beads.Bead{Title: "source", Type: "task", Status: "open"})
		if err != nil {
			t.Fatalf("creating source: %v", err)
		}
		blocker, err := deps.Store.Create(beads.Bead{Title: "blocker", Type: "task", Status: "open"})
		if err != nil {
			t.Fatalf("creating blocker: %v", err)
		}
		if err := deps.Store.DepAdd(source.ID, blocker.ID, "blocks"); err != nil {
			t.Fatalf("DepAdd(source, blocker): %v", err)
		}
		if err := deps.Store.Close(blocker.ID); err != nil {
			t.Fatalf("closing blocker: %v", err)
		}

		result := attachGraphWorkflow(t, deps, source.ID)
		beadsInWorkflow := workflowBeads(t, deps.Store, result.WorkflowID)
		inWorkflow := make(map[string]bool, len(beadsInWorkflow))
		for _, b := range beadsInWorkflow {
			inWorkflow[b.ID] = true
		}
		entrySteps := 0
		for _, b := range beadsInWorkflow {
			var external []string
			internal := false
			for _, depID := range blockingDepIDs(t, deps.Store, b.ID) {
				if inWorkflow[depID] {
					internal = true
					continue
				}
				external = append(external, depID)
			}
			if internal {
				continue
			}
			entrySteps++
			for _, depID := range external {
				dep, err := deps.Store.Get(depID)
				if err != nil {
					t.Fatalf("Get(blocker %s): %v", depID, err)
				}
				if dep.Status != "closed" {
					t.Errorf("entry workflow bead %s still waits on open blocker %s (status %q)", b.ID, depID, dep.Status)
				}
			}
		}
		if entrySteps == 0 {
			t.Fatal("no entry step found; the fixture no longer distinguishes entry from dependent steps")
		}
	})

	t.Run("no source blockers leaves no external edges", func(t *testing.T) {
		deps := sourceBlockerSlingDeps(t)
		source, err := deps.Store.Create(beads.Bead{Title: "source", Type: "task", Status: "open"})
		if err != nil {
			t.Fatalf("creating source: %v", err)
		}

		result := attachGraphWorkflow(t, deps, source.ID)
		beadsInWorkflow := workflowBeads(t, deps.Store, result.WorkflowID)
		inWorkflow := make(map[string]bool, len(beadsInWorkflow))
		for _, b := range beadsInWorkflow {
			inWorkflow[b.ID] = true
		}
		for _, b := range beadsInWorkflow {
			for _, depID := range blockingDepIDs(t, deps.Store, b.ID) {
				if !inWorkflow[depID] {
					t.Errorf("workflow bead %s has an unexpected external blocks edge to %s", b.ID, depID)
				}
			}
		}
	})
}

// TestSlingAttachGraphFormulaSkipsUnresolvableSourceBlockers covers the
// split-store case. A dependency row only means something to the store that
// holds it: an edge naming an id the workflow store cannot resolve would leave
// every step permanently not-ready, so the launch drops it and records why
// instead of wedging the workflow. A blocker that is already closed needs no
// edge at all — it constrains nothing.
func TestSlingAttachGraphFormulaSkipsUnresolvableSourceBlockers(t *testing.T) {
	newSplit := func(t *testing.T) (SlingDeps, beads.Bead, beads.Bead) {
		t.Helper()
		formulaDir := t.TempDir()
		writeTwoStepGraphFormula(t, formulaDir, "graph-work")
		cfg := graphV2SlingTestConfig(t, formulaDir)
		deps, work, _ := splitSlingDeps(t, cfg)
		deps.CityPath = t.TempDir()
		source, err := work.Create(beads.Bead{Title: "source", Type: "task", Status: "open"})
		if err != nil {
			t.Fatalf("creating source: %v", err)
		}
		blocker, err := work.Create(beads.Bead{Title: "blocker", Type: "task", Status: "open"})
		if err != nil {
			t.Fatalf("creating blocker: %v", err)
		}
		if err := work.DepAdd(source.ID, blocker.ID, "blocks"); err != nil {
			t.Fatalf("DepAdd(source, blocker): %v", err)
		}
		return deps, source, blocker
	}

	t.Run("open blocker outside the workflow store is dropped and warned about", func(t *testing.T) {
		deps, source, blocker := newSplit(t)
		if _, err := deps.GraphStore.Get(blocker.ID); err == nil {
			t.Fatal("fixture blocker is resolvable from the graph store; the case under test is gone")
		}

		result := attachGraphWorkflow(t, deps, source.ID)
		for _, b := range workflowBeads(t, deps.GraphStore, result.WorkflowID) {
			if containsID(blockingDepIDs(t, deps.GraphStore, b.ID), blocker.ID) {
				t.Errorf("workflow bead %s carries a blocks edge to %s, which the workflow store cannot resolve", b.ID, blocker.ID)
			}
		}
		found := false
		for _, warning := range result.BeadWarnings {
			if strings.Contains(warning, "cannot resolve") && strings.Contains(warning, blocker.ID) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("BeadWarnings = %v, want a warning that %s cannot be resolved", result.BeadWarnings, blocker.ID)
		}
	})

	t.Run("closed blocker outside the workflow store is silently satisfied", func(t *testing.T) {
		deps, source, blocker := newSplit(t)
		if err := deps.Store.Close(blocker.ID); err != nil {
			t.Fatalf("closing blocker: %v", err)
		}

		result := attachGraphWorkflow(t, deps, source.ID)
		for _, b := range workflowBeads(t, deps.GraphStore, result.WorkflowID) {
			if containsID(blockingDepIDs(t, deps.GraphStore, b.ID), blocker.ID) {
				t.Errorf("workflow bead %s carries a blocks edge to closed out-of-store blocker %s", b.ID, blocker.ID)
			}
		}
		for _, warning := range result.BeadWarnings {
			if strings.Contains(warning, blocker.ID) {
				t.Errorf("BeadWarnings = %v, want no warning for a closed blocker", result.BeadWarnings)
			}
		}
	})
}
