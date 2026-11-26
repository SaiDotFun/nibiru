// Command eventkeys checks event construction in Nibiru-owned Go packages.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/NibiruChain/nibiru/v2/internal/analysis/eventkeys"
)

func main() {
	singlechecker.Main(eventkeys.Analyzer)
}
