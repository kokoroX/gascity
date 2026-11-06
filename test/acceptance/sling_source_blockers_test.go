//go:build acceptance_a

package acceptance_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/builtinpacks"
	"github.com/gastownhall/gascity/internal/config"
	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

const sourceBlockerWorker = `#!/bin/bash
set -u
cd "$GC_CITY" || exit 1
GC="${GC_BIN:-gc}"
log="$GC_CITY/.gc/source-blocker-worker.log"
claims="$GC_CITY/.gc/source-blocker-claims"
echo started >> "$log"
while true; do
  out=$("$GC" hook --claim --json 2>>"$log")
  last=$(printf '%s\n' "$out" | tail -n 1)
  action=$(printf '%s\n' "$last" | jq -r '.action // empty' 2>>"$log")
  id=$(printf '%s\n' "$last" | jq -r '.bead_id // empty' 2>>"$log")
  if [ "$action" = work ] && [ -n "$id" ]; then
    printf '%s\n' "$id" >> "$claims"
    "$GC" bd update "$id" --set-metadata gc.outcome=pass --set-metadata gc.work_outcome=no-op --status closed >> "$log" 2>&1
  else
    sleep 1
  fi
done
`

func TestSlingSourceBlockers(t *testing.T) {
	env := testEnv.Clone().With("GC_BEADS", "bd").Without("GC_DOLT")
	c := helpers.NewCity(t, env)
	c.InitNoStart("claude")
	c.WriteConfig(`[workspace]

[session]
provider = "subprocess"

[daemon]
formula_v2 = true
patrol_interval = "200ms"
start_ready_timeout = "10m"
`)
	write := func(name, data string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(c.Dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("formulas/source-blocker.toml", `formula = "source-blocker"
version = 2
contract = "graph.v2"

[[steps]]
id = "first"
title = "First"

[[steps]]
id = "second"
title = "Second"
needs = ["first"]
`, 0o644)
	write("worker.sh", sourceBlockerWorker, 0o755)
	coreSource, ok := builtinpacks.Source("core")
	if !ok {
		t.Fatal("builtin core pack has no source")
	}
	write("pack.toml", fmt.Sprintf(`[pack]
name = "source-blocker-test"
schema = 2

[imports.core]
source = %q
version = %q

[[agent]]
name = "worker"
scope = "city"
max_active_sessions = 1
start_command = %q

[[named_session]]
template = "worker"
mode = "always"
`, coreSource, config.BundledSourcePinnedVersion(coreSource), "bash "+filepath.Join(c.Dir, "worker.sh")), 0o644)

	// A deferred diagnostic runs before the supervisor cleanup.
	defer func() {
		if !t.Failed() {
			return
		}
		for _, args := range [][]string{{"status"}, {"bd", "list", "--json", "--limit", "0"}} {
			out, err := c.GC(args...)
			t.Logf("gc %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		for _, name := range []string{"source-blocker-worker.log", "source-blocker-claims"} {
			data, err := os.ReadFile(filepath.Join(c.Dir, ".gc", name))
			t.Logf("%s: %v\n%s", name, err, data)
		}
	}()
	gc := func(args ...string) string {
		t.Helper()
		out, stderr, err := helpers.RunGCStreams(c.Env, c.Dir, args...)
		if err != nil {
			t.Fatalf("gc %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, stderr)
		}
		return out
	}
	create := func(title string) string {
		t.Helper()
		var bead struct {
			ID string `json:"id"`
		}
		lastJSONLine(t, gc("bd", "create", title, "--json"), &bead)
		if bead.ID == "" {
			t.Fatal("created bead has no ID")
		}
		return bead.ID
	}
	blocker, source := create("blocker"), create("blocked source")
	gc("bd", "dep", "add", source, blocker)
	c.StartWithSupervisor()
	var slung struct {
		WorkflowID string `json:"workflow_id"`
	}
	lastJSONLine(t, gc("sling", "worker", source, "--on", "source-blocker", "--json"), &slung)
	if slung.WorkflowID == "" {
		t.Fatal("sling returned no workflow ID")
	}

	var rows []struct {
		ID       string            `json:"id"`
		Metadata map[string]string `json:"metadata"`
	}
	out := gc("bd", "list", "--json", "--limit", "0")
	start := strings.Index(out, "[")
	if start < 0 || json.Unmarshal([]byte(out[start:]), &rows) != nil {
		t.Fatalf("invalid bd list: %s", out)
	}
	var steps []string
	first, second := "", ""
	for _, row := range rows {
		if row.Metadata["gc.root_bead_id"] != slung.WorkflowID || row.Metadata["gc.step_id"] == "" {
			continue
		}
		steps = append(steps, row.ID)
		switch row.Metadata["gc.step_id"] {
		case "source-blocker.first":
			first = row.ID
		case "source-blocker.second":
			second = row.ID
		}
	}
	if len(steps) < 2 || first == "" || second == "" {
		t.Fatalf("workflow %s: steps %v, first %q, second %q; list: %s", slung.WorkflowID, steps, first, second, out)
	}
	for _, id := range []string{first, second} {
		var shown []struct {
			Dependencies []struct {
				ID          string `json:"id"`
				DependsOnID string `json:"depends_on_id"`
				Type        string `json:"type"`
				DepType     string `json:"dependency_type"`
			} `json:"dependencies"`
		}
		out := gc("bd", "show", id, "--json")
		start := strings.Index(out, "[")
		if start < 0 || json.Unmarshal([]byte(out[start:]), &shown) != nil || len(shown) != 1 {
			t.Fatalf("invalid bd show %s: %s", id, out)
		}
		found := false
		for _, dep := range shown[0].Dependencies {
			if (dep.ID == blocker || dep.DependsOnID == blocker) && (dep.Type == "blocks" || dep.DepType == "blocks") {
				found = true
			}
		}
		if found != (id == first) {
			t.Fatalf("step %s blocks dependency on %s = %v: %s", id, blocker, found, out)
		}
	}
	claims := func() []string {
		data, err := os.ReadFile(filepath.Join(c.Dir, ".gc", "source-blocker-claims"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return strings.Fields(string(data))
	}
	// Observe the entire window, even when every poll finds no work.
	c.WaitForCondition(func() bool {
		out := gc("bd", "ready", "--json", "--limit", "0")
		var ready []struct {
			ID string `json:"id"`
		}
		start := strings.Index(out, "[")
		if start < 0 || json.Unmarshal([]byte(out[start:]), &ready) != nil {
			t.Fatalf("invalid bd ready: %s", out)
		}
		for _, row := range ready {
			if slices.Contains(steps, row.ID) {
				t.Fatalf("blocked step %s is ready", row.ID)
			}
		}
		for _, id := range claims() {
			if slices.Contains(steps, id) {
				t.Fatalf("blocked step %s was claimed", id)
			}
		}
		return false
	}, 15*time.Second)
	gc("bd", "close", blocker)
	if !c.WaitForCondition(func() bool { return slices.Contains(claims(), first) }, 60*time.Second) {
		t.Fatalf("worker did not claim first step %s after closing %s; claims %v", first, blocker, claims())
	}
}
