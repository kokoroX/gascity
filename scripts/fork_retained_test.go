package scripts_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/bazeltest"
)

// scripts/fork-retained.txt lists the regression tests and build-config
// literals the fork added on top of upstream. A merge of upstream can drop any
// of them while the fork's CI stays green — the test simply stops existing, or
// a BUILD pin silently reverts — so the list is checked against the tree.
//
// The BUILD-srcs half is checked through the analysis graph under Bazel: a
// package's BUILD.bazel is a package-private source file, unreadable from
// //scripts without an exports_files() line in that package, so //scripts's
// go_test_srcs_labels genquery (scripts/BUILD.bazel) is the evidence that a
// test file is still compiled by its go_test. Outside Bazel the same rule is
// applied to <pkg>/BUILD.bazel's text.

const (
	forkRetainedManifestPath   = "scripts/fork-retained.txt"
	forkRetainedGoTestSrcsEnv  = "GC_FORK_RETAINED_GO_TEST_SRCS"
	forkRetainedGoTestSrcsFile = "go_test_srcs_labels"
)

// forkRetainedEntry is one parsed manifest line.
type forkRetainedEntry struct {
	line int
	kind string // "test" or "contains"
	path string // package directory for test, repo-relative file for contains
	arg  string // TestName for test, literal for contains
}

var (
	forkRetainedTestLine     = regexp.MustCompile(`^test\s+(\S+)\s+(\S+)$`)
	forkRetainedContainsLine = regexp.MustCompile(`^contains\s+(\S+)\s+(.+)$`)
)

// parseForkRetainedManifest parses the manifest. Blank lines and lines whose
// first non-space character is '#' are comments. Every other line must be a
// known directive; an unknown one is an error rather than a silently ignored
// line, so a typo cannot retire a guard.
func parseForkRetainedManifest(text string) ([]forkRetainedEntry, error) {
	var entries []forkRetainedEntry
	var errs []string
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case forkRetainedTestLine.MatchString(line):
			match := forkRetainedTestLine.FindStringSubmatch(line)
			entries = append(entries, forkRetainedEntry{line: i + 1, kind: "test", path: match[1], arg: match[2]})
		case forkRetainedContainsLine.MatchString(line):
			match := forkRetainedContainsLine.FindStringSubmatch(line)
			entries = append(entries, forkRetainedEntry{line: i + 1, kind: "contains", path: match[1], arg: match[2]})
		default:
			errs = append(errs, fmt.Sprintf("%s:%d: unknown directive %q (want \"test <package dir> <TestName>\" or \"contains <file> <literal>\")", forkRetainedManifestPath, i+1, line))
		}
	}
	if len(errs) > 0 {
		return entries, errors.New(strings.Join(errs, "\n"))
	}
	return entries, nil
}

// forkRetainedGoTestSrcs reads the go_test srcs labels the Bazel analysis graph
// reports (one "//pkg:file.go" per line) and indexes them by "pkg/file.go".
// It returns nil outside Bazel, where the caller reads BUILD.bazel instead.
func forkRetainedGoTestSrcs(t *testing.T) map[string]bool {
	t.Helper()
	path := bazeltest.DataPath(t, forkRetainedGoTestSrcsEnv)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	srcs := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		pkg, file, ok := strings.Cut(strings.TrimPrefix(line, "//"), ":")
		if !ok || pkg == "" || file == "" {
			continue
		}
		srcs[pkg+"/"+file] = true
	}
	if len(srcs) == 0 {
		t.Fatalf("%s (%s=%s) lists no go_test srcs", filepath.Base(path), forkRetainedGoTestSrcsEnv, path)
	}
	return srcs
}

