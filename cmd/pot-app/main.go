//go:build darwin

package main

import (
	"context"
	"log"
	"os"

	"github.com/jjyr/pot/internal/composition"
	"github.com/jjyr/pot/internal/desktop"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == composition.WorkerArgument {
		if composition.ServeWorker(context.Background(), os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	if err := desktop.Run(); err != nil {
		log.Fatal(err)
	}
}
