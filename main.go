package main

import (
	"log"
	"os"

	"github.com/photodialectic/claudex/internal/cli"
)

// Thin wrapper to preserve legacy package while new builds target cmd/claudex.
func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		log.Fatalf("error: %v", err)
	}
}
