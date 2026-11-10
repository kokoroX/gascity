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

// SourceBlockerReport classifies the still-open source blockers a graph.v2
// workflow attach inherits from its source bead. It is a read-only preview of
// what the real launch does: local blockers become direct blocks edges;
// remote blockers become cross-store gates that hold the workflow.
type SourceBlockerReport struct {
	// WorkflowBlockers are open blockers the workflow store resolves, so a
	// real sling writes direct dependency edges to each of them.
	WorkflowBlockers []string
	// RemoteBlockers are open (or unreadable) blockers whose holding copy is
	// not in the workflow store, so a real sling gates every step on them
	// instead of writing an edge.
	RemoteBlockers []RemoteSourceBlocker
	// DirectUnresolved are missing local blockers referenced by direct edges.
	DirectUnresolved []string
	// Unresolved are missing remote blockers held by cross-store gates.
	Unresolved []string
}

// RemoteSourceBlocker is a source blocker whose holding copy lives outside the
// workflow store.
type RemoteSourceBlocker struct {
	// ID is the blocker bead ID.
	ID string
	// Open reports that at least one serving store holds an unsatisfied copy of
	// the blocker. False means the scan could not read or find a copy: the real
	// sling still gates on it, but the preview cannot claim it is open.
	Open bool
}

// Empty reports whether the source bead contributes no blocker to the
// workflow.
func (r SourceBlockerReport) Empty() bool {
	return len(r.WorkflowBlockers) == 0 && len(r.RemoteBlockers) == 0 && len(r.DirectUnresolved) == 0 && len(r.Unresolved) == 0
}

// SourceBlockerPreview reports the still-open blockers the source bead beadID
// would impose on a graph.v2 workflow attach, classifying each by where the
// real launch can observe it. It reuses sourceBlockerIDs -- the same
// classification the launch applies -- and drops blockers that no longer hold
// (all copies satisfied), which block nothing. It only reads: no edge, gate,
// or bead is written.
func SourceBlockerPreview(deps SlingDeps, beadID string) (SourceBlockerReport, error) {
	blockerIDs, remote, unresolved, err := sourceBlockerIDs(deps, beadID)
	if err != nil {
		return SourceBlockerReport{}, err
	}

	graphStore := deps.GraphStore
	report := SourceBlockerReport{}
	for _, id := range blockerIDs {
		blocker, getErr := graphStore.Get(id)
		switch {
		case getErr == nil:
			if beads.DependencySatisfied(blocker.Status, beads.ReadinessWorkOutcome(blocker.Metadata)) {
				continue
			}
			report.WorkflowBlockers = append(report.WorkflowBlockers, id)
		case errors.Is(getErr, beads.ErrNotFound):
			// A same-store dependency can point at a bead that does not exist.
			// The real launch still writes the edge, so the workflow waits on a
			// bead that resolves nowhere; report it where no resolving store is
			// reported.
			report.DirectUnresolved = append(report.DirectUnresolved, id)
		default:
			return SourceBlockerReport{}, fmt.Errorf("resolving blocker %s of %s: %w", id, beadID, getErr)
		}
	}

	// Unresolved blockers are gated too; keep the buckets disjoint so a reader
	// sees each blocker under the classification that matters most.
	resolvedElsewhere := make(map[string]bool, len(report.DirectUnresolved)+len(unresolved))
	for _, id := range report.DirectUnresolved {
		resolvedElsewhere[id] = true
	}
	for _, id := range unresolved {
		if resolvedElsewhere[id] {
			continue
		}
		resolvedElsewhere[id] = true
		report.Unresolved = append(report.Unresolved, id)
	}
	for _, blocker := range remote {
		if resolvedElsewhere[blocker.id] {
			continue
		}
		report.RemoteBlockers = append(report.RemoteBlockers, RemoteSourceBlocker{ID: blocker.id, Open: blocker.foundOpen})
	}

	slices.Sort(report.WorkflowBlockers)
	slices.Sort(report.DirectUnresolved)
	slices.Sort(report.Unresolved)
	slices.SortFunc(report.RemoteBlockers, func(a, b RemoteSourceBlocker) int { return strings.Compare(a.ID, b.ID) })
	return report, nil
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
	graphStore := deps.GraphStore
	sameStore := deps.GraphStore == (beads.GraphStore{}) || beads.SameStore(deps.GraphStore, deps.Store)
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
