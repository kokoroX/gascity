package sling

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/dispatch"
)

type remoteSourceBlocker struct {
	id        string
	foundOpen bool
}

// sourceBlockerIDs classifies source dependencies as local blocker IDs,
// remote blockers requiring a gate, and blockers absent from the available
// serving stores. The release pass checks all serving stores before
// closing a gate; a missing blocker therefore continues to hold the workflow.
// Single-store launches retain their original direct edges.
func sourceBlockerIDs(deps SlingDeps, beadID string) (blockerIDs []string, remote []remoteSourceBlocker, unresolved []string, err error) {
	depsList, err := deps.Store.DepList(beadID, "down")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("listing dependencies of %s: %w", beadID, err)
	}
	graphStore := deps.graphStore()
	sameStore := deps.GraphStore == nil || deps.GraphStore == deps.Store
	seen := make(map[string]bool, len(depsList))
	for _, dep := range depsList {
		if !beads.IsReadyBlockingDependencyType(dep.Type) {
			continue
		}
		blockerID := strings.TrimSpace(dep.DependsOnID)
		if blockerID == "" || blockerID == beadID || seen[blockerID] {
			continue
		}
		seen[blockerID] = true
		if sameStore {
			blockerIDs = append(blockerIDs, blockerID)
			continue
		}
		if _, err := graphStore.Get(blockerID); err == nil {
			blockerIDs = append(blockerIDs, blockerID)
			continue
		} else if !errors.Is(err, beads.ErrNotFound) {
			return nil, nil, nil, fmt.Errorf("resolving blocker %s of %s in the workflow store: %w", blockerID, beadID, err)
		}
		// Inspect every available serving store before treating a remote blocker
		// as satisfied. A closed copy cannot release an open copy elsewhere.
		stores := []beads.Store{deps.Store} // residency:allow — the selected source plus caller-supplied serving stores; every copy must be checked, not deduplicated by id
		unread := false
		if deps.SourceWorkflowStores != nil {
			views, listErr := deps.SourceWorkflowStores()
			unread = listErr != nil || (deps.SourceWorkflowScanIncomplete != nil && deps.SourceWorkflowScanIncomplete())
			for _, view := range views {
				if view.Store != nil {
					stores = append(stores, view.Store)
				} else {
					unread = true
				}
			}
		}
		found, holding := false, false
		for _, store := range stores {
			blocker, getErr := store.Get(blockerID)
			if errors.Is(getErr, beads.ErrNotFound) {
				continue
			}
			if getErr != nil {
				unread = true
				continue
			}
			found = true
			if !beads.DependencySatisfied(blocker.Status, beads.ReadinessWorkOutcome(blocker.Metadata)) {
				holding = true
			}
		}
		if holding || unread || !found {
			remote = append(remote, remoteSourceBlocker{id: blockerID, foundOpen: holding})
		}
		if !found && !unread {
			unresolved = append(unresolved, blockerID)
		}

	}
	slices.Sort(blockerIDs)
	slices.SortFunc(remote, func(a, b remoteSourceBlocker) int { return strings.Compare(a.id, b.id) })
	slices.Sort(unresolved)
	return blockerIDs, remote, unresolved, nil
}

// unprojectableBlockerWarning identifies source blockers absent from the
// serving stores; their gates hold work until the release pass resolves them.
func unprojectableBlockerWarning(beadID, rootID string, unprojectable []string) string {
	return fmt.Sprintf("%s is blocked by %s, which the workflow store cannot resolve; workflow %s is held by a gate until the blocker is resolved",
		beadID, strings.Join(unprojectable, ","), rootID)
}

func gateSourceBlockers(store beads.GraphStore, remote []remoteSourceBlocker) func(beads.Bead) ([]string, error) {
	if len(remote) == 0 {
		return nil
	}
	return func(root beads.Bead) ([]string, error) {
		var ids []string
		for _, blocker := range remote {
			id, err := dispatch.EnsureCrossStoreGate(store, root, blocker.id, blocker.foundOpen)
			if err != nil {
				return nil, err
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
}
