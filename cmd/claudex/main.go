package main

import (
	"log"
	"os"

	"claudex/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		log.Fatalf("error: %v", err)
	}
}
