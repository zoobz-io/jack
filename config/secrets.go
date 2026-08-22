package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zoobzio/jack/domain"
)

// SecretsPath returns the host path of the agent's secrets env file. It lives
// under the data dir — never under the (git-friendly, container-shared) config
// dir — so a token is visible only to the host user and the one container it
// belongs to.
func (e *Env) SecretsPath(agent domain.Agent) string {
	return filepath.Join(e.DataDir, "secrets", string(agent)+".env")
}

// AgentSecrets loads the agent's secrets file: KEY=VALUE lines destined for
// the agent's container environment (e.g. GH_TOKEN for the gh CLI). Blank
// lines and #-comments are skipped; values are taken verbatim after the first
// '=' — no quoting or expansion. A missing file simply yields no secrets, but
// a malformed line or a file readable by group/other is an error, so a typo
// or loose permissions cannot silently launch a misconfigured agent.
func (e *Env) AgentSecrets(agent domain.Agent) (map[string]string, error) {
	path := e.SecretsPath(agent)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by others (mode %o) — chmod 600 it", path, perm)
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}

	secrets := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE, got %q", path, i+1, line)
		}
		secrets[key] = strings.TrimSpace(value)
	}
	return secrets, nil
}

// SessionEnv returns the host path of the agent's rendered session env file,
// mounted into the container and sourced when claude launches. It is the
// jack-managed sibling of the user-edited secrets file: only jack writes it,
// always in place, so the container's bind mount of it never goes stale the
// way an editor's rename-on-save would leave it.
func (e *Env) SessionEnv(agent domain.Agent) string {
	return filepath.Join(e.DataDir, string(agent), "session.env")
}

// WriteSessionEnv renders the agent's secrets into its session env file as
// shell export lines, sourced by the container at claude launch. It rewrites
// the file in place (truncate + write, never rename) because a running
// container bind-mounts it by inode. An agent without secrets still gets the
// file — the mount source must exist, or docker manufactures a directory in
// its place. Values are single-quoted for the shell, so the sourced result
// matches AgentSecrets' verbatim reading of the user's secrets file.
func (e *Env) WriteSessionEnv(agent domain.Agent) error {
	secrets, err := e.AgentSecrets(agent)
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Rendered by jack from " + e.SecretsPath(agent) + " — do not edit.\n")
	b.WriteString("# Edit that file, then run `jack refresh` to re-render.\n")
	for _, k := range keys {
		b.WriteString("export " + k + "=" + shQuote(secrets[k]) + "\n")
	}

	path := e.SessionEnv(agent)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating agent dir: %w", err)
	}
	if werr := os.WriteFile(path, []byte(b.String()), 0o600); werr != nil {
		return fmt.Errorf("writing session env: %w", werr)
	}
	return nil
}

// shQuote single-quotes s for POSIX sh, closing and reopening the quote around
// embedded single quotes, so sourcing yields the value byte-for-byte.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
