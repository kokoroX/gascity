package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// These tests pin the source-blocker disclosure of `gc sling --dry-run`: a
// graph.v2 formula attach projects the source bead's still-open blocking
// dependencies onto workflow entry steps, so the preview must say so instead of
// letting an operator conclude that sling checks no dependencies at all.

// graphV2DryRunCity returns a city config whose city formula layer resolves the
// graph-work graph.v2 formula writeGraphV2FormulaForDryRunTest writes.
func graphV2DryRunCity(t *testing.T) *config.City {
	t.Helper()
	formulaDir := t.TempDir()
	writeGraphV2FormulaForDryRunTest(t, formulaDir)
	return &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Daemon:        config.DaemonConfig{FormulaV2: boolPtr(true)},
		FormulaLayers: config.FormulaLayers{City: []string{formulaDir}},
	}
}

const (
	// dryRunSourceBead and dryRunBlockerBead are the IDs the source-blocker
	// dry-run fixtures seed.
	dryRunSourceBead  = "BL-42"
	dryRunBlockerBead = "BL-42.1"
)

// seededBlockedStore returns a store holding the source and blocker beads, both
// open, with a depType dependency from the source onto the blocker.
func seededBlockedStore(t *testing.T, depType string) beads.Store {
	t.Helper()
	store := seededStore(dryRunSourceBead, dryRunBlockerBead)
	if depType != "" {
		if err := store.DepAdd(dryRunSourceBead, dryRunBlockerBead, depType); err != nil {
			t.Fatalf("DepAdd(%s): %v", depType, err)
		}
	}
	return store
}

// runDryRunSingle drives one bead's dry-run preview and returns what it
// printed. The fake querier answers with the bead itself and no children, so
// the preview reaches the formula sections instead of a blocking-molecule
// error.
func runDryRunSingle(t *testing.T, cfg *config.City, a config.Agent, store beads.Store, onFormula string) (stdout, stderr string, code int) {
	t.Helper()
	q := newFakeChildQuerier()
	if b, err := store.Get(dryRunSourceBead); err == nil {
		q.beadsByID[dryRunSourceBead] = b
	}
	q.childrenOf[dryRunSourceBead] = []beads.Bead{}

	runner := newFakeRunner()
	deps, out, errOut := testDeps(cfg, runtime.NewFake(), runner.run)
	deps.Store = store
	opts := testOpts(a, dryRunSourceBead)
	opts.OnFormula = onFormula
	opts.DryRun = true
	code = doSling(opts, deps, q, out, errOut)
	if len(runner.calls) != 0 {
		t.Errorf("dry-run executed commands: %v", runner.calls)
	}
	return out.String(), errOut.String(), code
}

func TestDryRunDefaultGraphV2FormulaReportsSourceBlockers(t *testing.T) {
	cfg := graphV2DryRunCity(t)
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("graph-work")}
	store := seededBlockedStore(t, "blocks")

	out, stderr, code := runDryRunSingle(t, cfg, a, store, "")
	if code != 0 {
		t.Fatalf("dry-run returned %d, want 0; stderr: %s", code, stderr)
	}
	if !strings.Contains(out, "Source blockers:") {
		t.Fatalf("stdout missing Source blockers section:\n%s", out)
	}
	if !strings.Contains(out, "BL-42.1 (open) — the workflow's entry steps wait until it closes; later steps follow them") {
		t.Errorf("stdout missing the open source blocker line:\n%s", out)
	}
	if strings.Contains(out, "Source blockers: none") {
		t.Errorf("stdout claims no source blockers while one is open:\n%s", out)
	}
}

func TestDryRunDefaultGraphV2FormulaSourceBlockersClosed(t *testing.T) {
	cfg := graphV2DryRunCity(t)
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("graph-work")}
	store := seededBlockedStore(t, "blocks")
	if err := store.Close("BL-42.1"); err != nil {
		t.Fatalf("closing blocker: %v", err)
	}

	out, stderr, code := runDryRunSingle(t, cfg, a, store, "")
	if code != 0 {
		t.Fatalf("dry-run returned %d, want 0; stderr: %s", code, stderr)
	}
	if !strings.Contains(out, "Source blockers: none — the workflow is claimable as soon as it is created") {
		t.Errorf("stdout missing the no-blocker line for a closed blocker:\n%s", out)
	}
	if strings.Contains(out, "BL-42.1") {
		t.Errorf("stdout lists a closed blocker:\n%s", out)
	}
}

