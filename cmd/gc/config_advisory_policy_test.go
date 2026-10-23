package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// advisoryWarning is a composition warning that survives
// shouldEmitLoadCityConfigWarning, so these cases exercise delivery rather
// than the content filter.
const advisoryWarning = `/city/pack.toml: [agents] is a deprecated compatibility alias for [agent_defaults]; rewrite the table name to [agent_defaults]`

func TestConfigAdvisoryTargetPolicy(t *testing.T) {
	var buf bytes.Buffer

	t.Run("nil writer stays nil", func(t *testing.T) {
		if got := configAdvisoryTarget(nil); got != nil {
			t.Fatalf("configAdvisoryTarget(nil) = %#v, want nil", got)
		}
	})

	t.Run("discard stays discard", func(t *testing.T) {
		if got := configAdvisoryTarget(io.Discard); got != io.Discard {
			t.Fatalf("configAdvisoryTarget(io.Discard) = %#v, want io.Discard", got)
		}
	})

	t.Run("non-terminal writer is dropped", func(t *testing.T) {
		withConfigAdvisoryTerminal(t, func(io.Writer) bool { return false })
		if got := configAdvisoryTarget(&buf); got != io.Discard {
			t.Fatalf("configAdvisoryTarget(non-terminal) = %#v, want io.Discard", got)
		}
	})

	t.Run("terminal writer is kept", func(t *testing.T) {
		withConfigAdvisoryTerminal(t, func(io.Writer) bool { return true })
		if got := configAdvisoryTarget(&buf); got != io.Writer(&buf) {
			t.Fatalf("configAdvisoryTarget(terminal) = %#v, want the writer itself", got)
		}
	})

	t.Run("reporting writer is kept without a terminal", func(t *testing.T) {
		withConfigAdvisoryTerminal(t, func(io.Writer) bool { return false })
		if got := configAdvisoryTarget(advisoryReportingWriter{&buf}); got != io.Writer(&buf) {
			t.Fatalf("configAdvisoryTarget(reporting) = %#v, want the wrapped writer", got)
		}
	})

	t.Run("reporting writer unwraps discard", func(t *testing.T) {
		withConfigAdvisoryTerminal(t, func(io.Writer) bool { return true })
		if got := configAdvisoryTarget(advisoryReportingWriter{io.Discard}); got != io.Discard {
			t.Fatalf("configAdvisoryTarget(reporting discard) = %#v, want io.Discard", got)
		}
	})
}

// TestEmitLoadCityConfigWarningsHonorsAdvisoryPolicy is the regression guard
// for the reason the policy exists: a command that loads the city config in a
// non-interactive session must not spend the caller's context on advisories.
func TestEmitLoadCityConfigWarningsHonorsAdvisoryPolicy(t *testing.T) {
	prov := &config.Provenance{Warnings: []string{advisoryWarning}}

	t.Run("non-interactive stderr gets nothing", func(t *testing.T) {
		withConfigAdvisoryTerminal(t, func(io.Writer) bool { return false })
		var stderr bytes.Buffer
		emitLoadCityConfigWarnings(&stderr, prov)
		if stderr.Len() != 0 {
			t.Fatalf("advisory reached a non-interactive writer: %q", stderr.String())
		}
	})

	t.Run("interactive stderr still gets it", func(t *testing.T) {
		withConfigAdvisoryTerminal(t, func(io.Writer) bool { return true })
		var stderr bytes.Buffer
		emitLoadCityConfigWarnings(&stderr, prov)
		if !strings.Contains(stderr.String(), advisoryWarning) {
			t.Fatalf("advisory missing from interactive stderr: %q", stderr.String())
		}
	})

	t.Run("reporting command still gets it", func(t *testing.T) {
		withConfigAdvisoryTerminal(t, func(io.Writer) bool { return false })
		var stderr bytes.Buffer
		emitLoadCityConfigWarnings(advisoryReportingWriter{&stderr}, prov)
		if !strings.Contains(stderr.String(), advisoryWarning) {
			t.Fatalf("advisory missing from a reporting command's stderr: %q", stderr.String())
		}
	})

	t.Run("discard is still discarded", func(t *testing.T) {
		withConfigAdvisoryTerminal(t, func(io.Writer) bool { return true })
		emitLoadCityConfigWarnings(io.Discard, prov)
	})
}

// TestConfigAdvisoryPolicyBypassesStayReviewed pins the set of commands that
// opt out of the policy. Wrapping a writer in advisoryReportingWriter makes a
// command print config advisories into every non-interactive caller's context,
// including an agent's, so a new bypass is a decision to review here rather
// than a detail of one command.
func TestConfigAdvisoryPolicyBypassesStayReviewed(t *testing.T) {
	allowed := map[string]string{
		"cmd_doctor.go": "reporting configuration problems is gc doctor's purpose",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read cmd/gc: %v", err)
	}
	found := map[string]struct{}{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Clean(name))
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if bytes.Contains(body, []byte("advisoryReportingWriter{")) {
			found[name] = struct{}{}
		}
	}

	for name := range found {
		if _, ok := allowed[name]; !ok {
			t.Fatalf("%s bypasses the config-advisory policy; if that is intended, add it to this test with the reason", name)
		}
	}
	for name, reason := range allowed {
		if _, ok := found[name]; !ok {
			t.Fatalf("%s no longer bypasses the config-advisory policy (%s); drop it from this test", name, reason)
		}
	}
}

// assumeInteractiveConfigAdvisories makes config-load advisories behave as if
// the case were running on a terminal. Tests that assert a loader or a command
// still produces an advisory use it so they keep testing that, not the
// delivery policy, which has its own cases above.
func assumeInteractiveConfigAdvisories(t *testing.T) {
	t.Helper()
	withConfigAdvisoryTerminal(t, func(io.Writer) bool { return true })
}

// withConfigAdvisoryTerminal swaps the terminal probe for one case and
// restores it afterwards.
func withConfigAdvisoryTerminal(t *testing.T, probe func(io.Writer) bool) {
	t.Helper()
	previous := configAdvisoryIsTerminal
	configAdvisoryIsTerminal = probe
	t.Cleanup(func() { configAdvisoryIsTerminal = previous })
}
