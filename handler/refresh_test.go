package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoobzio/jack/config"
)

// refreshFixture builds the world refresh operates on: a host login, an agent
// config dir, a registry entry for the agent, and drifted agent Claude state
// (stale credentials no longer linked to the host's, stale account keys mixed
// with agent memory). It returns the env and the host home path.
func refreshFixture(t *testing.T) (*config.Env, string) {
	t.Helper()
	env := testEnv(t)
	home := claudeHome(t)

	// Host claude.json with account keys the agent should pick up.
	hostJSON := `{"userID": "u-fresh", "oauthAccount": {"email": "op@example.com"}, "projects": {"/host/only": {}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(hostJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Agent config to apply.
	agentSrc := filepath.Join(env.ConfigDir, "agents", "case")
	if err := os.MkdirAll(agentSrc, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentSrc, "CLAUDE.md"), []byte("fresh config"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Registry knows the agent.
	reg, err := config.NewRegistry(env.RegistryPath)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	reg.Add("case", "app", "https://example.com/app.git")
	if err := reg.Save(); err != nil {
		t.Fatalf("saving registry: %v", err)
	}

	// A secrets file and a stale rendered session env, as a container created
	// before a token rotation would have.
	if err := os.MkdirAll(filepath.Join(env.DataDir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.SecretsPath("case"), []byte("GH_TOKEN=ghp_new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(env.DataDir, "case"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.SessionEnv("case"), []byte("export GH_TOKEN='ghp_old'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Drifted agent state: a stale credential copy (not linked to the host's)
	// and a claude.json holding stale account keys plus agent memory.
	if err := os.MkdirAll(env.ClaudeDir("case"), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(env.ClaudeDir("case"), ".credentials.json")
	if err := os.WriteFile(stale, []byte(`{"token":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	agentJSON := `{"userID": "u-stale", "agentMemory": "keep me"}`
	if err := os.WriteFile(env.ClaudeJSON("case"), []byte(agentJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	return env, home
}

func TestRefreshSyncsConfigAndCredentials(t *testing.T) {
	env, home := refreshFixture(t)
	app := testApp(env, profileConfig("case"), nil, nil, nil)

	staleEnv, err := os.Stat(env.SessionEnv("case"))
	if err != nil {
		t.Fatal(err)
	}

	if err := refresh(app, "case"); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// Config re-applied into the workspace .claude.
	got, err := os.ReadFile(filepath.Join(env.DataDir, "case", ".claude", "CLAUDE.md")) //nolint:gosec // path is under t.TempDir()
	if err != nil {
		t.Errorf("applied config missing: %v", err)
	} else if string(got) != "fresh config" {
		t.Errorf("applied config = %q, want %q", got, "fresh config")
	}

	// Account keys refreshed, agent memory preserved.
	data, err := os.ReadFile(env.ClaudeJSON("case"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("agent claude.json invalid: %v", err)
	}
	if doc["userID"] != "u-fresh" {
		t.Errorf("userID = %v, want the host's u-fresh", doc["userID"])
	}
	if doc["agentMemory"] != "keep me" {
		t.Errorf("agentMemory = %v, agent state was clobbered", doc["agentMemory"])
	}
	if _, ok := doc["projects"]; ok {
		t.Error("host-only key leaked into agent claude.json")
	}

	// Session env re-rendered from the secrets file, in place (same inode the
	// container's bind mount holds).
	envData, err := os.ReadFile(env.SessionEnv("case"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(envData); !strings.Contains(got, "export GH_TOKEN='ghp_new'") {
		t.Errorf("session env not re-rendered from secrets: %s", got)
	}
	freshEnv, err := os.Stat(env.SessionEnv("case"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(staleEnv, freshEnv) {
		t.Error("session env was replaced (new inode), want an in-place rewrite")
	}

	// Credentials re-linked to the host's (same inode).
	ai, err := os.Stat(filepath.Join(env.ClaudeDir("case"), ".credentials.json"))
	if err != nil {
		t.Fatalf("agent credentials missing: %v", err)
	}
	hi, err := os.Stat(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(ai, hi) {
		t.Error("refreshed credentials are not hard-linked to the host's")
	}
}

func TestRefreshWithoutHostCredentials(t *testing.T) {
	// A host that keeps its login in the keychain has no credentials file; the
	// config half of the refresh still applies and the command succeeds.
	env, home := refreshFixture(t)
	if err := os.Remove(filepath.Join(home, ".claude", ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	app := testApp(env, profileConfig("case"), nil, nil, nil)

	if err := refresh(app, "case"); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// The stale agent credential is untouched (nothing to re-link from) and
	// the config still landed.
	data, err := os.ReadFile(filepath.Join(env.ClaudeDir("case"), ".credentials.json")) //nolint:gosec // path is under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"token":"stale"}` {
		t.Errorf("agent credentials changed without a host source: %s", data)
	}
	if _, err := os.Stat(filepath.Join(env.DataDir, "case", ".claude", "CLAUDE.md")); err != nil {
		t.Errorf("applied config missing: %v", err)
	}
}

func TestRefreshUnknownAgentConfig(t *testing.T) {
	// An agent in the registry but without an agents/<name>/ config dir fails
	// the apply step.
	env, _ := refreshFixture(t)
	if err := os.RemoveAll(filepath.Join(env.ConfigDir, "agents", "case")); err != nil {
		t.Fatal(err)
	}
	app := testApp(env, profileConfig("case"), nil, nil, nil)

	if err := refresh(app, "case"); err == nil {
		t.Fatal("refresh succeeded without an agent config dir; want an error")
	}
}

func TestRefreshEmptyRegistry(t *testing.T) {
	env := testEnv(t)
	claudeHome(t)
	app := testApp(env, profileConfig("case"), nil, nil, nil)

	if err := refresh(app, ""); err == nil {
		t.Fatal("refresh succeeded with an empty registry; want an error")
	}
}
