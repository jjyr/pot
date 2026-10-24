//go:build darwin || (linux && desktop)

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/internal/desktop"
	"github.com/jjyr/pot/internal/selfupdate"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/processscope"
)

func main() {
	// Dispatch before flag parsing or creating a Wails application. The installer
	// runs in its own process while the original App stops and execs its successor.
	if len(os.Args) > 1 && os.Args[1] == "_update-worker" {
		if len(os.Args) != 3 {
			log.Print("usage: pot-app _update-worker JOB")
			os.Exit(1)
		}
		if err := selfupdate.Worker(os.Args[2]); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.String())
		return
	}
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
	root := flag.String("root", "", "private data root")
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
