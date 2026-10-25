package molecule

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/formula"
)

// assertBlocksDependencyOn fails unless issueID has a "blocks" dependency on
// dependsOnID.
func assertBlocksDependencyOn(t *testing.T, store beads.Store, issueID, dependsOnID string) {
	t.Helper()
	deps, err := store.DepList(issueID, "down")
	if err != nil {
		t.Fatalf("DepList(%s): %v", issueID, err)
	}
	for _, dep := range deps {
		if dep.DependsOnID == dependsOnID && dep.Type == "blocks" {
			return
		}
	}
	t.Fatalf("dependencies for %s = %+v, want a blocks dependency on %s", issueID, deps, dependsOnID)
}

// TestInstantiateBlockedByAddsBlocksDepToEveryStep covers the BlockedBy
// shorthand: one existing blocker id becomes a "blocks" dependency on the root
// and on every step, written at create time. Blocks edges are deduplicated per
// (step, blocker), so a caller repeating an id (or padding it with whitespace)
// does not multiply the edges.
func TestInstantiateBlockedByAddsBlocksDepToEveryStep(t *testing.T) {
	store := beads.NewMemStore()

	blockerA, err := store.Create(beads.Bead{Title: "Blocker A", Type: "task"})
	if err != nil {
		t.Fatalf("create blocker A: %v", err)
	}
	blockerB, err := store.Create(beads.Bead{Title: "Blocker B", Type: "task"})
	if err != nil {
		t.Fatalf("create blocker B: %v", err)
	}
	recipe := &formula.Recipe{
		Name: "wf",
		Steps: []formula.RecipeStep{
			{ID: "wf", Title: "Workflow", Type: "task", IsRoot: true, Metadata: map[string]string{beadmeta.KindMetadataKey: beadmeta.KindWorkflow}},
			{ID: "wf.step", Title: "Work", Type: "task"},
		},
		Deps: []formula.RecipeDep{
			{StepID: "wf.step", DependsOnID: "wf", Type: "parent-child"},
		},
	}
	blockedBy := []string{blockerA.ID, " " + blockerB.ID + " ", blockerA.ID, "  "}

	expanded := expandBlockedBy(recipe, Options{BlockedBy: blockedBy})
	if got, want := len(expanded.ExternalDeps), len(recipe.Steps)*2; got != want {
		t.Fatalf("expanded external deps = %d, want %d (one per step per distinct blocker); deps=%+v", got, want, expanded.ExternalDeps)
	}
	if len(expanded.BlockedBy) != 0 {
		t.Errorf("expanded BlockedBy = %v, want cleared so a second expansion cannot duplicate the edges", expanded.BlockedBy)
	}

	result, err := Instantiate(context.Background(), store, recipe, Options{BlockedBy: blockedBy})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	stepID := result.IDMapping["wf.step"]
	for _, beadID := range []string{result.RootID, stepID} {
		assertBlocksDependencyOn(t, store, beadID, blockerA.ID)
		assertBlocksDependencyOn(t, store, beadID, blockerB.ID)
		deps, err := store.DepList(beadID, "down")
		if err != nil {
			t.Fatalf("DepList(%s): %v", beadID, err)
		}
		blockEdges := 0
		for _, dep := range deps {
			if dep.DependsOnID == blockerA.ID && dep.Type == "blocks" {
				blockEdges++
			}
		}
		if blockEdges != 1 {
			t.Errorf("bead %s has %d blocks edges to %s, want 1", beadID, blockEdges, blockerA.ID)
		}
	}
}
