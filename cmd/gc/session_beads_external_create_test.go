package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// A configured named session bead created outside the controller (as
// `gc session wake` does) lands in the backing store but not in the
// controller's primed CachingStore snapshot. syncSessionBeads must adopt that
// bead instead of minting a second one: the session-name availability check
// reads backing history, so a fresh create collides with "already belongs to"
// on every tick and the existing bead stays stuck in start-pending.
func TestSyncSessionBeads_AdoptsExternallyCreatedNamedSessionMissingFromCache(t *testing.T) {
	backing := beads.NewMemStore()
	store := beads.NewCachingStoreForTest(backing, nil)
	if err := store.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	clk := &clock.Fake{Time: time.Date(2026, 10, 18, 16, 12, 41, 0, time.UTC)}
	sp := runtime.NewFake()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{
			{Name: "refinery", Dir: "myrig"},
		},
		NamedSessions: []config.NamedSession{
			{Template: "refinery", Dir: "myrig", Mode: "on_demand"},
		},
	}

	// Written straight to the backing store, bypassing the cache, the same
	// way a separate `gc session wake` process creates it.
	external, err := backing.Create(beads.Bead{
		Title:  "myrig/refinery",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel, "template:myrig/refinery"},
		Metadata: map[string]string{
			"session_name":               "myrig--refinery",
			"session_name_explicit":      "true",
			"alias":                      "myrig/refinery",
			"template":                   "myrig/refinery",
			"session_origin":             "named",
			"state":                      "start-pending",
			"pending_create_claim":       "true",
			"wake_request":               "explicit",
			namedSessionMetadataKey:      "true",
			namedSessionIdentityMetadata: "myrig/refinery",
			namedSessionModeMetadata:     "on_demand",
		},
	})
	if err != nil {
		t.Fatalf("creating external session bead: %v", err)
	}

	cachedView, err := store.List(beads.ListQuery{Type: sessionBeadType})
	if err != nil {
		t.Fatalf("listing cached session beads: %v", err)
	}
	if len(cachedView) != 0 {
		t.Fatalf("precondition: cache should not see the external bead yet, got %d rows", len(cachedView))
	}

	ds := map[string]TemplateParams{
		"myrig--refinery": {
			TemplateName:            "myrig/refinery",
			InstanceName:            "myrig/refinery",
			Alias:                   "myrig/refinery",
			Command:                 "claude",
			ConfiguredNamedIdentity: "myrig/refinery",
			ConfiguredNamedMode:     "on_demand",
		},
	}

	var stderr bytes.Buffer
	index := syncSessionBeads("", store, ds, sp, allConfiguredDS(ds), cfg, clk, &stderr, false)

	if strings.Contains(stderr.String(), "already belongs to") {
		t.Fatalf("sync collided with the external bead instead of adopting it:\n%s", stderr.String())
	}
	if got := index["myrig--refinery"]; got != external.ID {
		t.Fatalf("open index for myrig--refinery = %q, want adopted external bead %s (stderr: %s)", got, external.ID, stderr.String())
	}

	all, err := backing.List(beads.ListQuery{Type: sessionBeadType, IncludeClosed: true})
	if err != nil {
		t.Fatalf("listing backing session beads: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected exactly the external session bead, got %d: %+v", len(all), all)
	}

	// The live read must also absorb the row so later ticks hit the cache.
	cachedView, err = store.List(beads.ListQuery{Type: sessionBeadType})
	if err != nil {
		t.Fatalf("listing cached session beads after sync: %v", err)
	}
	if len(cachedView) != 1 || cachedView[0].ID != external.ID {
		t.Fatalf("cache after sync = %+v, want only %s", cachedView, external.ID)
	}

	// A second tick must stay quiet: no new beads, no collision.
	stderr.Reset()
	syncSessionBeads("", store, ds, sp, allConfiguredDS(ds), cfg, clk, &stderr, false)
	if strings.Contains(stderr.String(), "already belongs to") {
		t.Fatalf("second tick collided:\n%s", stderr.String())
	}
	all, err = backing.List(beads.ListQuery{Type: sessionBeadType, IncludeClosed: true})
	if err != nil {
		t.Fatalf("listing backing session beads after second tick: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("second tick created extra beads: %d", len(all))
	}
}

// commitAfterFirstLiveList models an out-of-controller create that commits
// right after the controller's first live session_name lookup misses, i.e.
// between that lookup and the identifier-locked availability check.
type commitAfterFirstLiveList struct {
	beads.Store
	pending   *beads.Bead
	committed *beads.Bead
}

func (s *commitAfterFirstLiveList) List(query beads.ListQuery) ([]beads.Bead, error) {
	items, err := s.Store.List(query)
	if query.Live && s.pending != nil {
		created, createErr := s.Create(*s.pending)
		if createErr != nil {
			return nil, createErr
		}
		s.pending = nil
		s.committed = &created
	}
	return items, err
}

func TestSyncSessionBeads_AdoptsNamedSessionCommittedBeforeIdentifierLock(t *testing.T) {
	backing := &commitAfterFirstLiveList{
		Store: beads.NewMemStore(),
		pending: &beads.Bead{
			Title:  "myrig/refinery",
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel, "template:myrig/refinery"},
			Metadata: map[string]string{
				"session_name":               "myrig--refinery",
				"session_name_explicit":      "true",
				"alias":                      "myrig/refinery",
				"template":                   "myrig/refinery",
				"session_origin":             "named",
				"state":                      "start-pending",
				"pending_create_claim":       "true",
				namedSessionMetadataKey:      "true",
				namedSessionIdentityMetadata: "myrig/refinery",
				namedSessionModeMetadata:     "on_demand",
			},
		},
	}
	store := beads.NewCachingStoreForTest(backing, nil)
	if err := store.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	clk := &clock.Fake{Time: time.Date(2026, 10, 19, 2, 27, 12, 0, time.UTC)}
	sp := runtime.NewFake()
	cfg := &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "refinery", Dir: "myrig"}},
		NamedSessions: []config.NamedSession{{Template: "refinery", Dir: "myrig", Mode: "on_demand"}},
	}
	ds := map[string]TemplateParams{
		"myrig--refinery": {
			TemplateName:            "myrig/refinery",
			InstanceName:            "myrig/refinery",
			Alias:                   "myrig/refinery",
			Command:                 "claude",
			ConfiguredNamedIdentity: "myrig/refinery",
			ConfiguredNamedMode:     "on_demand",
		},
	}

	var stderr bytes.Buffer
	index := syncSessionBeads("", store, ds, sp, allConfiguredDS(ds), cfg, clk, &stderr, false)

	if backing.committed == nil {
		t.Fatal("precondition: the racing create never committed")
	}
	if strings.Contains(stderr.String(), "already belongs to") || strings.Contains(stderr.String(), "unavailable") {
		t.Fatalf("sync collided with the bead committed before the lock:\n%s", stderr.String())
	}
	if got := index["myrig--refinery"]; got != backing.committed.ID {
		t.Fatalf("open index for myrig--refinery = %q, want %s (stderr: %s)", got, backing.committed.ID, stderr.String())
	}
	all, err := backing.Store.List(beads.ListQuery{Type: sessionBeadType, IncludeClosed: true})
	if err != nil {
		t.Fatalf("listing backing session beads: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected only the racing bead, got %d", len(all))
	}
}
