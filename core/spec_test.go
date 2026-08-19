package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zoobzio/jack/config"
	"github.com/zoobzio/jack/domain"
)

// findMount returns the mount with the given container Target, or nil.
func findMount(mounts []Mount, target string) *Mount {
	for i := range mounts {
		if mounts[i].Target == target {
			return &mounts[i]
		}
	}
	return nil
}

func TestNewSpec(t *testing.T) {
	dataDir := t.TempDir()
	configDir := t.TempDir()

	env := &config.Env{
		ConfigDir:  configDir,
		ConfigPath: filepath.Join(configDir, "config.yaml"),
		DataDir:    dataDir,
	}

	id, err := domain.NewIdentity(domain.Agent("scout"), domain.Repo("myrepo"))
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	profile := config.Profile{
		Git: config.GitConfig{Name: "Ada", Email: "ada@example.com"},
	}
	ca := config.CAConfig{
		URL:         "https://ca.example.com",
		Fingerprint: "abc123",
		Provisioner: "jack",
	}

	spec := NewSpec(id, profile, env, ca)

	if spec.Name != id.Container {
		t.Errorf("spec.Name = %q, want %q", spec.Name, id.Container)
	}

	// The four standard binds plus the read-only .config/jack mount. The Claude
	// state binds come from the agent's private state under DataDir, not the
	// host's ~/.claude.
	wantMounts := map[string]struct {
		source   string
		readOnly bool
	}{
		home + "/.claude":           {source: filepath.Join(dataDir, "scout", "claude"), readOnly: false},
		home + "/.claude.json":      {source: filepath.Join(dataDir, "scout", "claude.json"), readOnly: false},
		home + "/workspace/.claude": {source: filepath.Join(dataDir, "scout", ".claude"), readOnly: true},
		home + "/workspace/myrepo":  {source: filepath.Join(dataDir, "scout", "myrepo"), readOnly: false},
		home + "/.config/jack":      {source: configDir, readOnly: true},
	}
	for target, want := range wantMounts {
		m := findMount(spec.Mounts, target)
		if m == nil {
			t.Errorf("missing mount for target %q", target)
			continue
		}
		if m.Source != want.source {
			t.Errorf("mount %q source = %q, want %q", target, m.Source, want.source)
		}
		if m.ReadOnly != want.readOnly {
			t.Errorf("mount %q ReadOnly = %v, want %v", target, m.ReadOnly, want.readOnly)
		}
	}

	// Tools volume.
	if len(spec.Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(spec.Volumes))
	}
	tools := spec.Volumes[0]
	if tools.Name != id.Container+"-tools" {
		t.Errorf("tools volume name = %q, want %q", tools.Name, id.Container+"-tools")
	}
	if tools.Target != home+"/.jack/bin" {
		t.Errorf("tools volume target = %q, want %q", tools.Target, home+"/.jack/bin")
	}

	// Env.
	wantEnv := map[string]string{
		"JACK_AGENT":          "scout",
		"GIT_AUTHOR_NAME":     "Ada",
		"GIT_COMMITTER_NAME":  "Ada",
		"GIT_AUTHOR_EMAIL":    "ada@example.com",
		"GIT_COMMITTER_EMAIL": "ada@example.com",
		"JACK_CA_URL":         "https://ca.example.com",
		"JACK_CA_FINGERPRINT": "abc123",
		"JACK_CA_PROVISIONER": "jack",
	}
	for k, v := range wantEnv {
		if spec.Env[k] != v {
			t.Errorf("env[%q] = %q, want %q", k, spec.Env[k], v)
		}
	}
}

