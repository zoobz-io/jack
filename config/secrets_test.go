package config

import (
	"os"
	"path/filepath"
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
