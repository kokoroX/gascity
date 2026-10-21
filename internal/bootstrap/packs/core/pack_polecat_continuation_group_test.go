package core

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/formula"
)

func TestPolecatFormulaStepsShareContinuationGroup(t *testing.T) {
	formulaDir := t.TempDir()
	for _, name := range []string{"mol-polecat-base.toml", "mol-polecat-commit.toml", "mol-polecat-report.toml"} {
		data, err := fs.ReadFile(PackFS, "formulas/"+name)
		if err != nil {
			t.Fatalf("reading formulas/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(formulaDir, name), data, 0o600); err != nil {
			t.Fatalf("writing formulas/%s: %v", name, err)
		}
	}

	for _, name := range []string{"mol-polecat-base", "mol-polecat-commit", "mol-polecat-report"} {
		recipe, err := formula.Compile(context.Background(), name, []string{formulaDir}, nil)
		if err != nil {
			t.Fatalf("compile %s: %v", name, err)
		}
		if len(recipe.Steps) == 0 {
			t.Fatalf("compiled %s has no steps", name)
		}
		checkedSteps := 0
		checkedStepIDs := make(map[string]bool)
		for _, step := range recipe.Steps {
			if step.IsRoot || step.ID == name+".workflow-finalize" {
				continue
			}
			checkedSteps++
			checkedStepIDs[step.ID] = true
			if got := step.Metadata["gc.continuation_group"]; got != "polecat-work" {
				t.Errorf("%s step %q has gc.continuation_group %q, want %q", name, step.ID, got, "polecat-work")
			}
		}
		if checkedSteps == 0 {
			t.Fatalf("compiled %s has no continuation group steps", name)
		}
		if !checkedStepIDs[name+".load-context"] {
			t.Errorf("compiled %s has no checked load-context step", name)
		}
	}
}
