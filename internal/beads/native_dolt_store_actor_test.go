package beads

import (
	"context"
	"fmt"
	"testing"

	beadslib "github.com/steveyegge/beads"
	"github.com/steveyegge/beads/issueops"
)

func nativeActorCases(t *testing.T, run func(*testing.T, []NativeDoltStoreOption, string)) {
	t.Helper()
	for _, tc := range []struct {
		name string
		opts []NativeDoltStoreOption
		want string
	}{
		{name: "default", want: "gascity"},
		{name: "order", opts: []NativeDoltStoreOption{WithNativeActor("order:dolt-health")}, want: "order:dolt-health"},
		{name: "trimmed", opts: []NativeDoltStoreOption{WithNativeActor("  order:dolt-health  ")}, want: "order:dolt-health"},
		{name: "blank", opts: []NativeDoltStoreOption{WithNativeActor("  ")}, want: "gascity"},
		{name: "empty", opts: []NativeDoltStoreOption{WithNativeActor("")}, want: "gascity"},
	} {
		t.Run(tc.name, func(t *testing.T) { run(t, tc.opts, tc.want) })
	}
}

func nativeActorStore(storage beadslib.Storage, opts []NativeDoltStoreOption) *NativeDoltStore {
	store := newNativeDoltStoreWithStorage(storage, "")
	for _, opt := range opts {
		opt(store)
	}
	return store
}

func TestNativeActorCreatedByCreate(t *testing.T) {
	nativeActorCases(t, func(t *testing.T, opts []NativeDoltStoreOption, want string) {
		var captured *beadslib.Issue
		var capturedActor string
		storage := &nativeDoltStorageSpy{
			createIssue: func(_ context.Context, issue *beadslib.Issue, actor string) error {
				captured = cloneNativeIssueForTest(issue)
				capturedActor = actor
				issue.ID = "gc-created"
				return nil
			},
			getIssue: func(context.Context, string) (*beadslib.Issue, error) {
				return cloneNativeIssueForTest(captured), nil
			},
		}
		store := nativeActorStore(storage, opts)
		for _, ephemeral := range []bool{false, true} {
			for _, create := range []func(Bead) (Bead, error){
				store.Create,
				store.CreateWithForeignID,
				func(b Bead) (created Bead, err error) {
					err = store.Tx("test actor attribution", func(tx Tx) error {
						created, err = tx.Create(b)
						return err
					})
					return created, err
				},
			} {
				captured = nil
				if _, err := create(Bead{ID: "gc-created", Title: "actor attribution", Ephemeral: ephemeral}); err != nil {
					t.Fatalf("create (ephemeral=%v): %v", ephemeral, err)
				}
				if captured == nil {
					t.Fatal("create did not reach storage")
				}
				if captured.CreatedBy != want || capturedActor != want {
					t.Errorf("CreatedBy=%q actor=%q, want %q for both", captured.CreatedBy, capturedActor, want)
				}
				if captured.Ephemeral != ephemeral {
					t.Errorf("Ephemeral=%v, want %v", captured.Ephemeral, ephemeral)
				}
			}
		}
	})
}

func TestNativeActorCreatedByGuardedBatch(t *testing.T) {
	nativeActorCases(t, func(t *testing.T, opts []NativeDoltStoreOption, want string) {
		storage := newBatchTxSpy()
		storage.applier.result = issueops.ApplyBatchResult{Items: []issueops.ItemResult{
			{Kind: issueops.ItemCreate, IssueID: "gc-created", RowVersion: 1},
		}}
		store := nativeActorStore(storage, opts)
		if _, err := store.ApplyGuarded(context.Background(), GuardedBatch{Items: []GuardedItem{
			{Create: &Bead{ID: "gc-created", Title: "actor attribution"}},
		}}); err != nil {
			t.Fatalf("ApplyGuarded: %v", err)
		}
		req := soleBatchRequest(t, storage.applier)
		if len(req.Items) != 1 || req.Items[0].Create == nil || req.Items[0].Create.Issue == nil {
			t.Fatalf("items = %+v, want one create", req.Items)
		}
		issue := req.Items[0].Create.Issue
		if issue.CreatedBy != want || req.Actor != want {
			t.Errorf("CreatedBy=%q actor=%q, want %q for both", issue.CreatedBy, req.Actor, want)
		}
	})
}

