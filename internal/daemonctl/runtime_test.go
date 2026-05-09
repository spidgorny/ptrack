package daemonctl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsPrefersWorkspaceRootOverNestedPackage(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module example.com/ptrack\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	appDir := filepath.Join(tempDir, "apps", "ptrack-web")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write nested package.json: %v", err)
	}

	paths, err := ResolvePaths(appDir)
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	if paths.WorkspaceRoot != tempDir {
		t.Fatalf("expected workspace root %q, got %q", tempDir, paths.WorkspaceRoot)
	}
}

func TestResolvePathsFallsBackToNearestPackageJSON(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	appDir := filepath.Join(tempDir, "apps", "ptrack-web")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	paths, err := ResolvePaths(filepath.Join(appDir, "src"))
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	if paths.WorkspaceRoot != appDir {
		t.Fatalf("expected package.json root %q, got %q", appDir, paths.WorkspaceRoot)
	}
}

func TestResolvePathsPrefersRepositoryRootOverNestedRuntimeDir(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "pnpm-workspace.yaml"), []byte("packages:\n  - apps/*\n"), 0o644); err != nil {
		t.Fatalf("write pnpm workspace: %v", err)
	}
	appDir := filepath.Join(tempDir, "apps", "ptrack-web")
	if err := os.MkdirAll(filepath.Join(appDir, ".ptrack"), 0o755); err != nil {
		t.Fatalf("mkdir nested runtime dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write nested package.json: %v", err)
	}

	paths, err := ResolvePaths(appDir)
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	if paths.WorkspaceRoot != tempDir {
		t.Fatalf("expected workspace root %q, got %q", tempDir, paths.WorkspaceRoot)
	}
}
