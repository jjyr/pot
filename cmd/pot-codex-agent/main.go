package main

import (
	"fmt"
	"github.com/jjyr/pot/integrations/codex/agent"
	"github.com/jjyr/pot/pkg/processscope"
	"os"
)

func main() {
	if processscope.HandleWorker() {
		return
	}
	if err := agent.Main(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
