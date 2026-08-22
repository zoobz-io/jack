package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyAgentCopiesRecursively(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	agentSrc := filepath.Join(configDir, "agents", "alex")
	if err := os.MkdirAll(filepath.Join(agentSrc, "commands"), 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentSrc, "CLAUDE.md"), []byte("top-level"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentSrc, "commands", "do.md"), []byte("nested"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	env := &Env{ConfigDir: configDir, DataDir: dataDir}

	if err := env.ApplyAgent("alex"); err != nil {
		t.Fatalf("ApplyAgent returned error: %v", err)
	}

	dstBase := filepath.Join(dataDir, "alex", ".claude")

	if got, err := os.ReadFile(filepath.Join(dstBase, "CLAUDE.md")); err != nil { //nolint:gosec // path is under t.TempDir()
		t.Errorf("reading copied CLAUDE.md: %v", err)
	} else if string(got) != "top-level" {
		t.Errorf("CLAUDE.md content = %q, want %q", got, "top-level")
	}

	if got, err := os.ReadFile(filepath.Join(dstBase, "commands", "do.md")); err != nil { //nolint:gosec // path is under t.TempDir()
		t.Errorf("reading nested copied file: %v", err)
	} else if string(got) != "nested" {
		t.Errorf("nested file content = %q, want %q", got, "nested")
	}
}

func TestApplyAgentReplacesStaleFiles(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	agentSrc := filepath.Join(configDir, "agents", "alex")
	if err := os.MkdirAll(agentSrc, 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentSrc, "CLAUDE.md"), []byte("fresh"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Pre-create a stale file in the destination.
	dstBase := filepath.Join(dataDir, "alex", ".claude")
	if err := os.MkdirAll(dstBase, 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	stale := filepath.Join(dstBase, "stale.md")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	env := &Env{ConfigDir: configDir, DataDir: dataDir}

	if err := env.ApplyAgent("alex"); err != nil {
		t.Fatalf("ApplyAgent returned error: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file still present (err=%v), want removed", err)
	}
	if got, err := os.ReadFile(filepath.Join(dstBase, "CLAUDE.md")); err != nil { //nolint:gosec // path is under t.TempDir()
		t.Errorf("reading copied CLAUDE.md: %v", err)
	} else if string(got) != "fresh" {
		t.Errorf("CLAUDE.md content = %q, want %q", got, "fresh")
	}
}

func TestApplyAgentKeepsDirInode(t *testing.T) {
	// A running container bind-mounts the workspace .claude directory by inode;
	// re-applying must rebuild its contents without replacing the directory, or
	// the mount is stranded on the stale copy.
	configDir := t.TempDir()
	dataDir := t.TempDir()

	agentSrc := filepath.Join(configDir, "agents", "alex")
	if err := os.MkdirAll(agentSrc, 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentSrc, "CLAUDE.md"), []byte("v1"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	env := &Env{ConfigDir: configDir, DataDir: dataDir}
	if err := env.ApplyAgent("alex"); err != nil {
		t.Fatalf("first ApplyAgent: %v", err)
	}

	dstBase := filepath.Join(dataDir, "alex", ".claude")
	before, err := os.Stat(dstBase)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(agentSrc, "CLAUDE.md"), []byte("v2"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := env.ApplyAgent("alex"); err != nil {
		t.Fatalf("second ApplyAgent: %v", err)
	}

	after, err := os.Stat(dstBase)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error(".claude directory was replaced (new inode), want contents rebuilt in place")
	}
	if got, err := os.ReadFile(filepath.Join(dstBase, "CLAUDE.md")); err != nil { //nolint:gosec // path is under t.TempDir()
		t.Errorf("reading re-applied CLAUDE.md: %v", err)
	} else if string(got) != "v2" {
		t.Errorf("CLAUDE.md content = %q, want %q", got, "v2")
	}
}

func TestApplyAgentMissingSourceDir(t *testing.T) {
	env := &Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()}

	err := env.ApplyAgent("ghost")
	if err == nil {
		t.Fatal("expected error for missing agent directory, got nil")
	}
}
