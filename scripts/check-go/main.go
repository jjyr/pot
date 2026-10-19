// Command check-go checks formatting and architectural import boundaries without
// adding desktop dependencies to headless builds.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check() error {
	if err := checkDatabaseBoundary("."); err != nil {
		return err
	}
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

// Keep schema evolution reviewable in one migration tree. Business packages may
// issue DML, but schema DDL and raw database opens must stay in the storage layer.
func checkDatabaseBoundary(root string) error {
	ddl := regexp.MustCompile(`(?i)\b(?:CREATE|ALTER|DROP)\s+(?:UNIQUE\s+)?(?:TABLE|INDEX|VIEW|TRIGGER)\b`)
	for _, directory := range []string{"cmd", "pkg", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			inStorage := strings.HasPrefix(filepath.ToSlash(relative), "pkg/storage/")
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			var violation error
			ast.Inspect(file, func(node ast.Node) bool {
				if violation != nil {
					return false
				}
				switch value := node.(type) {
				case *ast.BasicLit:
					if value.Kind == token.STRING {
						content, err := strconv.Unquote(value.Value)
						if err == nil && ddl.MatchString(content) {
							violation = fmt.Errorf("%s declares schema outside pkg/storage/migrations", path)
						}
					}
				case *ast.SelectorExpr:
					if !inStorage && value.Sel.Name == "OpenSQLite" {
						violation = fmt.Errorf("%s must use storage.OpenDatabase instead of OpenSQLite", path)
					}
				}
				return true
			})
			return violation
		})
		if err != nil {
			return err
		}
	}
	return nil
}