func TestNewSpecModel(t *testing.T) {
	env := &config.Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()}

	id, err := domain.NewIdentity(domain.Agent("scout"), domain.Repo("myrepo"))
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	// A profile with a model sets ANTHROPIC_MODEL.
	spec := NewSpec(id, config.Profile{Model: "claude-opus-4-8"}, env, config.CAConfig{})
	if spec.Env["ANTHROPIC_MODEL"] != "claude-opus-4-8" {
		t.Errorf("ANTHROPIC_MODEL = %q, want claude-opus-4-8", spec.Env["ANTHROPIC_MODEL"])
	}

	// A profile with no model leaves ANTHROPIC_MODEL unset.
	spec = NewSpec(id, config.Profile{}, env, config.CAConfig{})
	if _, ok := spec.Env["ANTHROPIC_MODEL"]; ok {
		t.Errorf("ANTHROPIC_MODEL set unexpectedly = %q", spec.Env["ANTHROPIC_MODEL"])
	}
}

func TestNewSpecColorterm(t *testing.T) {
	env := &config.Env{ConfigDir: t.TempDir(), DataDir: t.TempDir()}
	id, err := domain.NewIdentity(domain.Agent("scout"), domain.Repo("myrepo"))
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	// Propagates the host value when set.
	t.Run("propagates", func(t *testing.T) {
		t.Setenv("COLORTERM", "24bit")
		spec := NewSpec(id, config.Profile{}, env, config.CAConfig{})
		if spec.Env["COLORTERM"] != "24bit" {
			t.Errorf("COLORTERM = %q, want 24bit", spec.Env["COLORTERM"])
		}
	})

	// Defaults to truecolor when the host has none.
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("COLORTERM", "")
		spec := NewSpec(id, config.Profile{}, env, config.CAConfig{})
		if spec.Env["COLORTERM"] != "truecolor" {
			t.Errorf("COLORTERM = %q, want truecolor", spec.Env["COLORTERM"])
		}
	})
}

func TestNewSpecMinimal(t *testing.T) {
	dataDir := t.TempDir()
	configDir := t.TempDir()
	env := &config.Env{ConfigDir: configDir, DataDir: dataDir}

	id, err := domain.NewIdentity(domain.Agent("scout"), domain.Repo("myrepo"))
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	// Empty profile git and empty CA: only JACK_AGENT should be set.
	spec := NewSpec(id, config.Profile{}, env, config.CAConfig{})

	if spec.Env["JACK_AGENT"] != "scout" {
		t.Errorf("JACK_AGENT = %q, want scout", spec.Env["JACK_AGENT"])
	}
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "JACK_CA_URL", "JACK_CA_FINGERPRINT", "JACK_CA_PROVISIONER"} {
		if _, ok := spec.Env[k]; ok {
			t.Errorf("env[%q] set unexpectedly = %q", k, spec.Env[k])
		}
	}
}

func TestNewSpecSupportRepoOnDisk(t *testing.T) {
	dataDir := t.TempDir()
	configDir := t.TempDir()
	env := &config.Env{ConfigDir: configDir, DataDir: dataDir}

	id, err := domain.NewIdentity(domain.Agent("scout"), domain.Repo("myrepo"))
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	// Create the support repo dir on disk so it is mounted.
	supportDir := filepath.Join(dataDir, "scout", "other")
	if err := os.MkdirAll(supportDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	profile := config.Profile{Repos: []string{"https://host/u/other.git"}}
	spec := NewSpec(id, profile, env, config.CAConfig{})

	m := findMount(spec.Mounts, "/repos/other")
	if m == nil {
		t.Fatal("expected a mount for the on-disk support repo at /repos/other")
	}
	if m.Source != supportDir {
		t.Errorf("support mount source = %q, want %q", m.Source, supportDir)
	}
}

func TestNewSpecSupportRepoMissingSkipped(t *testing.T) {
	dataDir := t.TempDir()
	configDir := t.TempDir()
	env := &config.Env{ConfigDir: configDir, DataDir: dataDir}

	id, err := domain.NewIdentity(domain.Agent("scout"), domain.Repo("myrepo"))
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	// Support repo not created on disk -> skipped.
	profile := config.Profile{Repos: []string{"https://host/u/other.git"}}
	spec := NewSpec(id, profile, env, config.CAConfig{})

	if m := findMount(spec.Mounts, "/repos/other"); m != nil {
		t.Errorf("support repo not on disk should be skipped, got mount %+v", m)
	}
}
