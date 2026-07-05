// Command generate emits the TypeScript client into client/src/api.
// Run from the repo root: go run ./tools/generate
package main

import (
	"fmt"
	"os"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/api"
)

func main() {
	registry, _, _, _ := api.NewRegistry(&api.Deps{})

	gen := aprot.NewGenerator(registry).WithOptions(aprot.GeneratorOptions{
		OutputDir: "client/src/api",
		Mode:      aprot.OutputReact,
		Zod:       true,
	})
	files, err := gen.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate failed: %v\n", err)
		os.Exit(1)
	}
	for name := range files {
		fmt.Printf("generated client/src/api/%s\n", name)
	}
}