func TestDryRunDefaultGraphV2FormulaSourceBlockerTypes(t *testing.T) {
	for _, depType := range []string{"blocks", "waits-for", "conditional-blocks"} {
		t.Run(depType+" blocks the workflow", func(t *testing.T) {
			cfg := graphV2DryRunCity(t)
			a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("graph-work")}
			store := seededBlockedStore(t, depType)

			out, stderr, code := runDryRunSingle(t, cfg, a, store, "")
			if code != 0 {
				t.Fatalf("dry-run returned %d, want 0; stderr: %s", code, stderr)
			}
			if !strings.Contains(out, "BL-42.1 (open) — the workflow's entry steps wait until it closes; later steps follow them") {
				t.Errorf("stdout missing the %s blocker line:\n%s", depType, out)
			}
		})
	}
	for _, depType := range []string{"parent-child", "related"} {
		t.Run(depType+" does not block the workflow", func(t *testing.T) {
			cfg := graphV2DryRunCity(t)
			a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("graph-work")}
			store := seededBlockedStore(t, depType)

			out, stderr, code := runDryRunSingle(t, cfg, a, store, "")
			if code != 0 {
				t.Fatalf("dry-run returned %d, want 0; stderr: %s", code, stderr)
			}
			if !strings.Contains(out, "Source blockers: none — the workflow is claimable as soon as it is created") {
				t.Errorf("stdout should report no blockers for a %s dependency:\n%s", depType, out)
			}
		})
	}
}

func TestDryRunOnGraphV2FormulaReportsSourceBlockers(t *testing.T) {
	cfg := graphV2DryRunCity(t)
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	store := seededBlockedStore(t, "waits-for")

	out, stderr, code := runDryRunSingle(t, cfg, a, store, "graph-work")
	if code != 0 {
		t.Fatalf("dry-run returned %d, want 0; stderr: %s", code, stderr)
	}
	if !strings.Contains(out, "Attach formula:") {
		t.Fatalf("stdout missing the attach formula section:\n%s", out)
	}
	if !strings.Contains(out, "BL-42.1 (open) — the workflow's entry steps wait until it closes; later steps follow them") {
		t.Errorf("stdout missing the --on source blocker line:\n%s", out)
	}
}

func TestDryRunBatchGraphV2FormulaReportsSourceBlockers(t *testing.T) {
	cfg := graphV2DryRunCity(t)
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("graph-work")}
	store := seededStore("CVY-1", "BL-1", "BL-2", "BL-9")
	if err := store.DepAdd("BL-1", "BL-9", "blocks"); err != nil {
		t.Fatalf("DepAdd(BL-1, BL-9): %v", err)
	}

	convoy := beads.Bead{ID: "CVY-1", Type: "convoy", Status: "open"}
	listing := []beads.Bead{
		{ID: "BL-1", Title: "blocked child", ParentID: "CVY-1", Status: "open"},
		{ID: "BL-2", Title: "free child", ParentID: "CVY-1", Status: "open"},
	}
	q := newFakeChildQuerier()
	q.beadsByID["BL-1"] = listing[0]
	q.beadsByID["BL-2"] = listing[1]

	deps, stdout, stderr := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.Store = store
	opts := testOpts(a, "CVY-1")
	opts.DryRun = true
	if code := dryRunBatch(opts, deps, stdout, stderr, convoy, listing, listing, q); code != 0 {
		t.Fatalf("dryRunBatch returned %d, want 0; stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Source blockers (per open child):") {
		t.Fatalf("stdout missing the batch source blocker section:\n%s", out)
	}
	if !strings.Contains(out, "  BL-1: BL-9 (open) — the workflow's entry steps wait until it closes; later steps follow them") {
		t.Errorf("stdout missing BL-1's blocker line:\n%s", out)
	}
	if !strings.Contains(out, "  BL-2: none — claimable as soon as it is created") {
		t.Errorf("stdout missing BL-2's no-blocker line:\n%s", out)
	}
}

func TestDryRunBatchLegacySourceBlockerLookupErrors(t *testing.T) {
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "runner", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("code-review")}
	children := []beads.Bead{{ID: "BL-1", Status: "open"}, {ID: "BL-2", Status: "open"}}
	q := newFakeChildQuerier()
	deps, stdout, stderr := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.Store = depListFailingStore{Store: seededStore("BL-1", "BL-2")}
	opts := testOpts(a, "CVY-1")
	opts.DryRun = true
	if code := dryRunBatch(opts, deps, stdout, stderr, beads.Bead{ID: "CVY-1", Type: "convoy", Status: "open"}, children, children, q); code != 0 {
		t.Fatalf("dryRunBatch returned %d: %s", code, stderr.String())
	}
	for _, id := range []string{"BL-1", "BL-2"} {
		if !strings.Contains(stdout.String(), id+": unknown (") {
			t.Errorf("missing %s error: %s", id, stdout.String())
		}
	}
}

