package orders

import (
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/beadstest"
)

// actorStampedCreate is one create the fake store stamped: the title of the
// bead, the actor that became its created_by, and whether the caller named the
// actor explicitly (the capability path) or the store supplied its own.
type actorStampedCreate struct {
	title  string
	actor  string
	viaCap bool
}

// actorStampingStore is a beads.Store that stamps creates the way the native
// Dolt store does: Create uses the store's own actor, and CreateWithActor uses
// the actor the caller names. It records both, which is exactly the created_by
// column a bead ends up with — the bead value itself carries no such field.
type actorStampingStore struct {
	beads.Store
	actor string

	mu      sync.Mutex
	creates []actorStampedCreate
}

func newActorStampingStore(actor string) *actorStampingStore {
	return &actorStampingStore{Store: beads.NewMemStore(), actor: actor}
}

func (s *actorStampingStore) Create(b beads.Bead) (beads.Bead, error) {
	return s.recordCreate(b, s.actor, false)
}

// CreateWithActor implements beads.ActorCreateStore.
func (s *actorStampingStore) CreateWithActor(b beads.Bead, actor string) (beads.Bead, error) {
	return s.recordCreate(b, actor, true)
}

func (s *actorStampingStore) recordCreate(b beads.Bead, actor string, viaCap bool) (beads.Bead, error) {
	created, err := s.Store.Create(b)
	if err != nil {
		return created, err
	}
	s.mu.Lock()
	s.creates = append(s.creates, actorStampedCreate{title: b.Title, actor: actor, viaCap: viaCap})
	s.mu.Unlock()
	return created, nil
}

func (s *actorStampingStore) stampedCreates() []actorStampedCreate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]actorStampedCreate(nil), s.creates...)
}

// TestCreateRunStampsTheOrderActor pins the created_by a tracking bead carries.
//
// The bead is born in the controller process, whose store actor is
// "controller", so a create that names no actor attributes every order run to
// the controller and the dashboard cannot tell which order fired.
func TestCreateRunStampsTheOrderActor(t *testing.T) {
	store := newActorStampingStore("controller")
	st := NewStore(beads.OrdersStore{Store: store})

	if _, err := st.CreateRun("dolt-health:rig:jikerental", RunOpts{Actor: "order:dolt-health"}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	creates := store.stampedCreates()
	if len(creates) != 1 {
		t.Fatalf("creates = %+v, want exactly the one tracking bead", creates)
	}
	if creates[0].title != "order:dolt-health:rig:jikerental" {
		t.Errorf("created title = %q, want the tracking title order:dolt-health:rig:jikerental", creates[0].title)
	}
	if !creates[0].viaCap {
		t.Error("CreateRun created the tracking bead without naming an actor; the store's own actor (the controller) becomes created_by")
	}
	if creates[0].actor != "order:dolt-health" {
		t.Errorf("created_by actor = %q, want order:dolt-health", creates[0].actor)
	}
}

// TestCreateRunWithoutAnActorUsesTheStoreActor keeps the attribution opt-in:
// a caller that names no actor gets the store's own, so the front door does not
// silently rename every create it makes.
func TestCreateRunWithoutAnActorUsesTheStoreActor(t *testing.T) {
	store := newActorStampingStore("controller")
	st := NewStore(beads.OrdersStore{Store: store})

	if _, err := st.CreateRun("dolt-health", RunOpts{}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	creates := store.stampedCreates()
	if len(creates) != 1 {
		t.Fatalf("creates = %+v, want exactly the one tracking bead", creates)
	}
	if creates[0].viaCap {
		t.Error("CreateRun named an actor for a run with none configured")
	}
	if creates[0].actor != "controller" {
		t.Errorf("created_by actor = %q, want the store's own controller actor", creates[0].actor)
	}
}

// TestControllerWritesKeepTheStoreActor is the other half of the invariant: the
// order's actor rides on the tracking bead's birth alone. The outcome stamp, the
// cursor, the close and a plain controller create all keep the store's own
// actor, so the controller's own writes stay attributed to the controller.
func TestControllerWritesKeepTheStoreActor(t *testing.T) {
	store := newActorStampingStore("controller")
	st := NewStore(beads.OrdersStore{Store: store})

	run, err := st.CreateRun("dolt-health", RunOpts{Actor: "order:dolt-health"})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := st.SetOutcome(run.ID, RunOutcomeExec); err != nil {
		t.Fatalf("SetOutcome: %v", err)
	}
	if err := st.SetCursor(run.ID, "dolt-health", EventCursor(7)); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	if err := st.CloseRun(run.ID, "dispatch finished"); err != nil {
		t.Fatalf("CloseRun: %v", err)
	}
	// A create the controller makes for its own reasons: it takes the plain
	// Create path, so the store stamps its own actor.
	if _, err := store.Create(beads.Bead{Title: "controller housekeeping"}); err != nil {
		t.Fatalf("controller create: %v", err)
	}

	creates := store.stampedCreates()
	if len(creates) != 2 {
		t.Fatalf("creates = %+v, want the tracking bead and the controller's own bead", creates)
	}
	for _, c := range creates[1:] {
		if c.viaCap || c.actor != "controller" {
			t.Errorf("controller create %q stamped actor %q (named=%v), want the store's own controller actor", c.title, c.actor, c.viaCap)
		}
	}
}

// TestCreateRunFallsBackWithoutTheActorCapability keeps every provider working:
// a store that cannot stamp a per-create actor still creates the tracking bead,
// attributed to its own actor as before.
func TestCreateRunFallsBackWithoutTheActorCapability(t *testing.T) {
	rec := beadstest.NewRecordingStore(beads.NewMemStore())
	st := NewStore(beads.OrdersStore{Store: rec})

	if _, err := st.CreateRun("dolt-health", RunOpts{Actor: "order:dolt-health"}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if calls := rec.CallsForOp("Create"); len(calls) != 1 {
		t.Fatalf("Create calls = %d, want 1 through the ordinary create path", len(calls))
	}
}
