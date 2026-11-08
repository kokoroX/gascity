package beads_test

import (
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// actorStampingBacking stamps creates the way the native Dolt store does —
// Create uses the store's own actor, CreateWithActor the actor the caller names
// — and records the actor each create carried.
type actorStampingBacking struct {
	*beads.MemStore
	actor string

	mu      sync.Mutex
	creates []string
}

func (s *actorStampingBacking) Create(b beads.Bead) (beads.Bead, error) {
	return s.recordCreate(b, s.actor)
}

// CreateWithActor implements beads.ActorCreateStore.
func (s *actorStampingBacking) CreateWithActor(b beads.Bead, actor string) (beads.Bead, error) {
	return s.recordCreate(b, actor)
}

func (s *actorStampingBacking) recordCreate(b beads.Bead, actor string) (beads.Bead, error) {
	created, err := s.MemStore.Create(b)
	if err != nil {
		return created, err
	}
	s.mu.Lock()
	s.creates = append(s.creates, actor)
	s.mu.Unlock()
	return created, nil
}

func (s *actorStampingBacking) stampedActors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.creates...)
}

// TestCachingStoreForwardsCreateWithActor pins the cache layer's half of the
// per-create actor.
//
// The controller's store is a cache over the store the factory opened, so a
// cache that dropped beads.ActorCreateStore would make the capability
// unreachable for every caller above it — silently, because the fallback still
// writes the bead, just attributed to whoever opened the store. The created bead
// must also land in the cache, exactly as it does through Create.
func TestCachingStoreForwardsCreateWithActor(t *testing.T) {
	backing := &actorStampingBacking{MemStore: beads.NewMemStore(), actor: "controller"}
	cache := beads.NewCachingStore(backing, nil)

	created, err := cache.CreateWithActor(beads.Bead{Title: "order:dolt-health"}, "order:dolt-health")
	if err != nil {
		t.Fatalf("CreateWithActor: %v", err)
	}
	if got := backing.stampedActors(); len(got) != 1 || got[0] != "order:dolt-health" {
		t.Fatalf("backing create actors = %v, want [order:dolt-health]", got)
	}
	cached, err := cache.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after CreateWithActor: %v", err)
	}
	if cached.Title != "order:dolt-health" {
		t.Errorf("cached bead title = %q, want the bead CreateWithActor created", cached.Title)
	}
}

// TestCachingStoreCreateWithActorFallsBackWithoutTheCapability keeps a store
// with no actor concept writable: the cache still creates, under the backing's
// own actor.
func TestCachingStoreCreateWithActorFallsBackWithoutTheCapability(t *testing.T) {
	cache := beads.NewCachingStore(beads.NewMemStore(), nil)

	created, err := cache.CreateWithActor(beads.Bead{Title: "order:dolt-health"}, "order:dolt-health")
	if err != nil {
		t.Fatalf("CreateWithActor: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateWithActor returned no id through the fallback")
	}
	if _, err := cache.Get(created.ID); err != nil {
		t.Fatalf("Get after the fallback create: %v", err)
	}
}
