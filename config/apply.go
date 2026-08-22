// Package config loads and validates jack's YAML configuration, resolves the
// host environment (data, config, and registry paths), tracks cloned agents in
// the registry, and applies each agent's config into its workspace.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zoobzio/jack/domain"
)

// ApplyAgent copies the agent's config directory (agents/<name>/ under the user
// config dir) into that agent's workspace .claude directory
// (<data>/<name>/.claude). Claude Code's config inheritance then merges it with
// any .claude in the repo. The destination's contents are rebuilt from scratch
// so stale files from a previous apply do not linger — but the directory itself
// is kept: a running container bind-mounts it by inode, and replacing the
// directory would strand the mount on the stale copy.
func (e *Env) ApplyAgent(agent domain.Agent) error {
	src := filepath.Join(e.ConfigDir, "agents", string(agent))
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("agent directory not found: agents/%s/", agent)
	}
	dst := filepath.Join(e.DataDir, string(agent), ".claude")
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return fmt.Errorf("creating agent .claude dir: %w", err)
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return copyDir(src, dst)
}

// copyDir recursively copies the contents of src into dst. Symlinks are
// followed and their target content is copied.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		info, err := os.Stat(s) // Stat, not Lstat: follow symlinks and copy targets.
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := os.MkdirAll(d, 0o750); err != nil {
				return fmt.Errorf("creating directory %s: %w", d, err)
			}
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies a single file's contents from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(filepath.Clean(dst))
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}
