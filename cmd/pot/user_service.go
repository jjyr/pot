package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/access"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const userServiceLabel = "com.jjyr.pot.go.runtime"

func openUserBrowser(ctx context.Context, root, owner string) error {
	endpoint, err := access.ReadOperator(root)
	if err != nil {
		return err
	}
	value, err := access.CallOperator(ctx, endpoint, "status")
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var state struct {
		Enabled bool   `json:"enabled"`
		Origin  string `json:"origin"`
		Token   string `json:"token"`
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return err
	}
	if !state.Enabled || state.Origin == "" || state.Token == "" {
		return errors.New("enable configured Web access before opening it")
	}
	program := "xdg-open"
	if runtime.GOOS == "darwin" {
		program = "open"
	}
	return exec.CommandContext(ctx, program, state.Origin+"/#access_token="+state.Token).Run()
}
func renderUserService(goos, binary, root string) (string, error) {
	for _, s := range []string{binary, root} {
		if strings.ContainsAny(s, "\x00\r\n") {
			return "", errors.New("invalid service path")
		}
	}
	marker := base64.RawURLEncoding.EncodeToString([]byte(root))
	if goos == "darwin" {
		escape := func(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- pot-root:%s -->
<plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>%s</string><string>serve</string><string>-root</string><string>%s</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>Umask</key><integer>63</integer><key>StandardOutPath</key><string>%s/server.log</string><key>StandardErrorPath</key><string>%s/server.log</string></dict></plist>
`, marker, userServiceLabel, escape(binary), escape(root), escape(root), escape(root)), nil
	}
	if goos == "linux" {
		quote := func(s string) string { return strconv.Quote(strings.ReplaceAll(s, "%", "%%")) }
		return fmt.Sprintf("# pot-root:%s\n[Unit]\nDescription=Pot Go Runtime\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nExecStart=%s serve -root %s\nEnvironmentFile=-%%h/.config/pot-go/runtime.env\nUMask=0077\nRestart=on-failure\nRestartSec=3\nTimeoutStopSec=45\nKillMode=mixed\n\n[Install]\nWantedBy=default.target\n", marker, quote(binary), quote(root)), nil
	}
	return "", errors.New("user services require macOS or Linux")
}
func userService(ctx context.Context, root, action string, out io.Writer) error {
	if action != "install" && action != "start" && action != "stop" && action != "uninstall" {
		return errors.New("usage: pot service install|start|stop|uninstall")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	directory := filepath.Join(home, ".config/systemd/user")
	name := "pot-go-runtime.service"
	if runtime.GOOS == "darwin" {
		directory = filepath.Join(home, "Library/LaunchAgents")
		name = userServiceLabel + ".plist"
	} else if runtime.GOOS != "linux" {
		return errors.New("user services require macOS or Linux")
	}
	path := filepath.Join(directory, name)
	run := func(program string, args ...string) error {
		command := exec.CommandContext(ctx, program, args...)
		command.Stdout = out
		command.Stderr = out
		return command.Run()
	}
	if action == "install" {
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		binary, err = filepath.EvalSymlinks(binary)
		if err != nil {
			return err
		}
		body, err := renderUserService(runtime.GOOS, binary, root)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("service definition already exists or cannot be created: %w", err)
		}
		if _, err = file.WriteString(body); err == nil {
			err = file.Sync()
		}
		err = errors.Join(err, file.Close())
		if err != nil {
			return err
		}
		if runtime.GOOS == "linux" {
			if err = run("systemctl", "--user", "daemon-reload"); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintln(out, "Service installed; run pot service start")
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("service definition must be a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(body), "pot-root:"+base64.RawURLEncoding.EncodeToString([]byte(root))) {
		return errors.New("installed user service belongs to another data root")
	}
	start, stop := func() error { return nil }, func() error { return nil }
	if runtime.GOOS == "darwin" {
		domain := "gui/" + strconv.Itoa(os.Getuid())
		start = func() error { return run("launchctl", "bootstrap", domain, path) }
		stop = func() error {
			probe := exec.CommandContext(ctx, "launchctl", "print", domain+"/"+userServiceLabel)
			if err := probe.Run(); err != nil {
				return nil
			}
			return run("launchctl", "bootout", domain, path)
		}
	} else {
		start = func() error { return run("systemctl", "--user", "enable", "--now", name) }
		stop = func() error { return run("systemctl", "--user", "disable", "--now", name) }
	}
	switch action {
	case "start":
		return start()
	case "stop":
		return stop()
	case "uninstall":
		if err = stop(); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
		if runtime.GOOS == "linux" {
			return run("systemctl", "--user", "daemon-reload")
		}
	}
	return nil
}
