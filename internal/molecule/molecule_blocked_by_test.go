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

// TestInstantiateEntryBlockedByProjectsSafeTopology checks entry projection and
// the conservative fallback for control graphs.
func TestInstantiateEntryBlockedByProjectsSafeTopology(t *testing.T) {
	for _, tc := range []struct {
		name    string
		deps    []formula.RecipeDep
		control bool
		want    map[string]bool
	}{
		{"chain", []formula.RecipeDep{{StepID: "b", DependsOnID: "a", Type: "blocks"}}, false, map[string]bool{"root": true, "a": true}},
		{"multiple entries", []formula.RecipeDep{{StepID: "c", DependsOnID: "a", Type: "blocks"}, {StepID: "c", DependsOnID: "b", Type: "blocks"}}, false, map[string]bool{"root": true, "a": true, "b": true}},
		{"control fallback", []formula.RecipeDep{{StepID: "b", DependsOnID: "a", Type: "blocks"}}, true, map[string]bool{"root": true, "a": true, "b": true, "c": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := beads.NewMemStore()
			blocker, err := store.Create(beads.Bead{Title: "blocker", Type: "task"})
			if err != nil {
				t.Fatal(err)
			}
			recipe := &formula.Recipe{Name: "root", Steps: []formula.RecipeStep{
				{ID: "root", IsRoot: true, Type: "task"}, {ID: "a", Type: "task"}, {ID: "b", Type: "task"}, {ID: "c", Type: "task"},
			}, Deps: tc.deps}
			if tc.name == "chain" {
				recipe.Steps = recipe.Steps[:3]
			}
			if tc.control {
				recipe.Steps[2].Metadata = map[string]string{beadmeta.KindMetadataKey: beadmeta.KindScopeCheck}
			}
			result, err := Instantiate(context.Background(), store, recipe, Options{EntryBlockedBy: []string{blocker.ID}})
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range recipe.Steps {
				deps, err := store.DepList(result.IDMapping[step.ID], "down")
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, dep := range deps {
					if dep.Type == "blocks" && dep.DependsOnID == blocker.ID {
						found = true
					}
				}
				if found != tc.want[step.ID] {
					t.Errorf("step %s blocker edge = %v, want %v", step.ID, found, tc.want[step.ID])
				}
			}
		})
	}
}

func TestEntryBlockedByDeduplicatesBlockers(t *testing.T) {
	recipe := &formula.Recipe{Steps: []formula.RecipeStep{{ID: "root", IsRoot: true}, {ID: "a"}, {ID: "b"}}, Deps: []formula.RecipeDep{{StepID: "b", DependsOnID: "a", Type: "blocks"}}}
	for _, tc := range []struct {
		name string
		opts Options
		want map[string]int
	}{
		{"repeated", Options{EntryBlockedBy: []string{"x", " x ", "x"}}, map[string]int{"root": 1, "a": 1, "b": 0}},
		{"overlap", Options{EntryBlockedBy: []string{"x"}, BlockedBy: []string{" x "}}, map[string]int{"root": 1, "a": 1, "b": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expanded := expandBlockedBy(recipe, expandEntryBlockedBy(recipe, tc.opts))
			counts := make(map[string]int)
			for _, dep := range expanded.ExternalDeps {
				if dep.DependsOnID == "x" && dep.Type == "blocks" {
					counts[dep.StepID]++
				}
			}
			for step, want := range tc.want {
				if counts[step] != want {
					t.Errorf("step %s edges = %d, want %d", step, counts[step], want)
				}
			}
		})
	}
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
