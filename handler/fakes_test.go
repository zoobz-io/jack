package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zoobzio/jack/config"
	"github.com/zoobzio/jack/core"
	"github.com/zoobzio/jack/domain"
)

// execCall records one Docker.Exec invocation.
type execCall struct {
	Name string
	Cmd  []string
}

// createCall records one Tmux.Create invocation.
type createCall struct {
	Name string
	Cmd  string
}

// cloneCall records one Git.Clone invocation.
type cloneCall struct {
	URL string
	Dir string
}

// configCall records one Git.Config invocation.
type configCall struct {
	Dir   string
	Key   string
	Value string
}

// fakeDocker is a recording stand-in for core.Docker. Every method records its
// call and returns the configured result/error.
type fakeDocker struct {
	BuildCalls        int
	RunSpecs          []core.Spec
	ExecCalls         []execCall
	StopNames         []string
	RemoveVolumeNames []string

	RunningResult bool
	RunningErr    error

	BuildErr        error
	RunErr          error
	ExecErr         error
	StopErr         error
	RemoveVolumeErr error
}

func (d *fakeDocker) Build(_ context.Context) error {
	d.BuildCalls++
	return d.BuildErr
}

func (d *fakeDocker) Run(_ context.Context, spec core.Spec) error {
	d.RunSpecs = append(d.RunSpecs, spec)
	return d.RunErr
}

func (d *fakeDocker) Exec(_ context.Context, name string, cmd []string) error {
	d.ExecCalls = append(d.ExecCalls, execCall{Name: name, Cmd: cmd})
	return d.ExecErr
}

func (d *fakeDocker) Stop(_ context.Context, name string) error {
	d.StopNames = append(d.StopNames, name)
	return d.StopErr
}

func (d *fakeDocker) RemoveVolume(_ context.Context, name string) error {
	d.RemoveVolumeNames = append(d.RemoveVolumeNames, name)
	return d.RemoveVolumeErr
}

func (d *fakeDocker) Running(_ context.Context, _ string) (bool, error) {
	return d.RunningResult, d.RunningErr
}

// fakeTmux is a recording stand-in for core.Tmux.
type fakeTmux struct {
	HasResult bool
	HasErr    error

	CreateCalls []createCall
	AttachNames []string
	KillNames   []string

	ListResult []domain.Session
	ListErr    error

	CreateErr error
	AttachErr error
	KillErr   error
}

func (t *fakeTmux) Has(_ context.Context, _ string) (bool, error) {
	return t.HasResult, t.HasErr
}

func (t *fakeTmux) Create(_ context.Context, name, cmd string) error {
	t.CreateCalls = append(t.CreateCalls, createCall{Name: name, Cmd: cmd})
	return t.CreateErr
}

func (t *fakeTmux) Attach(_ context.Context, name string) error {
	t.AttachNames = append(t.AttachNames, name)
	return t.AttachErr
}

func (t *fakeTmux) Kill(_ context.Context, name string) error {
	t.KillNames = append(t.KillNames, name)
	return t.KillErr
}

func (t *fakeTmux) List(_ context.Context) ([]domain.Session, error) {
	return t.ListResult, t.ListErr
}

// fakeGit is a recording stand-in for core.Git.
type fakeGit struct {
	CloneCalls  []cloneCall
	ConfigCalls []configCall

	IdentityName  string
	IdentityEmail string
	IdentityErr   error

	CloneErr  error
	ConfigErr error
}

func (g *fakeGit) Clone(_ context.Context, url, dir string) error {
	g.CloneCalls = append(g.CloneCalls, cloneCall{URL: url, Dir: dir})
	return g.CloneErr
}

func (g *fakeGit) Config(_ context.Context, dir, key, value string) error {
	g.ConfigCalls = append(g.ConfigCalls, configCall{Dir: dir, Key: key, Value: value})
	return g.ConfigErr
}

func (g *fakeGit) GlobalIdentity(_ context.Context) (name, email string, err error) {
	return g.IdentityName, g.IdentityEmail, g.IdentityErr
}

// claudeHome points HOME at a temp dir seeded with the host Claude login
// (~/.claude with credentials, plus ~/.claude.json) that seeding the agents'
// private Claude state requires, and returns the home path.
func claudeHome(t *testing.T) string {
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
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write .claude.json: %v", err)
	}
	return home
}

// testEnv builds a config.Env whose directories live under a fresh t.TempDir(),
// so handlers that touch the filesystem stay isolated per test.
func testEnv(t *testing.T) *config.Env {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatalf("mkdir configDir: %v", err)
	}
	return &config.Env{
		ConfigDir:    configDir,
		ConfigPath:   filepath.Join(configDir, "config.yaml"),
		DataDir:      dataDir,
		RegistryPath: filepath.Join(dataDir, "registry.yaml"),
	}
}

// testApp wires an *core.App from an env and the three fakes plus a config. Any
// of the fakes may be nil, in which case a bare recording fake is substituted.
func testApp(env *config.Env, cfg *config.Config, d *fakeDocker, tm *fakeTmux, g *fakeGit) *core.App {
	if d == nil {
		d = &fakeDocker{}
	}
	if tm == nil {
		tm = &fakeTmux{}
	}
	if g == nil {
		g = &fakeGit{}
	}
	return core.NewAppWith(env, cfg, d, tm, g)
}

// profileConfig builds a Config with a single profile for agent, whose git
// identity is set so clone/spec code paths that read it have values to use.
func profileConfig(agent domain.Agent) *config.Config {
	return &config.Config{
		Profiles: map[domain.Agent]config.Profile{
			agent: {
				Git: config.GitConfig{Name: "Test Agent", Email: "agent@test.io"},
			},
		},
	}
}
