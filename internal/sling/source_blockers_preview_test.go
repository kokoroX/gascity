package sling

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/dispatch"
)

// seedPreviewBlocker creates a source bead and a blocker bead in store and
// links them with depType. An empty depType creates both beads but no
// dependency.
func seedPreviewBlocker(t *testing.T, store beads.Store, depType string) (source, blocker beads.Bead) {
	t.Helper()
	var err error
	source, err = store.Create(beads.Bead{Title: "source", Type: "task", Status: "open"})
	if err != nil {
		t.Fatalf("creating source: %v", err)
	}
	blocker, err = store.Create(beads.Bead{Title: "blocker", Type: "task", Status: "open"})
	if err != nil {
		t.Fatalf("creating blocker: %v", err)
	}
	if depType != "" {
		if err := store.DepAdd(source.ID, blocker.ID, depType); err != nil {
			t.Fatalf("DepAdd(%s): %v", depType, err)
		}
	}
	return source, blocker
}

// assertNoCrossStoreGates proves a preview minted no gate bead, the only
// write a real launch makes for a remote source blocker.
func assertNoCrossStoreGates(t *testing.T, store beads.Store) {
	t.Helper()
	gates, err := store.List(beads.ListQuery{Label: dispatch.CrossStoreGateLabel})
	if err != nil {
		t.Fatalf("listing cross-store gates: %v", err)
	}
	if len(gates) != 0 {
		t.Fatalf("preview created %d cross-store gate beads: %+v", len(gates), gates)
	}
}

// previewSplitDeps returns deps whose work and graph stores are distinct, the
// shape that pushes a source blocker through the cross-store gate path.
func previewSplitDeps(t *testing.T) SlingDeps {
	t.Helper()
	formulaDir := t.TempDir()
	writeTwoStepGraphFormula(t, formulaDir, "graph-work")
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps, _, _ := splitSlingDeps(t, cfg)
	deps.CityPath = t.TempDir()
	return deps
}

func TestSourceBlockerPreviewWorkflowStoreBlocker(t *testing.T) {
	deps := sourceBlockerSlingDeps(t)
	source, blocker := seedPreviewBlocker(t, deps.Store, "blocks")

	report, err := SourceBlockerPreview(deps, source.ID)
	if err != nil {
		t.Fatalf("SourceBlockerPreview: %v", err)
	}
	if got, want := report.WorkflowBlockers, []string{blocker.ID}; !slices.Equal(got, want) {
		t.Errorf("WorkflowBlockers = %v, want %v", got, want)
	}
	if len(report.RemoteBlockers) != 0 {
		t.Errorf("RemoteBlockers = %+v, want none for a same-store blocker", report.RemoteBlockers)
	}
	if len(report.Unresolved) != 0 {
		t.Errorf("Unresolved = %v, want none for a resolvable blocker", report.Unresolved)
	}
	if report.Empty() {
		t.Error("Empty() = true, want false with an open blocker")
	}
	assertNoCrossStoreGates(t, deps.Store)
}

func TestSourceBlockerPreviewClosedBlockerIsNotReported(t *testing.T) {
	deps := sourceBlockerSlingDeps(t)
	source, blocker := seedPreviewBlocker(t, deps.Store, "blocks")
	if err := deps.Store.Close(blocker.ID); err != nil {
		t.Fatalf("closing blocker: %v", err)
	}

	report, err := SourceBlockerPreview(deps, source.ID)
	if err != nil {
		t.Fatalf("SourceBlockerPreview: %v", err)
	}
	if !report.Empty() {
		t.Errorf("report = %+v, want empty for a closed blocker", report)
	}
}

func TestSourceBlockerPreviewBlockingDependencyTypes(t *testing.T) {
	for _, depType := range []string{"blocks", "waits-for", "conditional-blocks"} {
		t.Run(depType+" is reported", func(t *testing.T) {
			deps := sourceBlockerSlingDeps(t)
			source, blocker := seedPreviewBlocker(t, deps.Store, depType)

			report, err := SourceBlockerPreview(deps, source.ID)
			if err != nil {
				t.Fatalf("SourceBlockerPreview: %v", err)
			}
			if got, want := report.WorkflowBlockers, []string{blocker.ID}; !slices.Equal(got, want) {
				t.Errorf("WorkflowBlockers = %v, want %v", got, want)
			}
		})
	}
	for _, depType := range []string{"parent-child", "related"} {
		t.Run(depType+" is not a blocker", func(t *testing.T) {
			deps := sourceBlockerSlingDeps(t)
			source, _ := seedPreviewBlocker(t, deps.Store, depType)

			report, err := SourceBlockerPreview(deps, source.ID)
			if err != nil {
				t.Fatalf("SourceBlockerPreview: %v", err)
			}
			if !report.Empty() {
				t.Errorf("report = %+v, want empty for a %s dependency", report, depType)
			}
		})
	}
}

func TestSourceBlockerPreviewRemoteBlockerNeedsGate(t *testing.T) {
	deps := previewSplitDeps(t)
	source, blocker := seedPreviewBlocker(t, deps.Store, "blocks")

	report, err := SourceBlockerPreview(deps, source.ID)
	if err != nil {
		t.Fatalf("SourceBlockerPreview: %v", err)
	}
	want := []RemoteSourceBlocker{{ID: blocker.ID, Open: true}}
	if !slices.Equal(report.RemoteBlockers, want) {
		t.Errorf("RemoteBlockers = %+v, want %+v", report.RemoteBlockers, want)
	}
	if len(report.WorkflowBlockers) != 0 {
		t.Errorf("WorkflowBlockers = %v, want none: the blocker is not in the workflow store", report.WorkflowBlockers)
	}
	if len(report.Unresolved) != 0 {
		t.Errorf("Unresolved = %v, want none: the work store resolves the blocker", report.Unresolved)
	}
	assertNoCrossStoreGates(t, deps.Store)
	assertNoCrossStoreGates(t, deps.GraphStore)
}

func TestSourceBlockerPreviewUnresolvedBlocker(t *testing.T) {
	deps := sourceBlockerSlingDeps(t)
	source, _ := seedPreviewBlocker(t, deps.Store, "")
	const missingID = "zz-missing.1"
	if err := deps.Store.DepAdd(source.ID, missingID, "blocks"); err != nil {
		t.Fatalf("DepAdd(%s): %v", missingID, err)
	}

	report, err := SourceBlockerPreview(deps, source.ID)
	if err != nil {
		t.Fatalf("SourceBlockerPreview: %v", err)
	}
	if got, want := report.DirectUnresolved, []string{missingID}; !slices.Equal(got, want) {
		t.Errorf("DirectUnresolved = %v, want %v", got, want)
	}
	if len(report.WorkflowBlockers) != 0 {
		t.Errorf("WorkflowBlockers = %v, want none", report.WorkflowBlockers)
	}
	if len(report.RemoteBlockers) != 0 {
		t.Errorf("RemoteBlockers = %+v, want none: an unresolved blocker is reported once, as unresolved", report.RemoteBlockers)
	}
	assertNoCrossStoreGates(t, deps.Store)
}

func TestSourceBlockerPreviewDepListError(t *testing.T) {
	deps := sourceBlockerSlingDeps(t)
	source, _ := seedPreviewBlocker(t, deps.Store, "blocks")
	deps.Store = beads.WorkStore{Store: depListErrStore{Store: deps.Store, err: errors.New("boom")}}

	_, err := SourceBlockerPreview(deps, source.ID)
	if err == nil {
		t.Fatal("SourceBlockerPreview returned no error on a DepList failure")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want the underlying store failure", err)
	}
}
