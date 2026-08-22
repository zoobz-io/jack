package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSecrets writes content as agent "case"'s secrets file with the given
// mode, creating the secrets dir, and returns the env used.
func writeSecrets(t *testing.T, content string, mode os.FileMode) *Env {
	t.Helper()
	env := &Env{DataDir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(env.DataDir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.SecretsPath("case"), []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestAgentSecrets(t *testing.T) {
	env := writeSecrets(t, `
# case's GitHub identity
GH_TOKEN=ghp_secret123

EMPTY_OK=
SPACED = padded value
`, 0o600)

	secrets, err := env.AgentSecrets("case")
	if err != nil {
		t.Fatalf("AgentSecrets: %v", err)
	}
	want := map[string]string{
		"GH_TOKEN": "ghp_secret123",
		"EMPTY_OK": "",
		"SPACED":   "padded value",
	}
	if len(secrets) != len(want) {
		t.Errorf("got %d secrets %v, want %d", len(secrets), secrets, len(want))
	}
	for k, v := range want {
		if secrets[k] != v {
			t.Errorf("secrets[%q] = %q, want %q", k, secrets[k], v)
		}
	}
}

func TestAgentSecretsMissingFile(t *testing.T) {
	env := &Env{DataDir: t.TempDir()}
	secrets, err := env.AgentSecrets("case")
	if err != nil {
		t.Fatalf("AgentSecrets with no file: %v", err)
	}
	if secrets != nil {
		t.Errorf("secrets = %v, want nil for a missing file", secrets)
	}
}

func TestAgentSecretsMalformedLine(t *testing.T) {
	env := writeSecrets(t, "GH_TOKEN ghp_no_equals\n", 0o600)
	if _, err := env.AgentSecrets("case"); err == nil {
		t.Fatal("AgentSecrets accepted a line without '='; want an error")
	}

	env = writeSecrets(t, "=value_without_key\n", 0o600)
	if _, err := env.AgentSecrets("case"); err == nil {
		t.Fatal("AgentSecrets accepted an empty key; want an error")
	}
}

func TestAgentSecretsLoosePermissions(t *testing.T) {
	env := writeSecrets(t, "GH_TOKEN=x\n", 0o644)
	if _, err := env.AgentSecrets("case"); err == nil {
		t.Fatal("AgentSecrets accepted a group/other-readable file; want an error")
	}
}

func TestWriteSessionEnv(t *testing.T) {
	env := writeSecrets(t, "GH_TOKEN=ghp_secret123\nB_KEY=with 'quotes' inside\nA_KEY=plain\n", 0o600)

	if err := env.WriteSessionEnv("case"); err != nil {
		t.Fatalf("WriteSessionEnv: %v", err)
	}

	data, err := os.ReadFile(env.SessionEnv("case"))
	if err != nil {
		t.Fatalf("session env missing: %v", err)
	}
	got := string(data)

	// Exports are sorted and shell-quoted so sourcing reproduces the values
	// AgentSecrets read, byte for byte.
	wantLines := []string{
		"export A_KEY='plain'",
		`export B_KEY='with '\''quotes'\'' inside'`,
		"export GH_TOKEN='ghp_secret123'",
	}
	idx := -1
	for _, line := range wantLines {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("session env missing line %q in:\n%s", line, got)
			continue
		}
		if at := strings.Index(got, line); at < idx {
			t.Errorf("session env line %q out of sorted order in:\n%s", line, got)
		} else {
			idx = at
		}
	}

	info, err := os.Stat(env.SessionEnv("case"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("session env mode = %o, want 600", perm)
	}
}

func TestWriteSessionEnvWithoutSecrets(t *testing.T) {
	// No secrets file: the session env must still exist (a missing bind-mount
	// source would make docker manufacture a directory), just with no exports.
	env := &Env{DataDir: t.TempDir()}

	if err := env.WriteSessionEnv("case"); err != nil {
		t.Fatalf("WriteSessionEnv: %v", err)
	}
	data, err := os.ReadFile(env.SessionEnv("case"))
	if err != nil {
		t.Fatalf("session env missing: %v", err)
	}
	if strings.Contains(string(data), "export ") {
		t.Errorf("session env has exports without a secrets file:\n%s", data)
	}
}

func TestWriteSessionEnvRewritesInPlace(t *testing.T) {
	// A running container bind-mounts the session env by inode; re-rendering
	// must truncate and rewrite, never replace the file.
	env := writeSecrets(t, "GH_TOKEN=old\n", 0o600)
	if err := env.WriteSessionEnv("case"); err != nil {
		t.Fatalf("first WriteSessionEnv: %v", err)
	}
	before, err := os.Stat(env.SessionEnv("case"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(env.SecretsPath("case"), []byte("GH_TOKEN=new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := env.WriteSessionEnv("case"); err != nil {
		t.Fatalf("second WriteSessionEnv: %v", err)
	}

	after, err := os.Stat(env.SessionEnv("case"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("session env was replaced (new inode), want an in-place rewrite")
	}
	data, err := os.ReadFile(env.SessionEnv("case"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "export GH_TOKEN='new'") {
		t.Errorf("session env not re-rendered:\n%s", data)
	}
}

func TestWriteSessionEnvPropagatesSecretsErrors(t *testing.T) {
	// A loose-permissions secrets file must fail the render, same as it fails
	// container creation.
	env := writeSecrets(t, "GH_TOKEN=x\n", 0o644)
	if err := env.WriteSessionEnv("case"); err == nil {
		t.Fatal("WriteSessionEnv succeeded with a world-readable secrets file; want an error")
	}
}
