package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if state.HTTPAddress != "127.0.0.1:7777" {
		t.Fatalf("expected daemon to bind to 127.0.0.1:7777, got %q", state.HTTPAddress)
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

func TestResolveWebDirUsesWorkspaceBuildOutput(t *testing.T) {
	workspaceRoot := t.TempDir()
	webDir := filepath.Join(workspaceRoot, "apps", "ptrack-web", "dist")
	if err := os.MkdirAll(filepath.Join(webDir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir web assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "assets", "index.js"), []byte("console.log('ptrack');"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	resolvedDir, info, err := resolveWebDir(workspaceRoot)
	if err != nil {
		t.Fatalf("resolve web dir: %v", err)
	}
	if resolvedDir != webDir {
		t.Fatalf("expected resolved dir %q, got %q", webDir, resolvedDir)
	}
	if !info.Enabled || info.Source != "workspace" {
		t.Fatalf("expected enabled workspace web info, got %+v", info)
	}
	if len(info.ViteFiles) != 1 || info.ViteFiles[0] != "assets/index.js" {
		t.Fatalf("expected vite files, got %+v", info.ViteFiles)
	}
}

func TestResolveWebDirReportsMissingWorkspaceBuild(t *testing.T) {
	workspaceRoot := t.TempDir()

	resolvedDir, info, err := resolveWebDir(workspaceRoot)
	if err != nil {
		t.Fatalf("resolve web dir: %v", err)
	}
	if resolvedDir != "" {
		t.Fatalf("expected no resolved dir, got %q", resolvedDir)
	}
	if info.Enabled {
		t.Fatalf("expected disabled web info, got %+v", info)
	}
	if !strings.Contains(info.Reason, "pnpm build") {
		t.Fatalf("expected build hint in reason, got %q", info.Reason)
	}
}
