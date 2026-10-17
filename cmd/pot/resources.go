package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/serverconfig"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/sandbox"
	"io"
	"path/filepath"
)

func resourceCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "import" {
		return errors.New("usage: pot resources import -root PATH -resources RESOURCES -sandbox ID -bundle ID -directory EXPORT")
	}
	flags := flag.NewFlagSet("resources import", flag.ContinueOnError)
	flags.SetOutput(out)
	root := flags.String("root", "", "private Go Runtime root")
	resources := flags.String("resources", "", "installed product resource directory; defaults to saved server settings")
	sandboxID := flags.String("sandbox", sandbox.SharedID, "target Sandbox definition")
	bundleID := flags.String("bundle", "", "explicit bundle ID; omitted retains the Sandbox pin or provider default")
	directory := flags.String("directory", "", "offline verified resource export")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *directory == "" || flags.NArg() != 0 {
		return errors.New("explicit resource export directory required")
	}
	var err error
	if *root == "" {
		*root, err = instance.DefaultPath()
		if err != nil {
			return err
		}
	}
	*root, err = filepath.Abs(*root)
	if err != nil {
		return err
	}
	if *resources == "" {
		settings, err := serverconfig.Load(*root)
		if err != nil {
			return err
		}
		*resources = settings.Resources
	}
	r, err := product.Open(ctx, product.Options{Root: *root, ResourceDirectory: *resources, DisableMetadataRefresh: true})
	if err != nil {
		return err
	}
	defer r.Close()
	if err = r.ImportSandboxResources(ctx, *sandboxID, *bundleID, *directory); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Verified Sandbox resources imported and bound.")
	return err
}
