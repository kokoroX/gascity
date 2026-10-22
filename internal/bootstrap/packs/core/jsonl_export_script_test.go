package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// jsonlExportScriptPath is the on-disk copy of the jsonl-export order's
// script. These tests run it, so they need a real path.
const jsonlExportScriptPath = "assets/scripts/jsonl-export.sh"

// jsonlExportEnvKeys are the settings each test supplies itself. A live Gas
// City controller exports several of these, and an inherited copy would point
// the archive, the pack state or the outcome declaration at the host's city.
var jsonlExportEnvKeys = map[string]struct{}{
	"GC_CITY":                     {},
	"GC_CITY_PATH":                {},
	"GC_CITY_RUNTIME_DIR":         {},
	"GC_PACK_STATE_DIR":           {},
	"GC_SYSTEM_PACKS_DIR":         {},
	"GC_ESCALATE_SCRIPT":          {},
	"GC_JSONL_ARCHIVE_REPO":       {},
	"GC_JSONL_SUSPENDED_RIGS":     {},
	"GC_JSONL_SCRUB":              {},
	"GC_JSONL_SPIKE_THRESHOLD":    {},
	"GC_JSONL_MIN_PREV_FOR_SPIKE": {},
	"GC_ORDER_OUTCOME_FILE":       {},
	"GC_MAINTENANCE_DONE_TARGET":  {},
}

// jsonlExportFakeGC answers the four reads jsonl-export.sh makes of each
// scope: the rig list, the scope's database name, the export itself and the
// store's configuration. One rig is suspended ("jr"), one is not ("live").
const jsonlExportFakeGC = `rig=city
prev=
for a in "$@"; do
  if [ "$prev" = "--rig" ]; then rig=$a; fi
  prev=$a
done
case "$*" in
  *"rig list --json"*)
      printf '%s\n' '{"rigs":[{"name":"city","hq":true,"suspended":false},{"name":"live","hq":false,"suspended":false},{"name":"jr","hq":false,"suspended":true}]}'
      ;;
  *"sql --csv SELECT DATABASE()"*)
      printf 'DATABASE()\n%sdb\n' "$rig"
      ;;
  *"config list --json"*)
      printf '{"issue_prefix":"%s","types.custom":"molecule,convoy"}\n' "$rig"
      ;;
  *" export --all -o "*)
      out=
      prev=
      for a in "$@"; do
        if [ "$prev" = "-o" ]; then out=$a; fi
        prev=$a
      done
      printf '{"_type":"issue","id":"%s-1","status":"open","issue_type":"task","title":"t"}\n' "$rig" > "$out"
      ;;
  *"sql --csv SELECT COUNT"*)
      printf 'row_count\n1\n'
      ;;
esac
exit 0
`

// jsonlExportRun runs jsonl-export.sh against a throwaway city and archive.
// It returns the script output, the archive path and the outcome file path.
func jsonlExportRun(t *testing.T, extraEnv ...string) (out, archive, outcomeFile string) {
	t.Helper()
	binDir, _ := fakeGCBin(t, jsonlExportFakeGC)
	city := t.TempDir()
	archive = filepath.Join(t.TempDir(), "jsonl-archive")
	outcomeFile = filepath.Join(t.TempDir(), "outcome.json")
	env := append([]string{
		"GC_CITY_PATH=" + city,
		"GC_JSONL_ARCHIVE_REPO=" + archive,
		"GC_PACK_STATE_DIR=" + t.TempDir(),
		"GC_ORDER_OUTCOME_FILE=" + outcomeFile,
	}, extraEnv...)
	output, err := runPackScript(t, jsonlExportScriptPath, binDir, jsonlExportEnvKeys, env)
	if err != nil {
		t.Fatalf("jsonl-export.sh failed: %v\n%s", err, output)
	}
	return output, archive, outcomeFile
}