// findForkRetainedTestFile returns the name of the *_test.go file in dir that
// defines func name(. It follows symlinks, because under Bazel every runfile is
// one.
func findForkRetainedTestFile(dir, name string) (string, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read package dir: %w", err)
	}
	defines := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(`)
	var found []string
	for _, entry := range dirEntries {
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		// Follow runfiles symlinks: DirEntry.Info reports the link, not its
		// target, so stat the joined path instead.
		info, err := os.Stat(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		if defines.Match(content) {
			found = append(found, entry.Name())
		}
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no *_test.go in %s defines func %s(", dir, name)
	}
	return found[0], nil
}

// forkRetainedSrcsRegistered reports whether file is in a go_test srcs of the
// BUILD.bazel that owns pkgDir. Under Bazel the answer comes from the analysis
// graph; otherwise the BUILD text is matched by file name.
func forkRetainedSrcsRegistered(root, pkgDir, file string, goTestSrcs map[string]bool) (bool, string) {
	if goTestSrcs != nil {
		return goTestSrcs[pkgDir+"/"+file], "the Bazel analysis graph"
	}
	build, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pkgDir), "BUILD.bazel"))
	if err != nil {
		return false, fmt.Sprintf("read %s/BUILD.bazel: %v", pkgDir, err)
	}
	return strings.Contains(string(build), file), fmt.Sprintf("%s/BUILD.bazel", pkgDir)
}

// checkForkRetainedRepo checks every entry against the repository at root and
// returns one problem per regressed entry, so a single run reports all of them.
func checkForkRetainedRepo(root string, entries []forkRetainedEntry, goTestSrcs map[string]bool) []string {
	var problems []string
	for _, entry := range entries {
		where := fmt.Sprintf("%s:%d: %s %s %s", forkRetainedManifestPath, entry.line, entry.kind, entry.path, entry.arg)
		switch entry.kind {
		case "test":
			dir := filepath.Join(root, filepath.FromSlash(entry.path))
			file, err := findForkRetainedTestFile(dir, entry.arg)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v (dropped when merging upstream?)", where, err))
				continue
			}
			registered, evidence := forkRetainedSrcsRegistered(root, entry.path, file, goTestSrcs)
			if !registered {
				problems = append(problems, fmt.Sprintf("%s: %s is not in a go_test srcs of %s (per %s); under Bazel add //%s:<leaf>_test to //scripts:go_test_srcs_labels (dropped when merging upstream?)", where, file, evidence, evidence, entry.path))
			}
		case "contains":
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.path)))
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v (dropped when merging upstream?)", where, err))
				continue
			}
			if !strings.Contains(string(content), entry.arg) {
				problems = append(problems, fmt.Sprintf("%s: %s no longer contains %q (dropped when merging upstream?)", where, entry.path, entry.arg))
			}
		}
	}
	return problems
}

// runForkRetainedGuard is the whole guard: parse the manifest at root, refuse an
// empty one, and check every entry.
func runForkRetainedGuard(root string, goTestSrcs map[string]bool) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(forkRetainedManifestPath)))
	if err != nil {
		return fmt.Errorf("read %s: %w", forkRetainedManifestPath, err)
	}
	entries, err := parseForkRetainedManifest(string(data))
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("%s lists no entries; an empty manifest guards nothing", forkRetainedManifestPath)
	}
	problems := checkForkRetainedRepo(root, entries, goTestSrcs)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d fork-retained entries regressed — restore them from the fork (or from the source commit named in the manifest comment); do not delete manifest entries:\n%s",
		len(problems), len(entries), strings.Join(problems, "\n"))
}

// TestForkRetainedRegressionCoverage checks the checked-in manifest against the
// repository.
func TestForkRetainedRegressionCoverage(t *testing.T) {
	root := repoRoot(t)
	if err := runForkRetainedGuard(root, forkRetainedGoTestSrcs(t)); err != nil {
		t.Fatal(err)
	}
}

// TestParseForkRetainedManifest pins the manifest grammar.
func TestParseForkRetainedManifest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		want    []forkRetainedEntry
		wantErr string
	}{
		{
			name: "test_and_contains",
			text: "# comment\n\ntest internal/sling TestSlingWaits\ncontains cmd/gc/BUILD.bazel \"//conditions:default\": [],\n",
			want: []forkRetainedEntry{
				{line: 3, kind: "test", path: "internal/sling", arg: "TestSlingWaits"},
				{line: 4, kind: "contains", path: "cmd/gc/BUILD.bazel", arg: `"//conditions:default": [],`},
			},
		},
		{
			name: "contains_literal_keeps_inner_spaces_to_end_of_line",
			text: "contains a/BUILD.bazel data = [\"@dolt_bin//:dolt\"],\n",
			want: []forkRetainedEntry{
				{line: 1, kind: "contains", path: "a/BUILD.bazel", arg: `data = ["@dolt_bin//:dolt"],`},
			},
		},
		{
			name: "empty_manifest_parses_to_no_entries",
			text: "# only a comment\n\n",
		},
		{
			name:    "unknown_directive",
			text:    "test internal/sling TestA\nrestore internal/sling TestB\n",
			wantErr: `fork-retained.txt:2: unknown directive "restore internal/sling TestB"`,
		},
		{
			name:    "test_without_name",
			text:    "test internal/sling\n",
			wantErr: `unknown directive "test internal/sling"`,
		},
		{
			name:    "contains_without_literal",
			text:    "contains cmd/gc/BUILD.bazel\n",
			wantErr: `unknown directive "contains cmd/gc/BUILD.bazel"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseForkRetainedManifest(tc.text)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to name %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parsed %d entries, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// writeForkRetainedFixture stages a repository root holding files plus the
// guard's manifest.
func writeForkRetainedFixture(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, filepath.FromSlash(forkRetainedManifestPath)), manifest)
	for rel, content := range files {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root
}

