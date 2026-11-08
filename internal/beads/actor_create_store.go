package beads

// ActorCreateStore is an optional store capability for creating ONE bead under
// an explicit actor instead of the store's own.
//
// A store's actor is fixed when it is opened — the native Dolt store carries it
// from open to close, and the bd-backed store inherits it from the process
// environment — because a store handle is process-scoped, not call-scoped. That
// is right for almost every write, but it is wrong for a caller that creates a
// bead on behalf of somebody else INSIDE its own process: the controller opens
// one store and, from it, writes the order-tracking bead that records a
// scheduled order's run. With no way to name an actor per create, that bead's
// created_by is the controller's, and every order run is attributed to the
// controller in bead views, audit logs and the dashboard. The alternatives are
// worse: mutating the process actor races every concurrent write, and opening a
// second handle per order pays a second database connection to relabel one row.
//
// The actor stamps BOTH the row's created_by and the actor the backend records
// on the write itself (the event log's actor), so the two never disagree. A
// blank or whitespace-only actor means "the store's own", which is why a caller
// can pass its configured actor through unconditionally.
//
// It is OPTIONAL, and callers must treat its absence as "this store cannot
// attribute the write to somebody else" rather than as an error: a store with no
// actor concept of its own (MemStore, FileStore) has nothing to override, and
// the write still has to happen. The stores that implement it are the ones whose
// created_by a bead view reads: NativeDoltStore, BdStore and the pass-through
// layers above them (CachingStore, ProxiedStore) — a layer that dropped it would
// silently fall back to the store actor on every create it forwards.
type ActorCreateStore interface {
	CreateWithActor(b Bead, actor string) (Bead, error)
}
