package main

import (
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// clearedReleaseAffinityMetadata returns a metadata map with gc.session_affinity
// and the stranded stamps set to the empty string, for a release that moves a
// claim off a dead or departing session. It does NOT clear
// gc.continuation_group: a group names a segment of one workflow ("these steps
// run in one session"), not the session that held them, so the group must
// outlive the released claim. The next session to claim
// any step of the segment then preassigns the rest through
// preassignHookContinuationGroup. Clearing it here left the restored segment
// unprotected and let concurrent cards' steps cross sessions.
//
// cmd/gc clears affinity by persisting an empty value rather than deleting the
// key (as internal/dispatch does) because these helpers feed
// beads.UpdateOpts.Metadata, whose merge only touches supplied keys. Every
// consumer treats the keys as absent when strings.TrimSpace is empty, so
// empty-value and deleted are equivalent.
//
// Re-routing paths that break the segment itself (graph re-decoration,
// retry-to-pool, reopen-source) clear the whole
// beadmeta.SessionAffinityMetadataKeys set instead.
func clearedReleaseAffinityMetadata() map[string]string {
	metadata := make(map[string]string, len(beadmeta.SessionAffinityMetadataKeys))
	for _, key := range beadmeta.SessionAffinityMetadataKeys {
		if key == beadmeta.ContinuationGroupMetadataKey {
			continue
		}
		metadata[key] = ""
	}
	return metadata
}

// clearSessionAffinityMetadataOnBead persists an empty value for every
// session-affinity key on beadID, continuation group included. It is for
// re-routing paths that end the segment itself (reopen-source re-slings the
// source to a new workflow), not for a release, which keeps the group; see
// clearedReleaseAffinityMetadata. See that function too for why cmd/gc clears
// by empty value rather than key deletion.
func clearSessionAffinityMetadataOnBead(store beads.Store, beadID string) error {
	for _, key := range beadmeta.SessionAffinityMetadataKeys {
		if err := store.SetMetadata(beadID, key, ""); err != nil {
			return err
		}
	}
	return nil
}

// clearSessionCurrentClaim clears the claim back-channel
// (beadmeta.CurrentClaimBeadIDMetadataKey) that `gc hook --claim` stamped onto
// sessionID's own bead.
//
// It is the session-side sibling of the affinity clearing above and exists for
// the same reason: a session that has had its work taken away must stop naming a
// bead it no longer owns. `gc hook current` is what a formula step uses to close
// the bead it is running, so a stale stamp is not merely untidy — the next step
// to read it would close somebody else's bead. Callers on the work-bead side of
// a release have no session identity in hand and cannot clear it; every path
// that releases work FROM A KNOWN SESSION calls this.
//
// Best-effort and silent: it runs inside close/retire cascades that are
// themselves best-effort, the failure mode is a stale stamp that the session's
// next claim overwrites anyway, and a closing session's bead may already be
// unreadable. Errors are returned for callers that want to report them.
func clearSessionCurrentClaim(store beads.Store, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if store == nil || sessionID == "" {
		return nil
	}
	_, err := sessionFrontDoor(store).SetCurrentClaim(sessionID, "")
	return err
}
