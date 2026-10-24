package main

import (
	"fmt"
	"github.com/jjyr/pot/internal/agenthost"
	"github.com/jjyr/pot/pkg/processscope"
	"os"
)

func main() {
	if processscope.HandleWorker() {
		return
	}
	if err := agenthost.MainRuntime(agenthost.Antigravity(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
