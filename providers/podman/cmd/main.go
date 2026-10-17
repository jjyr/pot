package main

import (
	"fmt"
	"github.com/jjyr/pot/pkg/providerplugin"
	"github.com/jjyr/pot/pkg/sandbox"
	local "github.com/jjyr/pot/providers/internal/local"
	"os"
)

func main() {
	err := providerplugin.Serve(func(c providerplugin.Config) (sandbox.Provider, error) {
		return local.NewPodman(local.PodmanConfig{RuntimeRoot: c.Root, UserSkills: c.UserSkills, BuiltinSkills: c.BuiltinSkills}), nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
