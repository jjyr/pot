// Command check-go checks formatting and architectural import boundaries without
// adding desktop dependencies to headless builds.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check() error {
	format := exec.Command("gofmt", "-l", "cmd", "pkg", "internal", "plugins", "web", "scripts/check-go", "scripts/tests/testdata/go-public-api")
	output, err := format.Output()
	if err != nil {
		return err
	}
	if len(output) != 0 {
		return fmt.Errorf("run gofmt on:\n%s", output)
	}
	list := exec.Command("go", "list", "-mod=readonly", "-json", "./cmd/...", "./pkg/...", "./internal/...", "./resources/...", "./plugins/...", "./web")
	output, err = list.Output()
	if err != nil {
		return fmt.Errorf("list packages: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var pkg struct {
			ImportPath string
			Imports    []string
			Deps       []string
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		isDesktop := pkg.ImportPath == "github.com/jjyr/pot/internal/desktop" || strings.HasPrefix(pkg.ImportPath, "github.com/jjyr/pot/internal/desktop/") || pkg.ImportPath == "github.com/jjyr/pot/cmd/pot-app"
		if !isDesktop {
			for _, dep := range pkg.Deps {
				if strings.HasPrefix(dep, "github.com/wailsapp/") {
					return fmt.Errorf("headless package %s depends on %s", pkg.ImportPath, dep)
				}
			}
		}
		// Reusable components must be usable without the product implementation.
		// The complete-host facade is the sole explicit exception.
		if strings.HasPrefix(pkg.ImportPath, "github.com/jjyr/pot/pkg/") && pkg.ImportPath != "github.com/jjyr/pot/pkg/runtime" {
			for _, dep := range pkg.Deps {
				if strings.HasPrefix(dep, "github.com/jjyr/pot/internal/") {
					return fmt.Errorf("public component %s depends on product implementation %s", pkg.ImportPath, dep)
				}
			}
		}
		if !strings.HasPrefix(pkg.ImportPath, "github.com/jjyr/pot/plugins/") {
			for _, dep := range pkg.Deps {
				if strings.HasPrefix(dep, "github.com/jjyr/pot/plugins/") {
					return fmt.Errorf("core package %s links provider implementation %s", pkg.ImportPath, dep)
				}
			}
		}
		base := strings.TrimPrefix(pkg.ImportPath, "github.com/jjyr/pot/internal/")
		if base == "runner" || base == "desktopview" {
			for _, dep := range pkg.Deps {
				for _, forbidden := range []string{"control", "runtime", "transport", "gateway", "secrets", "desktop"} {
					prefix := "github.com/jjyr/pot/internal/" + forbidden
					if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
						return fmt.Errorf("component %s depends on %s", pkg.ImportPath, dep)
					}
				}
			}
		}
	}
	vet := exec.Command("go", "vet", "./...")
	vet.Stdout, vet.Stderr = os.Stdout, os.Stderr
	return vet.Run()
}