// jsonlExportOutcomeScopes reads the scope reasons the run declared, keyed by
// scope label. A run with nothing to declare writes no file at all, which
// reads here as no scopes.
func jsonlExportOutcomeScopes(t *testing.T, outcomeFile string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(outcomeFile)
	if os.IsNotExist(err) {
		return map[string]string{}
	}
	if err != nil {
		t.Fatalf("read order outcome: %v", err)
	}
	var decoded struct {
		Scopes []struct {
			Scope  string `json:"scope"`
			Reason string `json:"reason"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode order outcome %q: %v", raw, err)
	}
	scopes := make(map[string]string, len(decoded.Scopes))
	for _, scope := range decoded.Scopes {
		scopes[scope.Scope] = scope.Reason
	}
	return scopes
}

// TestJSONLExportDeclaresSuspendedRigSkipped pins the visibility half of the
// suspended-rig gap. core_rig_names leaves a suspended rig out on purpose — a
// bd read there restarts its retired proxy and Dolt — but the export used to
// drop it without a word, so the archive silently stopped covering that
// store exactly while the city was idle or stopped. The rig is still not
// exported by default; it now has to be declared as a skipped scope.
func TestJSONLExportDeclaresSuspendedRigSkipped(t *testing.T) {
	out, archive, outcomeFile := jsonlExportRun(t)

	scopes := jsonlExportOutcomeScopes(t, outcomeFile)
	reason, declared := scopes["rig:jr"]
	if !declared {
		t.Fatalf("suspended rig must be declared as a skipped scope, got %v\n%s", scopes, out)
	}
	if !strings.Contains(reason, "suspended") {
		t.Fatalf("skip reason must say the rig is suspended, got %q", reason)
	}
	if _, err := os.Stat(filepath.Join(archive, "jrdb", "issues.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("suspended rig must not be exported by default, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(archive, "livedb", "issues.jsonl")); err != nil {
		t.Fatalf("a running rig must still be exported: %v\n%s", err, out)
	}
}

// TestJSONLExportCoversNamedSuspendedRig pins the opt-in half: a rig named in
// GC_JSONL_SUSPENDED_RIGS is exported even while suspended. A store on an
// external server (JikeRental's, say) costs nothing to read when its rig is
// down, and it is the store whose backup matters most while the city is
// stopped.
func TestJSONLExportCoversNamedSuspendedRig(t *testing.T) {
	out, archive, outcomeFile := jsonlExportRun(t, "GC_JSONL_SUSPENDED_RIGS=jr")

	if _, err := os.Stat(filepath.Join(archive, "jrdb", "issues.jsonl")); err != nil {
		t.Fatalf("named suspended rig must be exported: %v\n%s", err, out)
	}
	if scopes := jsonlExportOutcomeScopes(t, outcomeFile); len(scopes) != 0 {
		t.Fatalf("an exported rig must not also be declared skipped, got %v", scopes)
	}
}

// TestJSONLExportCoversAllSuspendedRigs pins the "all" spelling of the opt-in.
func TestJSONLExportCoversAllSuspendedRigs(t *testing.T) {
	_, archive, _ := jsonlExportRun(t, "GC_JSONL_SUSPENDED_RIGS=all")

	if _, err := os.Stat(filepath.Join(archive, "jrdb", "issues.jsonl")); err != nil {
		t.Fatalf("GC_JSONL_SUSPENDED_RIGS=all must export a suspended rig: %v", err)
	}
}

// TestJSONLExportArchivesStoreConfig pins the restore gap: the export carries
// only issue and memory records, so a snapshot restored into a fresh store
// fails on the first bead of a custom type ("invalid issue type: convoy")
// until the store's own configuration is set. The archive therefore keeps
// each scope's `bd config list --json` beside its issues.
func TestJSONLExportArchivesStoreConfig(t *testing.T) {
	out, archive, _ := jsonlExportRun(t)

	raw, err := os.ReadFile(filepath.Join(archive, "livedb", "config.json"))
	if err != nil {
		t.Fatalf("archive must keep the scope's store config: %v\n%s", err, out)
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("archived store config %q is not a JSON object: %v", raw, err)
	}
	if config["types.custom"] != "molecule,convoy" {
		t.Fatalf("archived store config must carry types.custom, got %v", config)
	}
	if config["issue_prefix"] != "live" {
		t.Fatalf("archived store config must carry issue_prefix, got %v", config)
	}
}

// TestJSONLExportWritesRestoreRecipe pins that the archive explains how to
// restore itself. Whoever reaches for this archive has the archive and
// nothing else, so the config-before-import ordering has to be written down
// where they will be standing.
func TestJSONLExportWritesRestoreRecipe(t *testing.T) {
	out, archive, _ := jsonlExportRun(t)

	raw, err := os.ReadFile(filepath.Join(archive, "RESTORE.md"))
	if err != nil {
		t.Fatalf("archive must carry a restore recipe: %v\n%s", err, out)
	}
	for _, want := range []string{
		"config.json", "gc bd --rig <rig> config set", "gc bd --rig <rig> import", "issues.jsonl",
		// The count checks must count every restored record: bd list hides
		// gates and stops at 50 by default, and memories --json carries
		// schema_version beside the memories.
		"--include-gates", "-n 0", "del(.schema_version)",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("restore recipe must mention %q, got:\n%s", want, raw)
		}
	}
}

// TestJSONLExportReportsNoChanges pins that an unchanged run says so. The
// run that found the suspended-rig gap looked like a silent success: with
// every other scope unchanged the script exited 0 having printed nothing at
// all, so there was no way to tell a no-op apart from a narrowed export.
func TestJSONLExportReportsNoChanges(t *testing.T) {
	binDir, _ := fakeGCBin(t, jsonlExportFakeGC)
	archive := filepath.Join(t.TempDir(), "jsonl-archive")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "-q", "--bare", "-b", "main", origin)
	env := []string{
		"GC_CITY_PATH=" + t.TempDir(),
		"GC_JSONL_ARCHIVE_REPO=" + archive,
		"GC_PACK_STATE_DIR=" + t.TempDir(),
	}
	// First run creates the archive and its first snapshot.
	if out, err := runPackScript(t, jsonlExportScriptPath, binDir, jsonlExportEnvKeys, env); err != nil {
		t.Fatalf("first jsonl-export.sh run failed: %v\n%s", err, out)
	}
	// With an origin that holds every commit, the second run has neither a
	// staged change nor anything local-only — the one path that used to print
	// nothing at all.
	runGit(t, archive, "remote", "add", "origin", origin)
	if out, err := runPackScript(t, jsonlExportScriptPath, binDir, jsonlExportEnvKeys, env); err != nil {
		t.Fatalf("second jsonl-export.sh run failed: %v\n%s", err, out)
	}
	out, err := runPackScript(t, jsonlExportScriptPath, binDir, jsonlExportEnvKeys, env)
	if err != nil {
		t.Fatalf("third jsonl-export.sh run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no changes") {
		t.Fatalf("an unchanged run must report itself, got:\n%s", out)
	}
}

// runGit runs git in dir (the current directory when dir is empty) and fails
// the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
