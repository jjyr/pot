package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	product "github.com/jjyr/pot/internal/runtime"
	"io"
)

func resourceCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "import" {
		return errors.New("usage: pot resources import -root PATH -directory EXPORT")
	}
	flags := flag.NewFlagSet("resources import", flag.ContinueOnError)
	flags.SetOutput(out)
	root := flags.String("root", "", "private Go Runtime root")
	directory := flags.String("directory", "", "offline verified resource export")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *directory == "" || flags.NArg() != 0 {
		return errors.New("explicit resource export directory required")
	}
	r, err := product.Open(ctx, product.Options{Root: *root, DisableMetadataRefresh: true})
	if err != nil {
		return err
	}
	defer r.Close()
	if err = r.ImportSandboxResources(ctx, *directory); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Verified Sandbox resources imported and bound.")
	return err
}
