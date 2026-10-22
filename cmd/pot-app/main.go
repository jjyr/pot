//go:build darwin || (linux && desktop)

package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/jjyr/pot/internal/desktop"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/processscope"
)

func main() {
	if processscope.HandleWorker() {
		return
	}
	if len(os.Args) == 2 && os.Args[1] == executor.PatchWorkerArgument {
		if executor.ServePatchWorker(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == composition.WorkerArgument {
		if composition.ServeWorker(context.Background(), os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	root := flag.String("root", "", "private Go data root")
	preview := flag.Bool("preview", false, "disposable fixture UI")
	smoke := flag.Bool("smoke-test", false, "test native UI with a fresh private root")
	flag.Parse()
	var err error
	if *smoke {
		err = desktop.RunSmoke(*root)
	} else if *preview {
		err = desktop.RunPreview()
	} else {
		err = desktop.Run(*root)
	}
	if err != nil {
		log.Fatal(err)
	}
}
