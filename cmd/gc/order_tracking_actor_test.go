package main

import (
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/orders"
)

// orderActorStampedCreate is one create a store stamped: the title of the bead,
// the actor that became its created_by, and whether the caller named that actor
// (the per-create capability) or the store supplied its own.
type orderActorStampedCreate struct {
	title  string
	actor  string
	viaCap bool
}

// orderActorStampingStore stamps creates the way the native Dolt store does —
// Create uses the store's own actor, CreateWithActor the actor the caller names
// — and records both. The bead value carries no created_by field, so the actor
// a create named IS the attribution column the bead lands with.
type orderActorStampingStore struct {
	*beads.MemStore
	actor string

	mu      sync.Mutex
	creates []orderActorStampedCreate
}

func newOrderActorStampingStore(actor, idPrefix string) *orderActorStampingStore {
	mem := beads.NewMemStore()
	mem.IDPrefix = idPrefix
	return &orderActorStampingStore{MemStore: mem, actor: actor}
}

func (s *orderActorStampingStore) Create(b beads.Bead) (beads.Bead, error) {
	return s.recordCreate(b, s.actor, false)
}

// CreateWithActor implements beads.ActorCreateStore.
func (s *orderActorStampingStore) CreateWithActor(b beads.Bead, actor string) (beads.Bead, error) {
	return s.recordCreate(b, actor, true)
}

func (s *orderActorStampingStore) recordCreate(b beads.Bead, actor string, viaCap bool) (beads.Bead, error) {
	created, err := s.MemStore.Create(b)
	if err != nil {
		return created, err
	}
	s.mu.Lock()
	s.creates = append(s.creates, orderActorStampedCreate{title: b.Title, actor: actor, viaCap: viaCap})
	s.mu.Unlock()
	return created, nil
}

func (s *orderActorStampingStore) stampedCreates() []orderActorStampedCreate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]orderActorStampedCreate(nil), s.creates...)
}

// TestOrderDispatchStampsTrackingBeadWithOrderActor pins the attribution of the
// bead a scheduled order writes on every run.
//
// The tracking bead is created inside the CONTROLLER process, whose store was
// opened with the controller as its actor, so the order that fired was
// indistinguishable from the controller in every bead view. The dispatch names
// the order for that one create; the controller's own writes keep its actor.
func TestOrderDispatchStampsTrackingBeadWithOrderActor(t *testing.T) {
	cityPath, cfg, a := newExecOrderFixture(t)
	workStore := beads.NewMemStore()
	workStore.IDPrefix = "mc"
	binding := newOrderActorStampingStore("controller", "gcg")

	var rec memRecorder
	m := newSplitOrderDispatcher(t, cityPath, cfg, []orders.Order{a}, workStore, binding, &rec)
	dispatchOrderTick(t, m, cityPath)

	tracking := onlyTrackingBead(t, binding)
	creates := binding.stampedCreates()
	if len(creates) != 1 {
		t.Fatalf("binding creates = %+v, want exactly the one tracking bead", creates)
	}
	got := creates[0]
	if got.title != "order:dolt-health" {
		t.Errorf("tracking create title = %q, want order:dolt-health (the bead %s the dispatch closed)", got.title, tracking.ID)
	}
	if !got.viaCap {
		t.Error("the tracking bead was created without naming the order; created_by is the controller process's actor")
	}
	if got.actor != "order:dolt-health" {
		t.Errorf("tracking bead created_by = %q, want order:dolt-health", got.actor)
	}

	// The controller's own writes on the same store are unaffected: a create it
	// makes for its own reasons still lands as the controller.
	if _, err := binding.Create(beads.Bead{Title: "controller housekeeping"}); err != nil {
		t.Fatalf("controller create: %v", err)
	}
	creates = binding.stampedCreates()
	if len(creates) != 2 {
		t.Fatalf("binding creates = %+v, want the tracking bead and the controller's own bead", creates)
	}
	if last := creates[1]; last.viaCap || last.actor != "controller" {
		t.Errorf("controller create %q stamped created_by %q (named=%v), want the store's own controller actor", last.title, last.actor, last.viaCap)
	}
}

// TestOrderTrackingActorSurvivesTheControllerStoreChain pins the production
// shape of the controller's store: a policy wrapper over an in-memory read cache
// over the store the factory opened. The per-create actor has to reach the leaf
// through both layers — a layer that dropped the capability would not fail, it
// would quietly attribute the tracking bead to the controller again.
func TestOrderTrackingActorSurvivesTheControllerStoreChain(t *testing.T) {
	backing := newOrderActorStampingStore("controller", "gc")
	cached := beads.NewCachingStore(backing, nil)
	policy := wrapStoreWithBeadPolicies(cached, &config.City{})
	st := orders.NewStore(beads.OrdersStore{Store: policy})

	if _, err := st.CreateRun("dolt-health", orders.RunOpts{Actor: "order:dolt-health"}); err != nil {
		t.Fatalf("CreateRun through the controller's store chain: %v", err)
	}

	creates := backing.stampedCreates()
	if len(creates) != 1 {
		t.Fatalf("leaf creates = %+v, want exactly the one tracking bead", creates)
	}
	if !creates[0].viaCap || creates[0].actor != "order:dolt-health" {
		t.Errorf("tracking bead reached the leaf as created_by %q (named=%v), want order:dolt-health", creates[0].actor, creates[0].viaCap)
	}
}
