package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

func TestClosePatrolWispsFiltersAndCloses(t *testing.T) {
	store := beads.NewMemStore()
	eligible, err := store.Create(beads.Bead{Title: "mol-witness-patrol", Type: "molecule", Assignee: "rig/gastown.witness", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	inProgress := "in_progress"
	if err := store.Update(eligible.ID, beads.UpdateOpts{Status: &inProgress}); err != nil {
		t.Fatal(err)
	}
	withOwnerEdge, err := store.Create(beads.Bead{Title: "mol-refinery-patrol", Type: "molecule", Assignee: "rig/gastown.refinery", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DepAdd(withOwnerEdge.ID, "gc-999", "parent-child"); err != nil {
		t.Fatal(err)
	}
	withKind, err := store.Create(beads.Bead{Title: "mol-deacon-patrol", Type: "molecule", Assignee: "gastown.deacon", Ephemeral: true, Metadata: beads.StringMap{"gc.kind": "patrol"}})
	if err != nil {
		t.Fatal(err)
	}
	nonPatrol, err := store.Create(beads.Bead{Title: "mol-witness-check", Type: "molecule", Assignee: "rig/gastown.witness", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	wrongAssignee, err := store.Create(beads.Bead{Title: "mol-witness-patrol", Type: "molecule", Assignee: "somebody.else", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := store.Create(beads.Bead{Title: "mol-witness-patrol", Type: "molecule", Assignee: "rig/gastown.witness", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(closed.ID); err != nil {
		t.Fatal(err)
	}

	count, err := closePatrolWisps(store, []string{"rig/gastown.witness", "rig/gastown.refinery", "gastown.deacon"})
	if err != nil {
		t.Fatalf("closePatrolWisps() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("closePatrolWisps() count = %d, want 1", count)
	}
	got, err := store.Get(eligible.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "closed" || got.CloseReason != patrolWispCloseReason || got.Metadata["close_reason"] != patrolWispCloseReason {
		t.Fatalf("eligible bead after close = status %q close reason %q metadata %v", got.Status, got.CloseReason, got.Metadata)
	}
	for _, id := range []string{withOwnerEdge.ID, withKind.ID, nonPatrol.ID, wrongAssignee.ID, closed.ID} {
		got, err := store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		want := "open"
		if id == closed.ID {
			want = "closed"
		}
		if got.Status != want {
			t.Errorf("bead %q status = %q, want %q", id, got.Status, want)
		}
		if id == closed.ID && got.Metadata["close_reason"] != "" {
			t.Errorf("already closed bead close_reason = %q, want empty", got.Metadata["close_reason"])
		}
	}
}

func TestClosePatrolWispsPropagatesStoreErrors(t *testing.T) {
	store := &patrolCleanupErrorStore{Store: beads.NewMemStore(), listErr: errors.New("list unavailable")}
	if _, err := closePatrolWisps(store, []string{"agent"}); err == nil || !strings.Contains(err.Error(), "list unavailable") {
		t.Fatalf("closePatrolWisps() error = %v, want list error", err)
	}

	store = &patrolCleanupErrorStore{Store: beads.NewMemStore(), closeErr: errors.New("close unavailable")}
	_, err := store.Create(beads.Bead{Title: "mol-agent-patrol", Type: "molecule", Assignee: "agent", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closePatrolWisps(store, []string{"agent"}); err == nil || !strings.Contains(err.Error(), "close unavailable") {
		t.Fatalf("closePatrolWisps() error = %v, want close error", err)
	}
}

type patrolCleanupErrorStore struct {
	beads.Store
	listErr  error
	closeErr error
}

func (s *patrolCleanupErrorStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Store.List(query)
}

func (s *patrolCleanupErrorStore) CloseAll(ids []string, metadata map[string]string) (int, error) {
	if s.closeErr != nil {
		return 0, s.closeErr
	}
	return s.Store.CloseAll(ids, metadata)
}

func TestPatrolAssigneesByScope(t *testing.T) {
	cfg := &config.City{Agents: []config.Agent{
		{Name: "deacon", Scope: "city"},
		{Name: "witness", Scope: "rig", Dir: "frontend"},
		{Name: "unscoped-city"},
		{Name: "inline-rig", Dir: "frontend"},
	}}
	city := patrolAssigneesForCity(cfg)
	if strings.Join(city, ",") != "deacon,unscoped-city" {
		t.Fatalf("city assignees = %v", city)
	}
	rig := patrolAssigneesForRig(cfg, config.Rig{Name: "frontend"})
	if strings.Join(rig, ",") != "frontend/witness,frontend/inline-rig" {
		t.Fatalf("rig assignees = %v", rig)
	}
}

func TestStopAndRigSuspendKeepPatrolWispsFlags(t *testing.T) {
	for _, cmd := range []*cobra.Command{newStopCmd(nil, nil), newRigSuspendCmd(nil, nil)} {
		flag := cmd.Flags().Lookup("keep-patrol-wisps")
		if flag == nil || flag.DefValue != "false" {
			t.Errorf("%s --keep-patrol-wisps flag = %v", cmd.Name(), flag)
		}
	}
}