// TestRunForkRetainedGuard is the bite: each case is a way a fork-retained test
// or build pin disappears, and each must fail naming the manifest line.
func TestRunForkRetainedGuard(t *testing.T) {
	const buildWithTest = `go_test(
    name = "pkg_test",
    srcs = ["retained_test.go"],
)
`
	const definingTest = `package pkg

func TestRetained(t *testing.T) {}
`
	for _, tc := range []struct {
		name     string
		manifest string
		files    map[string]string
		srcs     map[string]bool
		wantErr  string
	}{
		{
			name:     "conforming_tree",
			manifest: "test internal/pkg TestRetained\ncontains internal/pkg/BUILD.bazel retained_test.go\n",
			files: map[string]string{
				"internal/pkg/retained_test.go": definingTest,
				"internal/pkg/BUILD.bazel":      buildWithTest,
			},
		},
		{
			name:     "genquery_srcs_are_evidence_under_bazel",
			manifest: "test internal/pkg TestRetained\n",
			files: map[string]string{
				"internal/pkg/retained_test.go": definingTest,
				"internal/pkg/BUILD.bazel":      "go_test(name = \"pkg_test\")\n",
			},
			srcs: map[string]bool{"internal/pkg/retained_test.go": true},
		},
		{
			name:     "genquery_srcs_missing_the_file",
			manifest: "test internal/pkg TestRetained\n",
			files: map[string]string{
				"internal/pkg/retained_test.go": definingTest,
				"internal/pkg/BUILD.bazel":      buildWithTest,
			},
			srcs:    map[string]bool{"internal/pkg/other_test.go": true},
			wantErr: "is not in a go_test srcs",
		},
		{
			name:     "test_function_missing",
			manifest: "test internal/pkg TestRetained\n",
			files: map[string]string{
				"internal/pkg/retained_test.go": "package pkg\n\nfunc TestSomethingElse(t *testing.T) {}\n",
				"internal/pkg/BUILD.bazel":      buildWithTest,
			},
			wantErr: "no *_test.go in ",
		},
		{
			name:     "test_file_missing",
			manifest: "test internal/pkg TestRetained\n",
			files: map[string]string{
				"internal/pkg/BUILD.bazel": buildWithTest,
			},
			wantErr: "no *_test.go in ",
		},
		{
			name:     "test_package_dir_missing",
			manifest: "test internal/pkg TestRetained\n",
			files:    map[string]string{},
			wantErr:  "read package dir",
		},
		{
			name:     "file_not_in_go_test_srcs",
			manifest: "test internal/pkg TestRetained\n",
			files: map[string]string{
				"internal/pkg/retained_test.go": definingTest,
				"internal/pkg/BUILD.bazel":      "go_test(name = \"pkg_test\", srcs = [\"other_test.go\"])\n",
			},
			wantErr: "is not in a go_test srcs",
		},
		{
			name:     "contains_literal_missing",
			manifest: "contains cmd/gc/BUILD.bazel GC_TEST_TOOL_PATHS\n",
			files: map[string]string{
				"cmd/gc/BUILD.bazel": "go_test(name = \"gc_test\")\n",
			},
			wantErr: "no longer contains",
		},
		{
			name:     "contains_file_missing",
			manifest: "contains cmd/gc/BUILD.bazel GC_TEST_TOOL_PATHS\n",
			files:    map[string]string{},
			wantErr:  "no such file or directory",
		},
		{
			name:     "empty_manifest_guards_nothing",
			manifest: "# nothing here\n",
			files:    map[string]string{},
			wantErr:  "lists no entries",
		},
		{
			name:     "unknown_directive_fails",
			manifest: "keep internal/pkg TestRetained\n",
			files:    map[string]string{},
			wantErr:  "unknown directive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeForkRetainedFixture(t, tc.manifest, tc.files)
			err := runForkRetainedGuard(root, tc.srcs)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("conforming tree failed the guard: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("regressed tree passed the guard (want %q)", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error does not name %q:\n%v", tc.wantErr, err)
			}
			if tc.name != "empty_manifest_guards_nothing" && tc.name != "unknown_directive_fails" &&
				!strings.Contains(err.Error(), forkRetainedManifestPath+":1:") {
				t.Errorf("error does not name the manifest line (want %s:1:):\n%v", forkRetainedManifestPath, err)
			}
		})
	}
}

// TestCheckForkRetainedReportsEveryRegression pins the all-at-once report: one
// merge can drop several entries and each must be named in a single failure.
func TestCheckForkRetainedReportsEveryRegression(t *testing.T) {
	root := writeForkRetainedFixture(t,
		"test internal/a TestA\ntest internal/b TestB\ncontains c/BUILD.bazel pin\n",
		map[string]string{
			"internal/a/a_test.go": "package a\n\nfunc TestA(t *testing.T) {}\n",
			"internal/a/BUILD.bazel": `go_test(
    name = "a_test",
    srcs = ["a_test.go"],
)
`,
			"internal/b/b.go": "package b\n",
		})
	err := runForkRetainedGuard(root, nil)
	if err == nil {
		t.Fatal("two dropped entries passed the guard")
	}
	for _, want := range []string{forkRetainedManifestPath + ":2:", forkRetainedManifestPath + ":3:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("report does not name %s:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), forkRetainedManifestPath+":1:") {
		t.Errorf("report names the conforming entry too:\n%v", err)
	}
}
