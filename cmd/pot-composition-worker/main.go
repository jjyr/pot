// Command pot-composition-worker provides the isolated JavaScript composition
// worker used by composition.ProcessRunner in external Go applications.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jjyr/pot/pkg/composition"
)

func main() {
	if err := composition.ServeWorker(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
