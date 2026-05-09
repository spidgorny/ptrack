package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"ptrack/internal/daemonctl"
	"ptrack/internal/model"
)

func TestTrackedInvocationReusesSingletonDaemon(t *testing.T) {
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	paths, err := daemonctl.ResolvePaths(packageDir)
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	defer cleanupDaemon(t, paths)
	cleanupDaemon(t, paths)

	binaryPath := filepath.Join(paths.WorkspaceRoot, "ptrack-test-bin")
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/ptrack")
	build.Dir = paths.WorkspaceRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}
	defer os.Remove(binaryPath)

	first := exec.Command(binaryPath, "sh", "-c", "printf 'daemon-first\\n'")
	first.Dir = paths.WorkspaceRoot
	var firstOutput bytes.Buffer
	first.Stdout = &firstOutput
	first.Stderr = &firstOutput
	if err := first.Run(); err != nil {
		t.Fatalf("run first tracked command: %v\n%s", err, firstOutput.String())
	}
	if firstOutput.String() == "" {
		t.Fatal("expected first command output")
	}

	state, err := waitForState(paths)
	if err != nil {
		t.Fatalf("wait for daemon state: %v", err)
	}
	if err := daemonctl.Ping(state, 2*time.Second); err != nil {
		t.Fatalf("ping daemon: %v", err)
	}

	second := exec.Command(binaryPath, "sh", "-c", "printf 'daemon-second\\n'")
	second.Dir = paths.WorkspaceRoot
	var secondOutput bytes.Buffer
	second.Stdout = &secondOutput
	second.Stderr = &secondOutput
	if err := second.Run(); err != nil {
		t.Fatalf("run second tracked command: %v\n%s", err, secondOutput.String())
	}

	stateAfter, err := waitForState(paths)
	if err != nil {
		t.Fatalf("refresh daemon state: %v", err)
	}
	if state.PID != stateAfter.PID {
		t.Fatalf("expected singleton daemon pid to remain stable, got %d then %d", state.PID, stateAfter.PID)
	}

	resp, err := http.Get(daemonctl.BaseURL(stateAfter) + "/api/v1/processes?limit=10")
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected list status: %d", resp.StatusCode)
	}
	var payload model.ProcessListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if payload.TrackedProcessCount < 2 {
		t.Fatalf("expected at least 2 tracked processes, got %d", payload.TrackedProcessCount)
	}
}

func waitForState(paths daemonctl.Paths) (daemonctl.State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		state, err := paths.ReadState()
		if err == nil {
			if err := daemonctl.Ping(state, time.Second); err == nil {
				return state, nil
			}
		}
		if ctx.Err() != nil {
			return daemonctl.State{}, ctx.Err()
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func cleanupDaemon(t *testing.T, paths daemonctl.Paths) {
	t.Helper()
	state, err := paths.ReadState()
	if err != nil {
		return
	}
	if state.PID > 0 {
		_ = syscall.Kill(state.PID, syscall.SIGTERM)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(state.PID, 0); err != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	_ = paths.RemoveState(state.PID)
}
