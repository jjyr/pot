package main

import (
	"fmt"
	"github.com/jjyr/pot/pkg/providerplugin"
	"github.com/jjyr/pot/pkg/sandbox"
	local "github.com/jjyr/pot/providers/internal/local"
	"os"
	"path/filepath"
)

func main() {
	err := providerplugin.Serve(func(c providerplugin.Config) (sandbox.Provider, error) {
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		return local.NewAppleVM(local.AppleVMConfig{Helper: filepath.Join(filepath.Dir(executable), "PotMacOSHelper"), UserSkills: c.UserSkills, BuiltinSkills: c.BuiltinSkills}), nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
