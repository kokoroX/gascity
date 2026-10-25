package sling

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
)

// sourceBlockerIDs returns the ids of the beads whose ready-blocking
// dependencies keep beadID from being claimed, split into the ids the graph
// store can resolve (blockerIDs, sorted) and the open ids it cannot
// (unprojectable, sorted).
//
// A dependency row lives in one store's dep table and can only reference an id
// that store resolves: the readiness reader left-joins the blocker row, so a
// dangling target yields a status that is never "closed" and the dependent bead
// drops out of Ready permanently. Workflow steps are created in
// deps.graphStore(), so a blocker that store cannot resolve must not be
// projected onto them. The caller records unprojectable ids instead, the way
// drain does; refusing the launch would make every split-store source with an
// out-of-store `blocks` edge unslingable.
//
// Closed blockers project on a single store — readiness ignores them there, and
// dropping the edge would make the single- and split-store shapes disagree. A
// closed blocker the graph store cannot resolve is dropped silently for the
// same reason: it constrains nothing, so the missing edge loses no meaning.
//
// deps.Store is the work store that holds beadID and its dependency edges. When
// graph is not relocated every id it resolves is co-resident, so the probe is
// skipped entirely.
func sourceBlockerIDs(deps SlingDeps, beadID string) (blockerIDs []string, unprojectable []string, err error) {
	depsList, err := deps.Store.DepList(beadID, "down")
	if err != nil {
		return nil, nil, fmt.Errorf("listing dependencies of %s: %w", beadID, err)
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
			return nil, nil, fmt.Errorf("resolving blocker %s of %s in the workflow store: %w", blockerID, beadID, err)
		}
		blocker, err := deps.Store.Get(blockerID)
		if errors.Is(err, beads.ErrNotFound) {
			// Resolvable nowhere: the source's own edge already dangles.
			// Projecting it would copy the dangle onto beads that can never
			// be released by closing the real blocker.
			unprojectable = append(unprojectable, blockerID)
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("resolving blocker %s of %s in the work store: %w", blockerID, beadID, err)
		}
		if convoycore.IsTerminalStatus(blocker.Status) {
			continue
		}
		unprojectable = append(unprojectable, blockerID)
	}
	slices.Sort(blockerIDs)
	slices.Sort(unprojectable)
	return blockerIDs, unprojectable, nil
}

// unprojectableBlockerWarning describes a workflow launched without a source
// constraint it could not express. It names the source, the blockers, and the
// root so the omission is attributable from the warning alone.
func unprojectableBlockerWarning(beadID, rootID string, unprojectable []string) string {
	return fmt.Sprintf("%s is blocked by %s, which the workflow store cannot resolve; workflow %s runs without that constraint",
		beadID, strings.Join(unprojectable, ","), rootID)
}
