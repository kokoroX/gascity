package main

import (
	"io"
)

// Config-load advisories (composition warnings carried on config.Provenance)
// are diagnostics, not part of any command's output contract, so they reach
// stderr only for commands whose job is to report configuration state. Most
// commands load the city config, and in an agent session that stderr lands in
// the model's context next to stdout and is re-read by every later call in the
// turn — so an advisory printed by, say, `gc bd list` would become the largest
// single item an agent reads.
//
// A terminal is not evidence a human is reading: agent harnesses run commands
// on a pty. The decision therefore lives in one place:
// emitLoadCityConfigWarnings and warnMissingRequiredBuiltinImports route
// advisories through configAdvisoryTarget, which delivers only to a reporting
// command's writer. A command whose job IS to report configuration state opts
// in by wrapping its writer in advisoryReportingWriter.
//
// Nothing is lost where it is meant to be read: `gc config show` prints these
// warnings itself (and returns them in `.warnings` for --json), `gc start`
// prints them directly, and the supervisor records them through
// emitSupervisorLoadCityConfigWarnings.

// advisoryReportingWriter marks a writer belonging to a command whose purpose
// is to report configuration state, so config-load advisories reach it.
// Wrapping is the only opt-in: anything else gets the default policy in
// configAdvisoryTarget.
type advisoryReportingWriter struct {
	io.Writer
}

// configAdvisoryTarget resolves where a config-load advisory should be
// written. Advisories reach a reporting command's writer and nothing else;
// any other writer is dropped, terminal or not. A nil writer stays nil, so
// callers that already silenced advisories (--json, internal loads) keep that
// behavior.
func configAdvisoryTarget(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	if reporting, ok := w.(advisoryReportingWriter); ok {
		return reporting.Writer
	}
	return io.Discard
}

// configAdvisoriesSuppressed reports whether configAdvisoryTarget resolved to
// a writer that throws its input away, so an emitter can skip the work of
// formatting advisories nobody will read.
func configAdvisoriesSuppressed(w io.Writer) bool {
	return w == nil || w == io.Discard
}
