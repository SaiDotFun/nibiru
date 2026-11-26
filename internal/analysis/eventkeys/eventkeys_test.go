package eventkeys

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer,
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
	analysistest.Run(t, analysistest.TestData(), Analyzer, modulePath+"app/unresolved")
}
