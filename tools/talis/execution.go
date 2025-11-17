package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// runScriptOverSSH SSHes into each remote host in parallel and executes
// the specified remoteScript directly (without tmux). Useful for short-lived commands.
func runScriptOverSSH(
	instances []Instance,
	sshKeyPath string,
	remoteScript string,
	timeout time.Duration,
) error {
	log.Printf("🎬 Executing script on %d instance(s)\n", len(instances))
	log.Printf("📋 Script to execute: %s\n", remoteScript)
	log.Printf("🔑 Using SSH key: %s\n", sshKeyPath)
	log.Printf("⏱️  Timeout per instance: %v\n", timeout)

	var wg sync.WaitGroup
	errCh := make(chan error, len(instances))
	counter := atomic.Uint32{}

	for _, inst := range instances {
		wg.Add(1)
		go func(inst Instance) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			log.Printf("🚀 [%s] Connecting via SSH to %s...\n", inst.Name, inst.PublicIP)

			ssh := exec.CommandContext(ctx,
				"ssh",
				"-i", sshKeyPath,
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				fmt.Sprintf("root@%s", inst.PublicIP),
				remoteScript,
			)

			out, err := ssh.CombinedOutput()
			if err != nil {
				log.Printf("❌ [%s] SSH command failed\n", inst.Name)
				errCh <- fmt.Errorf("[%s:%s] ssh error in %s: %v\nOutput: %s",
					inst.Name, inst.PublicIP, inst.Region, err, string(out))
				return
			}

			if len(out) > 0 {
				log.Printf("📝 [%s] Output: %s\n", inst.Name, string(out))
			}

			log.Printf("✅ [%s] Command completed successfully – %d/%d\n",
				inst.Name, counter.Add(1), len(instances))
		}(inst)
	}

	wg.Wait()
	close(errCh)

	var errs []error //nolint:prealloc
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		sb := strings.Builder{}
		sb.WriteString("❌ errors running remote script:\n")
		for _, e := range errs {
			sb.WriteString("- ")
			sb.WriteString(e.Error())
			sb.WriteByte('\n')
		}
		return errors.New(sb.String())
	}
	return nil
}

// runScriptInTMux SSHes into each remote host in parallel, and launches
// the specified remoteScript inside a detached tmux session named sessionName.
// It uses the same timeout per host and returns a combined error if any fail.
func runScriptInTMux(
	instances []Instance,
	sshKeyPath string, // e.g. "~/.ssh/id_ed25519"
	remoteScript string, // e.g. "source /root/start.sh" or "celestia-appd start"
	sessionName string, // e.g. "app"
	timeout time.Duration,
) error {
	log.Printf("🎬 Starting tmux session '%s' on %d instance(s)\n", sessionName, len(instances))
	log.Printf("📋 Script to execute: %s\n", remoteScript)
	log.Printf("🔑 Using SSH key: %s\n", sshKeyPath)
	log.Printf("⏱️  Timeout per instance: %v\n", timeout)

	var wg sync.WaitGroup
	errCh := make(chan error, len(instances))
	counter := atomic.Uint32{}

	for _, inst := range instances {
		wg.Add(1)
		go func(inst Instance) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			// Create a log file path for this session
			logFile := fmt.Sprintf("/tmp/%s-%s.log", sessionName, inst.Name)

			// Launch in tmux with output redirected to a log file
			// This way we can inspect the output even if the session crashes
			wrappedScript := fmt.Sprintf("(%s) > %s 2>&1", remoteScript, logFile)
			tmuxCmd := fmt.Sprintf("tmux new-session -d -s %s '%s'", sessionName, wrappedScript)

			log.Printf("🚀 [%s] Connecting via SSH to %s...\n", inst.Name, inst.PublicIP)
			log.Printf("   Command: %s\n", remoteScript)
			log.Printf("   Log file: %s\n", logFile)

			ssh := exec.CommandContext(ctx,
				"ssh",
				"-i", sshKeyPath,
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				fmt.Sprintf("root@%s", inst.PublicIP),
				tmuxCmd,
			)

			out, err := ssh.CombinedOutput()
			if err != nil {
				log.Printf("❌ [%s] SSH command failed\n", inst.Name)
				errCh <- fmt.Errorf("[%s:%s] ssh error in %s: %v\nOutput: %s",
					inst.Name, inst.PublicIP, inst.Region, err, string(out))
				return
			}

			if len(out) > 0 {
				log.Printf("📝 [%s] SSH output: %s\n", inst.Name, string(out))
			}

			// Verify the tmux session was created and is still running
			time.Sleep(500 * time.Millisecond) // Give tmux a moment to start

			verifyCmd := exec.CommandContext(ctx,
				"ssh",
				"-i", sshKeyPath,
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				fmt.Sprintf("root@%s", inst.PublicIP),
				fmt.Sprintf("tmux has-session -t %s 2>&1 && tmux list-panes -t %s -F '#{pane_pid}' 2>&1", sessionName, sessionName),
			)

			verifyOut, verifyErr := verifyCmd.CombinedOutput()

			// Always check the log file to see what's happening
			logCmd := exec.CommandContext(ctx,
				"ssh",
				"-i", sshKeyPath,
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				fmt.Sprintf("root@%s", inst.PublicIP),
				fmt.Sprintf("tail -50 %s 2>&1 || echo 'Log file not found'", logFile),
			)
			logOut, _ := logCmd.CombinedOutput()

			if verifyErr != nil {
				log.Printf("❌ [%s] Tmux session '%s' is NOT running\n", inst.Name, sessionName)
				log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
				log.Printf("📄 Log output from %s:\n", logFile)
				log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
				log.Printf("%s\n", string(logOut))
				log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

				errCh <- fmt.Errorf("[%s:%s] tmux session '%s' failed to start or exited immediately.\nSee log output above",
					inst.Name, inst.PublicIP, sessionName)
				return
			}

			log.Printf("✅ Verified: %s session running on %s (PID: %s) – %d/%d\n",
				sessionName, inst.Name, strings.TrimSpace(string(verifyOut)), counter.Add(1), len(instances))

			// Show first few lines of the log to confirm it's working
			if len(logOut) > 0 {
				lines := strings.Split(strings.TrimSpace(string(logOut)), "\n")
				previewLines := lines
				if len(lines) > 3 {
					previewLines = lines[:3]
				}
				log.Printf("   Log preview: %s\n", strings.Join(previewLines, " | "))
			}
		}(inst)
	}

	wg.Wait()
	close(errCh)

	var errs []error //nolint:prealloc
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		sb := strings.Builder{}
		sb.WriteString("❌ errors running remote script:\n")
		for _, e := range errs {
			sb.WriteString("- ")
			sb.WriteString(e.Error())
			sb.WriteByte('\n')
		}
		return errors.New(sb.String())
	}
	return nil
}
