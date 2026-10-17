package main

import (
	"fmt"
	"github.com/jjyr/pot/pkg/sandbox"
	"github.com/jjyr/pot/pkg/sandboxprovider"
	local "github.com/jjyr/pot/plugins/internal/local"
	"os"
)

func main() {
	err := sandboxprovider.Serve(func(c sandboxprovider.Config) (sandbox.Provider, error) {
		return local.NewPodman(local.PodmanConfig{RuntimeRoot: c.Root, UserSkills: c.UserSkills, BuiltinSkills: c.BuiltinSkills}), nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