func TestDryRunSourceBlockerUnresolvedDirectAndGate(t *testing.T) {
	for _, split := range []bool{false, true} {
		store := seededBlockedStore(t, "blocks")
		if err := store.Delete(dryRunBlockerBead); err != nil {
			t.Fatal(err)
		}
		deps, _, _ := testDeps(graphV2DryRunCity(t), runtime.NewFake(), newFakeRunner().run)
		deps.Store = store
		if split {
			deps.GraphStore = beads.NewMemStore()
		}
		var lines []string
		dryRunSourceBlockerSection(func(line string) { lines = append(lines, line) }, deps, dryRunSourceBead, true)
		out := strings.Join(lines, "\n")
		want := "entry steps wait on it until it exists and closes"
		if split {
			want = "every workflow step holds on a gate until it resolves"
		}
		if !strings.Contains(out, want) {
			t.Errorf("split=%v: output %q missing %q", split, out, want)
		}
	}
}

func TestDryRunSourceBlockersLookupErrorIsReported(t *testing.T) {
	cfg := graphV2DryRunCity(t)
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("graph-work")}
	store := seededBlockedStore(t, "blocks")

	out, stderr, code := runDryRunSingle(t, cfg, a, depListFailingStore{Store: store}, "")
	if code != 0 {
		t.Fatalf("dry-run returned %d, want 0: a read error must not hard-fail a preview; stderr: %s", code, stderr)
	}
	if !strings.Contains(out, "Source blockers: unknown (") || !strings.Contains(out, "boom") {
		t.Errorf("stdout should report the blocker lookup failure instead of silence:\n%s", out)
	}
}

func TestDryRunLegacyAttachSourceBlockersNotProjected(t *testing.T) {
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("code-review")}

	t.Run("blocked source says so", func(t *testing.T) {
		store := seededBlockedStore(t, "blocks")
		out, stderr, code := runDryRunSingle(t, cfg, a, store, "")
		if code != 0 {
			t.Fatalf("dry-run returned %d, want 0; stderr: %s", code, stderr)
		}
		if !strings.Contains(out, "Source blockers: not projected") {
			t.Errorf("stdout should say a legacy attach projects no blocker:\n%s", out)
		}
		if strings.Contains(out, "the workflow's entry steps wait") {
			t.Errorf("stdout claims projection for a legacy (non-graph.v2) attach:\n%s", out)
		}
	})

	t.Run("unblocked source stays quiet", func(t *testing.T) {
		store := seededStore("BL-42")
		out, stderr, code := runDryRunSingle(t, cfg, a, store, "")
		if code != 0 {
			t.Fatalf("dry-run returned %d, want 0; stderr: %s", code, stderr)
		}
		if strings.Contains(out, "Source blockers") {
			t.Errorf("stdout should stay quiet about blockers for an unblocked source:\n%s", out)
		}
	})
}

// TestDryRunSourceBlockersStoreIsUnchanged pins dry-run purity for the new
// source-blocker disclosure: reading the blockers must not write an edge, a
// gate, or any other bead.
func TestDryRunSourceBlockersStoreIsUnchanged(t *testing.T) {
	cfg := graphV2DryRunCity(t)
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), DefaultSlingFormula: strPtr("graph-work")}
	store := seededStore("BL-42", "BL-42.1", "BL-42.2")
	if err := store.DepAdd("BL-42", "BL-42.1", "blocks"); err != nil {
		t.Fatalf("DepAdd(BL-42, BL-42.1): %v", err)
	}
	if err := store.DepAdd("BL-42", "BL-42.2", "conditional-blocks"); err != nil {
		t.Fatalf("DepAdd(BL-42, BL-42.2): %v", err)
	}

	q := newFakeChildQuerier()
	q.beadsByID["BL-42"] = beads.Bead{ID: "BL-42", Type: "task", Status: "open"}
	q.childrenOf["BL-42"] = []beads.Bead{}
	runner := newFakeRunner()
	deps, stdout, stderr := testDeps(cfg, runtime.NewFake(), runner.run)
	deps.Store = store
	opts := testOpts(a, "BL-42")
	opts.DryRun = true

	before := listAllBeads(t, store)
	beforeDeps, err := store.DepList("BL-42", "down")
	if err != nil {
		t.Fatalf("DepList before: %v", err)
	}

	code := doSling(opts, deps, q, stdout, stderr)
	if code != 0 {
		t.Fatalf("dry-run returned %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "BL-42.1 (open) — the workflow's entry steps wait until it closes; later steps follow them") {
		t.Fatalf("dry-run did not report the open blocker, so the purity assertion would be vacuous:\n%s", stdout.String())
	}

	after := listAllBeads(t, store)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("dry-run mutated the store:\nbefore: %+v\nafter:  %+v", before, after)
	}
	afterDeps, err := store.DepList("BL-42", "down")
	if err != nil {
		t.Fatalf("DepList after: %v", err)
	}
	if !reflect.DeepEqual(beforeDeps, afterDeps) {
		t.Errorf("dry-run changed dependencies: before %+v, after %+v", beforeDeps, afterDeps)
	}
}
