package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/jjyr/pot/internal/access"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/instance"
	qrcode "github.com/skip2/go-qrcode"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
)

func accessCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("access", flag.ContinueOnError)
	flags.SetOutput(out)
	root := flags.String("root", "", "independent browser service root")
	browserRoot := flags.String("browser-root", "", "independent web-ui-server data root")
	origin := flags.String("origin", "", "public origin for an offline connection QR")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: pot access -browser-root PATH status|token|regenerate-token|qr|enable|disable")
	}
	if *browserRoot != "" {
		if *root != "" {
			return errors.New("choose one browser data root")
		}
		*root = *browserRoot
	}
	if *root == "" {
		var err error
		*root, err = instance.DefaultPath()
		if err != nil {
			return err
		}
	}
	var err error
	*root, err = filepath.Abs(*root)
	if err != nil {
		return err
	}
	action := flags.Arg(0)
	switch action {
	case "status", "token", "regenerate-token", "qr", "enable", "disable":
	default:
		return errors.New("unknown access command")
	}
	var value map[string]any
	endpoint, err := access.ReadOperator(*root)
	if err == nil {
		value, err = access.CallOperator(ctx, endpoint, strings.ReplaceAll(action, "-", "_"))
		if err != nil {
			var connection *net.OpError
			if !errors.As(err, &connection) {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if value == nil {
		store := webui.AccessStore{Path: filepath.Join(*root, "browser-access.json")}
		config, err := store.Load(false)
		if err != nil {
			return fmt.Errorf("browser service root required: %w", err)
		}
		switch action {
		case "status", "token", "qr":
		case "regenerate-token", "enable", "disable":
			config, err = store.Change(action)
			if err != nil {
				return err
			}
		}
		value = map[string]any{"enabled": !config.Disabled, "token": config.Token, "generation": config.Generation, "origin": *origin, "running": false}
	}
	if action == "qr" {
		address, _ := value["origin"].(string)
		token, _ := value["token"].(string)
		if address == "" || token == "" || value["enabled"] != true {
			return errors.New("enabled browser origin required; offline QR also requires -origin")
		}
		address = strings.TrimSuffix(address, "/") + "/#access_token=" + token
		qr, err := qrcode.New(address, qrcode.Medium)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Connection QR contains access credentials. Do not share.\n%s\n%s\n", address, qr.ToSmallString(false))
		return err
	}
	if action == "token" {
		_, err := fmt.Fprintln(out, value["token"])
		return err
	}
	delete(value, "token")
	delete(value, "connection_url")
	delete(value, "qr_data_url")
	return json.NewEncoder(out).Encode(value)
}
