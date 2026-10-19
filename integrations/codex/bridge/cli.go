package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func readConfig(path string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || len(raw) > maxBody {
		return c, errors.New("invalid_bridge_config")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return c, errors.New("explicit_listen_address_required")
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return c, errors.New("listen_must_be_explicit_ip")
	}
	if !ip.IsLoopback() && (c.TLSCert == "" || c.TLSKey == "") {
		return c, errors.New("non_loopback_requires_tls")
	}
	if c.Database == "" {
		return c, errors.New("database_required")
	}
	if !filepath.IsAbs(c.Database) {
		c.Database = filepath.Join(filepath.Dir(path), c.Database)
	}
	for alias, path := range c.Workspaces {
		if !validID(alias) {
			return c, errors.New("invalid_workspace_alias")
		}
		p, e := workspacePath(path)
		if e != nil {
			return c, e
		}
		st, e := os.Stat(p)
		if e != nil || !st.IsDir() {
			return c, errors.New("workspace_requires_directory")
		}
		c.Workspaces[alias] = p
	}
	return c, nil
}
func secureJSON(path string, v any) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(v)
	return errors.Join(err, f.Close())
}

const CodexVersion = "codex-cli 0.163.0"

func checkCodex(binary string) error {
	if binary == "" {
		binary = "codex"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return errors.New("codex_not_installed_or_unavailable_check_path")
	}
	if strings.TrimSpace(string(raw)) != CodexVersion {
		return errors.New("codex_version_unsupported_expected_0.163.0")
	}
	return nil
}

func Main(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(out, "usage: pot-codex-bridge serve|pair|mcp|check --config PATH (Codex CLI 0.163.0)")
		return nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := flags.String("config", "", "configuration file")
	endpoint := flags.String("endpoint", "", "HTTPS Tailnet bridge URL")
	device := flags.String("device", "pot", "device label")
	control := flags.String("control", "", "Pot Control ID")
	assistant := flags.String("assistant", "main", "Pot assistant ID")
	aliases := flags.String("workspaces", "", "comma-separated workspace aliases")
	write := flags.Bool("allow-write", false, "allow workspace-write tasks")
	remoteFile := flags.String("remote-file", "", "new private pairing file for Pot")
	localFile := flags.String("local-file", "", "new private pairing file for local Codex MCP")
	if err := flags.Parse(args[1:]); err != nil {
		return errors.New("invalid_flags")
	}
	if len(flags.Args()) != 0 {
		return errors.New("unexpected_arguments")
	}
	if *config == "" {
		return errors.New("config_required")
	}
	if args[0] == "mcp" {
		return MCP(in, out, *config)
	}
	c, err := readConfig(*config)
	if err != nil {
		return err
	}
	if args[0] == "check" {
		if err = checkCodex(c.Codex); err != nil {
			return err
		}
		fmt.Fprintln(out, "configuration_and_codex_version_valid; verify codex login status locally")
		return nil
	}
	if args[0] == "pair" {
		if !validID(*control) || !validID(*assistant) || *remoteFile == "" || *localFile == "" || *remoteFile == *localFile || len(*device) > 128 {
			return errors.New("pairing_requires_control_assistant_and_distinct_new_files")
		}
		if !strings.HasPrefix(*endpoint, "https://") {
			return errors.New("https_endpoint_required")
		}
		workspaces := strings.Split(*aliases, ",")
		for _, alias := range workspaces {
			if c.Workspaces[alias] == "" {
				return errors.New("workspace_alias_not_configured")
			}
		}
		for _, p := range []string{*remoteFile, *localFile} {
			if _, e := os.Lstat(p); !errors.Is(e, os.ErrNotExist) {
				return errors.New("pairing_file_already_exists_or_inaccessible")
			}
		}
		store, e := Open(c.Database)
		if e != nil {
			return e
		}
		defer store.Close()
		remote, local, e := store.Pair(Pair{Device: *device, Control: *control, Assistant: *assistant, Workspaces: workspaces, Write: *write})
		if e != nil {
			return e
		}
		remote.Endpoint = *endpoint
		local.Endpoint = "http://" + c.Listen
		if c.TLSCert != "" {
			local.Endpoint = *endpoint
		}
		if e = secureJSON(*remoteFile, remote); e != nil {
			return e
		}
		if e = secureJSON(*localFile, local); e != nil {
			return e
		}
		fmt.Fprintln(out, "pairing_created; transfer remote-file privately to Pot; no task has run")
		return nil
	}
	if args[0] != "serve" {
		return errors.New("unknown_command")
	}
	if err = os.MkdirAll(filepath.Dir(c.Database), 0700); err != nil {
		return err
	}
	if err = checkCodex(c.Codex); err != nil {
		return err
	}
	lock, err := os.OpenFile(c.Database+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("bridge_already_running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	store, err := Open(c.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.Recover(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	worker, err := os.Executable()
	if err != nil {
		return err
	}
	server := NewServer(ctx, store, c, []string{worker})
	server.Start()
	defer server.Wait()
	httpServer := &http.Server{Addr: c.Listen, Handler: server, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	fmt.Fprintln(out, "bridge_listening "+c.Listen)
	if c.TLSCert != "" {
		err = httpServer.ListenAndServeTLS(c.TLSCert, c.TLSKey)
	} else {
		err = httpServer.ListenAndServe()
	}
	stop()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
