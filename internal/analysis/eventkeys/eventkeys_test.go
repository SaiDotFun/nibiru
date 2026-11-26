package eventkeys

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../testdata")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, fixtureDir(t), Analyzer,
		modulePath+"app", modulePath+"x/wasm/keeper", modulePath+"lib/ibc-go/compat", bankPath)
}

func TestAudit(t *testing.T) {
	if err := Analyzer.Flags.Set("audit", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := Analyzer.Flags.Set("audit", "false"); err != nil {
			t.Error(err)
		}
	})
	analysistest.Run(t, fixtureDir(t), Analyzer, modulePath+"app/unresolved")
}
