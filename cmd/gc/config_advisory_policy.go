package main

import (
	"io"
)

// Config-load advisories (composition warnings carried on config.Provenance)
// are diagnostics for whoever is reading the terminal, not part of any
// command's output contract. Every command that loads the city config used to
// decide for itself whether they reach stderr, by picking the writer it passed
// to loadCityConfig: configWarnWriter dropped them for --json at 11 call sites
// while 48 others passed stderr straight through. The result was incidental —
// `gc formula list --json` printed them and plain `gc session list` did not —
// and in an agent session, where stderr lands in the model's context next to
// stdout and is re-read by every later call in the turn, the repeated advisory
// became the largest single item an agent read.
//
// The decision belongs in one place instead: emitLoadCityConfigWarnings routes
// every advisory through configAdvisoryTarget, so no call site chooses any
// more. A command whose job IS to report configuration state opts in by
// wrapping its writer in advisoryReportingWriter.
//
// Nothing is lost where it is meant to be read: `gc config show` prints these
// warnings itself (and returns them in `.warnings` for --json), and the
// supervisor records them through emitSupervisorLoadCityConfigWarnings.

// advisoryReportingWriter marks a writer belonging to a command whose purpose
// is to report configuration state, so config-load advisories reach it even
// when it is not a terminal. Wrapping is the opt-in: anything else gets the
// default policy in configAdvisoryTarget.
type advisoryReportingWriter struct {
	io.Writer
}

// configAdvisoryIsTerminal reports whether a writer is an interactive
// terminal. It is a variable so tests can exercise the interactive branch
// without a pty.
var configAdvisoryIsTerminal = startOutputIsTerminal

// configAdvisoryTarget resolves where a config-load advisory should be
// written. Advisories reach a reporting command's writer always and any other
// writer only when it is an interactive terminal; otherwise they are dropped.
// A nil writer stays nil and io.Discard stays io.Discard, so callers that
// already silenced advisories (--json, internal loads) keep that behavior.
func configAdvisoryTarget(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	if reporting, ok := w.(advisoryReportingWriter); ok {
		return reporting.Writer
	}
	if w == io.Discard {
		return io.Discard
	}
	if configAdvisoryIsTerminal(w) {
		return w
	}
	return io.Discard
}

// configAdvisoriesSuppressed reports whether configAdvisoryTarget resolved to
// a writer that throws its input away, so an emitter can skip the work of
// formatting advisories nobody will read.
func configAdvisoriesSuppressed(w io.Writer) bool {
	return w == nil || w == io.Discard
}