func TestNativeActorCreatedByGraphBatch(t *testing.T) {
	nativeActorCases(t, func(t *testing.T, opts []NativeDoltStoreOption, want string) {
		storage := newGraphApplyBatchSpy()
		store := nativeActorStore(storage, opts)
		plan := &GraphApplyPlan{Nodes: []GraphApplyNode{{Key: "root", Title: "actor attribution"}}}
		for _, storageClass := range []StorageClass{StorageDefault, StorageEphemeral, StorageNoHistory} {
			if _, err := store.ApplyGraphPlanWithStorage(context.Background(), plan, storageClass); err != nil {
				t.Fatalf("ApplyGraphPlanWithStorage(%s): %v", storageClass, err)
			}
			req := storage.recorder.requests[len(storage.recorder.requests)-1]
			issue := req.Items[0].Create.Issue
			if issue.CreatedBy != want || req.Actor != want {
				t.Errorf("CreatedBy=%q actor=%q, want %q for both", issue.CreatedBy, req.Actor, want)
			}
			ephemeral, noHistory, err := graphStorageFlags(storageClass)
			if err != nil {
				t.Fatal(err)
			}
			if issue.Ephemeral != ephemeral || issue.NoHistory != noHistory {
				t.Errorf("Ephemeral=%v NoHistory=%v, want %v and %v", issue.Ephemeral, issue.NoHistory, ephemeral, noHistory)
			}
		}
	})
}

func TestNativeActorCreatedByGraphTransaction(t *testing.T) {
	nativeActorCases(t, func(t *testing.T, opts []NativeDoltStoreOption, want string) {
		storage := newGraphApplyBatchSpy()
		calls := 0
		var storageClass StorageClass
		storage.createIssue = func(_ context.Context, issue *beadslib.Issue, actor string) error {
			calls++
			if issue.CreatedBy != want || actor != want {
				t.Fatalf("CreatedBy=%q actor=%q, want %q for both", issue.CreatedBy, actor, want)
			}
			ephemeral, noHistory, err := graphStorageFlags(storageClass)
			if err != nil {
				t.Fatal(err)
			}
			if issue.Ephemeral != ephemeral || issue.NoHistory != noHistory {
				t.Errorf("Ephemeral=%v NoHistory=%v, want %v and %v", issue.Ephemeral, issue.NoHistory, ephemeral, noHistory)
			}
			issue.ID = fmt.Sprintf("gc-created-%d", calls)
			return nil
		}
		store := nativeActorStore(storage, opts)
		plan := &GraphApplyPlan{Nodes: make([]GraphApplyNode, issueops.MaxApplyBatchItems+1)}
		for i := range plan.Nodes {
			plan.Nodes[i] = GraphApplyNode{Key: fmt.Sprintf("n%d", i), Title: "actor attribution"}
		}
		for _, class := range []StorageClass{StorageDefault, StorageEphemeral, StorageNoHistory} {
			storageClass = class
			if _, err := store.ApplyGraphPlanWithStorage(context.Background(), plan, storageClass); err != nil {
				t.Fatalf("ApplyGraphPlanWithStorage(%s): %v", storageClass, err)
			}
		}
		if calls != 3*len(plan.Nodes) || len(storage.recorder.requests) != 0 {
			t.Fatalf("transaction creates=%d batches=%d, want %d and 0", calls, len(storage.recorder.requests), 3*len(plan.Nodes))
		}
	})
}

// TestNativeActorCreateWithActor pins the per-create actor the order front door
// uses for a tracking bead.
//
// The controller's store is opened once, with the controller as its actor, so
// every create it makes would be attributed to the controller — including the
// order-tracking bead a scheduled order's dispatch writes in the controller
// process. CreateWithActor names the actor for that one create; the store's own
// actor keeps every other write it makes.
func TestNativeActorCreateWithActor(t *testing.T) {
	var captured *beadslib.Issue
	var capturedActor string
	storage := &nativeDoltStorageSpy{
		createIssue: func(_ context.Context, issue *beadslib.Issue, actor string) error {
			captured = cloneNativeIssueForTest(issue)
			capturedActor = actor
			issue.ID = "gc-created"
			return nil
		},
		getIssue: func(context.Context, string) (*beadslib.Issue, error) {
			return cloneNativeIssueForTest(captured), nil
		},
	}
	store := nativeActorStore(storage, []NativeDoltStoreOption{WithNativeActor("controller")})

	for _, tc := range []struct {
		name  string
		actor string
		want  string
	}{
		{name: "order", actor: "order:dolt-health", want: "order:dolt-health"},
		{name: "trimmed", actor: "  order:dolt-health  ", want: "order:dolt-health"},
		{name: "blank", actor: "   ", want: "controller"},
		{name: "empty", actor: "", want: "controller"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured, capturedActor = nil, ""
			if _, err := store.CreateWithActor(Bead{Title: "order:dolt-health"}, tc.actor); err != nil {
				t.Fatalf("CreateWithActor: %v", err)
			}
			if captured == nil {
				t.Fatal("CreateWithActor did not reach storage")
			}
			if captured.CreatedBy != tc.want || capturedActor != tc.want {
				t.Errorf("CreatedBy=%q actor=%q, want %q for both", captured.CreatedBy, capturedActor, tc.want)
			}
		})
	}

	captured, capturedActor = nil, ""
	if _, err := store.Create(Bead{Title: "controller housekeeping"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if captured == nil || captured.CreatedBy != "controller" || capturedActor != "controller" {
		t.Fatalf("plain Create after CreateWithActor stamped CreatedBy=%q actor=%q, want the store's own controller actor for both", captured.CreatedBy, capturedActor)
	}
}
