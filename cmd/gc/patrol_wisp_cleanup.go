package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/suspensionstate"
)

const patrolWispCloseReason = "gc shutdown: patrol wisp closed; role re-pours on start"

// closePatrolWisps closes eligible patrol wisps in one store scope.
func closePatrolWisps(store beads.Store, assignees []string) (int, error) {
	if store == nil || len(assignees) == 0 {
		return 0, nil
	}
	rows, err := store.List(beads.ListQuery{Type: "molecule", Assignees: assignees, TierMode: beads.TierWisps})
	if err != nil {
		return 0, fmt.Errorf("listing patrol wisps: %w", err)
	}
	eligible := make([]string, 0, len(rows))
	for _, bead := range rows {
		if !bead.Ephemeral || (bead.Status != "open" && bead.Status != "in_progress") {
			continue
		}
		_, hasKind := bead.Metadata[beadmeta.KindMetadataKey]
		if !isPatrolWispTitle(bead.Title) || hasKind {
			continue
		}
		hasEdge, err := hasPatrolOwnerDepEdge(store, bead.ID)
		if err != nil {
			return 0, fmt.Errorf("checking patrol wisp %q dependencies: %w", bead.ID, err)
		}
		if hasEdge {
			continue
		}
		eligible = append(eligible, bead.ID)
	}
	if len(eligible) == 0 {
		return 0, nil
	}
	closed, err := store.CloseAll(eligible, map[string]string{"close_reason": patrolWispCloseReason})
	if err != nil {
		return closed, fmt.Errorf("closing patrol wisps: %w", err)
	}
	return closed, nil
}

func isPatrolWispTitle(title string) bool {
	if !strings.HasPrefix(title, "mol-") || !strings.HasSuffix(title, "-patrol") {
		return false
	}
	role := strings.TrimSuffix(strings.TrimPrefix(title, "mol-"), "-patrol")
	return role != "" && !strings.Contains(role, "/")
}

// hasPatrolOwnerDepEdge reports whether a patrol wisp has a parent-child edge in either direction.
func hasPatrolOwnerDepEdge(store interface {
	DepList(id, direction string) ([]beads.Dep, error)
}, id string,
) (bool, error) {
	for _, direction := range []string{"down", "up"} {
		deps, err := store.DepList(id, direction)
		if err != nil {
			return false, fmt.Errorf("listing %q dependencies: %w", direction, err)
		}
		for _, dep := range deps {
			if dep.Type == "parent-child" && dep.IssueID != "" && dep.DependsOnID != "" {
				return true, nil
			}
		}
	}
	return false, nil
}

func patrolAssigneesForCity(cfg *config.City) []string {
	var assignees []string
	for _, agent := range cfg.Agents {
		if agent.Scope == "city" || (agent.Scope == "" && agent.Dir == "") {
			assignees = append(assignees, agent.QualifiedName())
		}
	}
	return assignees
}

func patrolAssigneesForRig(cfg *config.City, rig config.Rig) []string {
	var assignees []string
	for _, agent := range cfg.Agents {
		if agent.Scope != "city" && agent.Dir == rig.Name {
			assignees = append(assignees, agent.QualifiedName())
		}
	}
	return assignees
}

func cleanupPatrolWispsInScope(cityPath, scopeRoot string, assignees []string, stderr io.Writer, command string) int {
	if len(assignees) == 0 {
		return 0
	}
	store, err := openStoreAtForCity(scopeRoot, cityPath)
	if err != nil {
		fmt.Fprintf(stderr, "%s: patrol wisp cleanup: opening store %q: %v\n", command, scopeRoot, err) //nolint:errcheck // best-effort stderr
		return 0
	}
	closed, err := closePatrolWisps(store, assignees)
	if err != nil {
		fmt.Fprintf(stderr, "%s: patrol wisp cleanup: %v\n", command, err) //nolint:errcheck // best-effort stderr
	}
	return closed
}

func cleanupPatrolWispsForStop(cityPath string, cfg *config.City, stderr io.Writer) {
	closed := cleanupPatrolWispsInScope(cityPath, cityPath, patrolAssigneesForCity(cfg), stderr, "gc stop")
	suspension, err := loadSuspensionState(fsys.OSFS{}, cityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc stop: patrol wisp cleanup: reading suspension state: %v\n", err) //nolint:errcheck // best-effort stderr
		if closed > 0 {
			fmt.Fprintf(stderr, "gc stop: closed %d patrol wisp(s)\n", closed) //nolint:errcheck // best-effort stderr
		}
		return
	}
	for _, rig := range cfg.Rigs {
		if suspensionstate.EffectiveRigSuspended(suspension, rig.Name, rig.EffectiveSuspendedOnStart()) {
			continue
		}
		scopeRoot := resolveStoreScopeRoot(cityPath, rig.Path)
		closed += cleanupPatrolWispsInScope(cityPath, scopeRoot, patrolAssigneesForRig(cfg, rig), stderr, "gc stop")
	}
	if closed > 0 {
		fmt.Fprintf(stderr, "gc stop: closed %d patrol wisp(s)\n", closed) //nolint:errcheck // best-effort stderr
	}
}
