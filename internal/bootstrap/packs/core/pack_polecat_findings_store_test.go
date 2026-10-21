package core

import (
	"bufio"
	"bytes"
	"io/fs"
	"strings"
	"testing"
)

func TestPolecatFindingsUseCityStore(t *testing.T) {
	data, err := fs.ReadFile(PackFS, "formulas/mol-polecat-base.toml")
	if err != nil {
		t.Fatalf("reading formulas/mol-polecat-base.toml: %v", err)
	}

	matches := 0
	assigneeQueryFound := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := scanner.Text()
		if strings.Contains(line, "--assignee=$GC_AGENT") {
			assigneeQueryFound = true
			if strings.Contains(line, "--city") {
				t.Errorf("line %d: assignee query must stay in the rig store: %s", lineNumber, line)
			}
		}

		findingCommand := strings.Contains(line, "gc bd create") ||
			strings.Contains(line, "gc bd comment") ||
			(strings.Contains(line, "gc bd list") && strings.Contains(line, "--title-contains"))
		if !findingCommand {
			continue
		}
		matches++
		if !strings.Contains(line, `--city="$GC_CITY"`) {
			t.Errorf("line %d: finding command must use the city store: %s", lineNumber, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning formulas/mol-polecat-base.toml: %v", err)
	}
	if matches < 6 {
		t.Fatalf("matched %d finding commands, want at least 6", matches)
	}
	if !assigneeQueryFound {
		t.Fatal("did not find the assignee query")
	}
}
