// pot-sandbox-check qualifies the image's actual SRT launcher without a model.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/sandbox"
)

func check() int {
	root, err := os.MkdirTemp("", "pot-container-srt-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(root)
	work := filepath.Join(root, "work")
	secret := filepath.Join(root, "private")
	if err = os.Mkdir(work, 0700); err == nil {
		err = os.WriteFile(secret, []byte("synthetic-private-state"), 0600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	socket := filepath.Join(work, "authority.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer listener.Close()
	connection, err := net.Dial("unix", socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	// The socket is real and visible inside the writable workspace. Only a
	// permission denial proves the packaged seccomp boundary is working.
	code := `const fs=require('node:fs'),net=require('node:net');
const secret=process.argv[1],socket=process.argv[2];
try{fs.readFileSync(secret);throw Error('private state exposed')}catch(e){if(!['ENOENT','EPERM','EACCES'].includes(e.code))throw e}
fs.writeFileSync('proof','confined');
if(!fs.statSync(socket).isSocket())throw Error('socket control hidden');
const s=net.connect(socket);s.on('connect',()=>{console.error('Unix authority exposed');process.exit(1)});
s.on('error',e=>{if(!['EPERM','EACCES'].includes(e.code))throw e;console.log('Protected file/write/socket checks passed')});`
	node := "/opt/pot/Resources/node/bin/node"
	command := exec.CommandContext(ctx, node, "-e", code, secret, socket)
	command.Dir = work
	command.Env = sandbox.Environment()
	finish, err := (sandbox.Launcher{
		Node: node, Directory: "/opt/pot/Resources/sandbox", ProtectedPaths: []string{secret},
	}).Prepare(ctx, command, work, nil, nil, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	output, runErr := command.CombinedOutput()
	cleanupErr := finish()
	fmt.Print(string(output))
	if runErr != nil {
		// Distinguish a known kernel/container namespace refusal from missing
		// assets, invalid policy or a failed isolation assertion. No host retry.
		message := string(output)
		if cleanupErr == nil && strings.Contains(message, "bwrap:") &&
			((strings.Contains(message, "Creating new namespace failed") && strings.Contains(message, "Operation not permitted")) ||
				strings.Contains(message, "No permissions to create new namespace")) {
			if _, err := os.Stat(filepath.Join(work, "proof")); os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "Protected execution unavailable: container/host denies namespaces; no workload ran")
				return 2
			}
		}
		fmt.Fprintln(os.Stderr, runErr, cleanupErr)
		return 1
	}
	if cleanupErr != nil {
		fmt.Fprintln(os.Stderr, cleanupErr)
		return 1
	}
	proof, err := os.ReadFile(filepath.Join(work, "proof"))
	if err != nil || string(proof) != "confined" {
		fmt.Fprintln(os.Stderr, "workspace write not preserved", err)
		return 1
	}
	json.NewEncoder(os.Stdout).Encode(map[string]bool{"protected": true})
	return 0
}

func main() { os.Exit(check()) }
