package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// hostLogin points HOME at a temp dir seeded with a host Claude login:
// ~/.claude/.credentials.json plus a ~/.claude.json mixing account keys with
// host-only state. It returns the home path.
func hostLogin(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	creds := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.WriteFile(creds, []byte(`{"token":"host-secret"}`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	hostJSON := `{
		"oauthAccount": {"email": "op@example.com"},
		"userID": "u-123",
		"hasCompletedOnboarding": true,
		"numStartups": 1580,
		"projects": {"/home/op/secret": {"history": ["do not leak"]}}
	}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(hostJSON), 0o600); err != nil {
		t.Fatalf("write .claude.json: %v", err)
	}
	return home
}

func TestEnsureClaudeStateSeeds(t *testing.T) {
	home := hostLogin(t)
	env := &Env{DataDir: t.TempDir()}

	if err := env.EnsureClaudeState("case"); err != nil {
		t.Fatalf("EnsureClaudeState: %v", err)
	}

	// Credentials are hard-linked to the host's (same inode both ways).
	agentCreds := filepath.Join(env.ClaudeDir("case"), ".credentials.json")
	ai, err := os.Stat(agentCreds)
	if err != nil {
		t.Fatalf("agent credentials missing: %v", err)
	}
	hi, err := os.Stat(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(ai, hi) {
		t.Error("agent credentials are not hard-linked to the host's")
	}

	// claude.json carries the account keys and nothing else.
	data, err := os.ReadFile(env.ClaudeJSON("case"))
	if err != nil {
		t.Fatalf("agent claude.json missing: %v", err)
	}
	var seeded map[string]any
	if err := json.Unmarshal(data, &seeded); err != nil {
		t.Fatalf("agent claude.json is not valid JSON: %v", err)
	}
	if seeded["userID"] != "u-123" {
		t.Errorf("userID = %v, want u-123", seeded["userID"])
	}
	if seeded["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding = %v, want true", seeded["hasCompletedOnboarding"])
	}
	if _, ok := seeded["oauthAccount"]; !ok {
		t.Error("oauthAccount missing from seeded claude.json")
	}
	for _, k := range []string{"projects", "numStartups"} {
		if _, ok := seeded[k]; ok {
			t.Errorf("host-only key %q leaked into agent claude.json", k)
		}
	}
}

func TestEnsureClaudeStateIsIdempotent(t *testing.T) {
	hostLogin(t)
	env := &Env{DataDir: t.TempDir()}

	if err := env.EnsureClaudeState("case"); err != nil {
		t.Fatalf("EnsureClaudeState: %v", err)
	}

	// The agent accumulates state; a second call must not clobber it.
	if err := os.WriteFile(env.ClaudeJSON("case"), []byte(`{"agent":"memory"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := env.EnsureClaudeState("case"); err != nil {
		t.Fatalf("second EnsureClaudeState: %v", err)
	}
	data, err := os.ReadFile(env.ClaudeJSON("case"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"agent":"memory"}` {
		t.Errorf("agent claude.json clobbered on re-seed: %s", data)
	}
}

func TestEnsureClaudeStateWithoutHostCredentials(t *testing.T) {
	// A host that keeps credentials elsewhere (e.g. the macOS keychain) still
	// seeds claude.json; the agent logs in inside its container instead.
	home := hostLogin(t)
	if err := os.Remove(filepath.Join(home, ".claude", ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	env := &Env{DataDir: t.TempDir()}

	if err := env.EnsureClaudeState("case"); err != nil {
		t.Fatalf("EnsureClaudeState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.ClaudeDir("case"), ".credentials.json")); !os.IsNotExist(err) {
		t.Errorf("credentials seeded from nothing (stat err %v), want absent", err)
	}
	if _, err := os.Stat(env.ClaudeJSON("case")); err != nil {
		t.Errorf("claude.json not seeded: %v", err)
	}
}

func TestEnsureClaudeStateMissingHostLogin(t *testing.T) {
	// No ~/.claude at all: the user has never logged in on the host.
	t.Run("no claude dir", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		env := &Env{DataDir: t.TempDir()}
		if err := env.EnsureClaudeState("case"); err == nil {
			t.Fatal("EnsureClaudeState succeeded with no host login; want an error")
		}
	})

	// ~/.claude present but ~/.claude.json missing.
	t.Run("no claude json", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		env := &Env{DataDir: t.TempDir()}
		if err := env.EnsureClaudeState("case"); err == nil {
			t.Fatal("EnsureClaudeState succeeded without ~/.claude.json; want an error")
		}
	})

	// ~/.claude.json exists but as a directory — the state a bind mount with a
	// missing source leaves behind.
	t.Run("claude json is a directory", func(t *testing.T) {
		home := hostLogin(t)
		jsonPath := filepath.Join(home, ".claude.json")
		if err := os.Remove(jsonPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(jsonPath, 0o700); err != nil {
			t.Fatal(err)
		}
		env := &Env{DataDir: t.TempDir()}
		if err := env.EnsureClaudeState("case"); err == nil {
			t.Fatal("EnsureClaudeState succeeded with ~/.claude.json as a directory; want an error")
		}
	})
}

func TestReseedClaudeCredentials(t *testing.T) {
	home := hostLogin(t)
	env := &Env{DataDir: t.TempDir()}

	if err := env.EnsureClaudeState("case"); err != nil {
		t.Fatalf("EnsureClaudeState: %v", err)
	}

	// The agent's copy drifts (simulating a rotation that broke a copied
	// credential); the host then holds the fresh token.
	agentCreds := filepath.Join(env.ClaudeDir("case"), ".credentials.json")
	if err := os.Remove(agentCreds); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentCreds, []byte(`{"token":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := env.ReseedClaudeCredentials("case"); err != nil {
		t.Fatalf("ReseedClaudeCredentials: %v", err)
	}
	data, err := os.ReadFile(filepath.Clean(agentCreds))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"token":"host-secret"}` {
		t.Errorf("agent credentials = %s, want the host's", data)
	}
	ai, _ := os.Stat(agentCreds)
	hi, _ := os.Stat(filepath.Join(home, ".claude", ".credentials.json"))
	if !os.SameFile(ai, hi) {
		t.Error("reseeded credentials are not hard-linked to the host's")
	}
}
