// Command pot-tool-executor reserves the Go guest/Host entry point. Execution
// operations are added in M3; it must not advertise unsupported capabilities.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("pot-tool-executor Go migration skeleton")
		return
	}
	fmt.Fprintln(os.Stderr, "Go executor operations are not implemented; do not install this skeleton in a guest image")
	os.Exit(1)
}
