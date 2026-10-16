//go:build darwin

package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/jjyr/pot/internal/desktop"
	"github.com/jjyr/pot/pkg/composition"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == composition.WorkerArgument {
		if composition.ServeWorker(context.Background(), os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	root := flag.String("root", "", "private Go data root")
	preview := flag.Bool("preview", false, "disposable fixture UI")
	flag.Parse()
	var err error
	if *preview {
		err = desktop.RunPreview()
	} else {
		err = desktop.Run(*root)
	}
	if err != nil {
		log.Fatal(err)
	}
}
