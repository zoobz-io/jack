package config

import (
	"fmt"
	"os"
	"path/filepath"
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
