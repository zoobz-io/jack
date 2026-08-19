package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zoobzio/jack/domain"
)

// claudeSeedKeys are the account and onboarding keys copied from the host's
// ~/.claude.json into a fresh agent claude.json. Everything else in the host
// file — prompt history, per-project state, feature caches — is host state the
// agents must not share.
var claudeSeedKeys = []string{"oauthAccount", "userID", "hasCompletedOnboarding", "lastOnboardingVersion"}

// ClaudeDir returns the host path of the agent's private Claude state
// directory, mounted into the container at ~/.claude. It is named "claude",
// not ".claude", because <data>/<agent>/.claude is the workspace soul copy
// (see ApplyAgent).
func (e *Env) ClaudeDir(agent domain.Agent) string {
	return filepath.Join(e.DataDir, string(agent), "claude")
}

// ClaudeJSON returns the host path of the agent's claude.json, mounted into
// the container at ~/.claude.json.
func (e *Env) ClaudeJSON(agent domain.Agent) string {
	return filepath.Join(e.DataDir, string(agent), "claude.json")
}

// EnsureClaudeState guarantees the agent's private Claude state exists,
// seeding it from the host login on first use. The seed is the minimum an
// agent needs to authenticate: the credentials file (hard-linked to the host's
// so token refreshes stay shared, copied when linking fails) and a claude.json
// holding only the claudeSeedKeys. Everything the agent accumulates afterwards
// — history, project state, settings — lives in its own directory, invisible
// to the host and to other agents. Existing state is left untouched, so a
// second call is a no-op and re-running never clobbers an agent's memory.
func (e *Env) EnsureClaudeState(agent domain.Agent) error {
	hostDir, hostJSON, err := hostClaudeState()
	if err != nil {
		return err
	}

	dir := e.ClaudeDir(agent)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating agent claude dir: %w", err)
	}

	// A host without a credentials file (e.g. macOS, where credentials live in
	// the keychain) has nothing to seed; the agent then logs in once inside
	// its container and that login persists in its own state dir.
	hostCreds := filepath.Join(hostDir, ".credentials.json")
	agentCreds := filepath.Join(dir, ".credentials.json")
	if _, err := os.Lstat(agentCreds); os.IsNotExist(err) {
		if _, herr := os.Stat(hostCreds); herr == nil {
			if lerr := linkOrCopy(hostCreds, agentCreds); lerr != nil {
				return fmt.Errorf("seeding agent credentials: %w", lerr)
			}
		}
	} else if err != nil {
		return err
	}

	agentJSON := e.ClaudeJSON(agent)
	if _, err := os.Stat(agentJSON); os.IsNotExist(err) {
		seed, serr := seedClaudeJSON(hostJSON)
		if serr != nil {
			return serr
		}
		if werr := os.WriteFile(agentJSON, seed, 0o600); werr != nil {
			return fmt.Errorf("writing agent claude.json: %w", werr)
		}
	} else if err != nil {
		return err
	}

	return nil
}

// ReseedClaudeCredentials replaces the agent's credentials file with a fresh
// link to the host's, for when a copied credential has drifted from the host
// login (e.g. after a token rotation). The rest of the agent's state — its
// memory — is kept.
func (e *Env) ReseedClaudeCredentials(agent domain.Agent) error {
	hostDir, _, err := hostClaudeState()
	if err != nil {
		return err
	}
	hostCreds := filepath.Join(hostDir, ".credentials.json")
	if _, err := os.Stat(hostCreds); err != nil {
		return fmt.Errorf("no host credentials to reseed from: %w", err)
	}

	if err := os.MkdirAll(e.ClaudeDir(agent), 0o700); err != nil {
		return fmt.Errorf("creating agent claude dir: %w", err)
	}
	agentCreds := filepath.Join(e.ClaudeDir(agent), ".credentials.json")
	if err := os.Remove(agentCreds); err != nil && !os.IsNotExist(err) {
		return err
	}
	return linkOrCopy(hostCreds, agentCreds)
}

// hostClaudeState locates the host's Claude login (~/.claude and
// ~/.claude.json) and verifies it is in place to seed from. A missing path
// means the user has never logged in on the host, which is an error directing
// them to do so first.
func hostClaudeState() (dir, file string, err error) {
	homedir, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolving home directory: %w", err)
	}
	dir = filepath.Join(homedir, ".claude")
	file = filepath.Join(homedir, ".claude.json")

	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		return "", "", fmt.Errorf("%s not found — run `claude` on the host once to log in before starting a session", dir)
	case err != nil:
		return "", "", fmt.Errorf("checking %s: %w", dir, err)
	case !info.IsDir():
		return "", "", fmt.Errorf("%s is not a directory", dir)
	}

	info, err = os.Stat(file)
	switch {
	case os.IsNotExist(err):
		return "", "", fmt.Errorf("%s not found — run `claude` on the host once to log in before starting a session", file)
	case err != nil:
		return "", "", fmt.Errorf("checking %s: %w", file, err)
	case info.IsDir():
		return "", "", fmt.Errorf("%s is a directory, not a file — an earlier bind mount likely created it; remove it and run `claude` on the host to log in again", file)
	}

	return dir, file, nil
}

// seedClaudeJSON extracts the claudeSeedKeys from the host's claude.json,
// returning a minimal document for a fresh agent. Keys absent on the host are
// omitted; claude re-derives whatever it needs.
func seedClaudeJSON(hostPath string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Clean(hostPath))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", hostPath, err)
	}
	var host map[string]json.RawMessage
	if uerr := json.Unmarshal(data, &host); uerr != nil {
		return nil, fmt.Errorf("parsing %s: %w", hostPath, uerr)
	}
	seed := make(map[string]json.RawMessage, len(claudeSeedKeys))
	for _, k := range claudeSeedKeys {
		if v, ok := host[k]; ok {
			seed[k] = v
		}
	}
	out, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rendering agent claude.json: %w", err)
	}
	return out, nil
}

// linkOrCopy hardlinks src to dst so in-place writes to either are shared,
// falling back to a private copy when linking fails (e.g. across filesystems).
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	// Credentials copied rather than linked must stay owner-only.
	return os.Chmod(dst, 0o600)
}
