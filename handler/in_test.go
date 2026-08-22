package handler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoobzio/jack/config"
	"github.com/zoobzio/jack/domain"
)

func TestInSessionExistsAttaches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := testEnv(t)

	tm := &fakeTmux{HasResult: true}
	d := &fakeDocker{}
	app := testApp(env, profileConfig("alex"), d, tm, &fakeGit{})

	if err := in(context.Background(), app, "alex", "jack", false); err != nil {
		t.Fatalf("in returned error: %v", err)
	}

	if len(tm.AttachNames) != 1 || tm.AttachNames[0] != "alex-jack" {
		t.Errorf("Attach = %v, want [alex-jack]", tm.AttachNames)
	}
	if len(d.RunSpecs) != 0 {
		t.Errorf("Run called %d times, want 0 for an existing session", len(d.RunSpecs))
	}
	if len(tm.CreateCalls) != 0 {
		t.Errorf("Create called for an existing session: %v", tm.CreateCalls)
	}
}

func TestInStartsContainerAndCreatesSession(t *testing.T) {
	claudeHome(t)
	env := testEnv(t)

	// Session absent and container nonexistent (Running errors for a container
	// that does not exist).
	tm := &fakeTmux{HasResult: false}
	d := &fakeDocker{RunningErr: errors.New("no such container")}
	app := testApp(env, profileConfig("alex"), d, tm, &fakeGit{})

	if err := in(context.Background(), app, "alex", "jack", false); err != nil {
		t.Fatalf("in returned error: %v", err)
	}

	if len(d.StopNames) != 0 {
		t.Errorf("Stop called for a nonexistent container: %v", d.StopNames)
	}
	if len(d.RunSpecs) != 1 {
		t.Fatalf("Run called %d times, want 1", len(d.RunSpecs))
	}
	if d.RunSpecs[0].Name != "jack-alex-jack" {
		t.Errorf("Run spec name = %q, want jack-alex-jack", d.RunSpecs[0].Name)
	}
	if len(tm.CreateCalls) != 1 || tm.CreateCalls[0].Name != "alex-jack" {
		t.Errorf("Create = %v, want one call for alex-jack", tm.CreateCalls)
	}
	if len(tm.AttachNames) != 1 || tm.AttachNames[0] != "alex-jack" {
		t.Errorf("Attach = %v, want [alex-jack]", tm.AttachNames)
	}
	// CA.URL is empty and no setup scripts exist, so nothing was exec'd.
	if len(d.ExecCalls) != 0 {
		t.Errorf("Exec called %d times, want 0", len(d.ExecCalls))
	}

	// A fresh start seeds the agent's private Claude state before the mounts.
	if _, err := os.Stat(env.ClaudeDir("alex")); err != nil {
		t.Errorf("agent claude dir not seeded: %v", err)
	}
	if _, err := os.Stat(env.ClaudeJSON("alex")); err != nil {
		t.Errorf("agent claude.json not seeded: %v", err)
	}
	// …and renders the session env the spec mounts, even with no secrets.
	if _, err := os.Stat(env.SessionEnv("alex")); err != nil {
		t.Errorf("session env not rendered: %v", err)
	}

	// The launch sources the session env before exec'ing claude, so a later
	// `jack refresh` reaches the next launch without recreating the container.
	cmd := tm.CreateCalls[0].Cmd
	if !strings.Contains(cmd, ". /root/.jack/session.env; exec claude") {
		t.Errorf("launch cmd = %q, want it to source the session env then exec claude", cmd)
	}
}

func TestInInjectsAgentSecrets(t *testing.T) {
	claudeHome(t)
	env := testEnv(t)

	// A secrets file for the agent lands in the container env on fresh start.
	if err := os.MkdirAll(filepath.Join(env.DataDir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.SecretsPath("alex"), []byte("GH_TOKEN=ghp_alex\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := &fakeDocker{RunningErr: errors.New("no such container")}
	app := testApp(env, profileConfig("alex"), d, &fakeTmux{HasResult: false}, &fakeGit{})

	if err := in(context.Background(), app, "alex", "jack", false); err != nil {
		t.Fatalf("in returned error: %v", err)
	}
	if got := d.RunSpecs[0].Env["GH_TOKEN"]; got != "ghp_alex" {
		t.Errorf("GH_TOKEN = %q, want ghp_alex", got)
	}
}

func TestInReseedRelinksCredentials(t *testing.T) {
	home := claudeHome(t)
	env := testEnv(t)

	// Session already exists, so in only attaches — but --reseed must still
	// relink the agent's credentials from the host login first.
	tm := &fakeTmux{HasResult: true}
	app := testApp(env, profileConfig("alex"), &fakeDocker{}, tm, &fakeGit{})

	if err := in(context.Background(), app, "alex", "jack", true); err != nil {
		t.Fatalf("in returned error: %v", err)
	}

	ai, err := os.Stat(filepath.Join(env.ClaudeDir("alex"), ".credentials.json"))
	if err != nil {
		t.Fatalf("agent credentials missing after reseed: %v", err)
	}
	hi, err := os.Stat(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(ai, hi) {
		t.Error("reseeded credentials are not hard-linked to the host's")
	}
	if len(tm.AttachNames) != 1 {
		t.Errorf("Attach = %v, want one call", tm.AttachNames)
	}
}

func TestInRemovesStoppedContainerBeforeRun(t *testing.T) {
	claudeHome(t)
	env := testEnv(t)

	// Session absent; the container exists but is stopped (e.g. after a host
	// reboot): Running reports (false, nil). The remnant must be removed before
	// the fresh run, or `docker run --name` would collide with it.
	tm := &fakeTmux{HasResult: false}
	d := &fakeDocker{RunningResult: false, RunningErr: nil}
	app := testApp(env, profileConfig("alex"), d, tm, &fakeGit{})

	if err := in(context.Background(), app, "alex", "jack", false); err != nil {
		t.Fatalf("in returned error: %v", err)
	}

	if len(d.StopNames) != 1 || d.StopNames[0] != "jack-alex-jack" {
		t.Errorf("Stop = %v, want [jack-alex-jack] to clear the stopped remnant", d.StopNames)
	}
	if len(d.RunSpecs) != 1 {
		t.Fatalf("Run called %d times, want 1", len(d.RunSpecs))
	}
	if len(tm.AttachNames) != 1 || tm.AttachNames[0] != "alex-jack" {
		t.Errorf("Attach = %v, want [alex-jack]", tm.AttachNames)
	}
}

func TestInStoppedContainerRemovalFails(t *testing.T) {
	claudeHome(t)
	env := testEnv(t)

	// Removing the stopped remnant fails: in must surface the error rather than
	// attempt a doomed `docker run`.
	tm := &fakeTmux{HasResult: false}
	d := &fakeDocker{RunningResult: false, StopErr: errors.New("permission denied")}
	app := testApp(env, profileConfig("alex"), d, tm, &fakeGit{})

	if err := in(context.Background(), app, "alex", "jack", false); err == nil {
		t.Fatal("in succeeded despite failing to remove the stopped container")
	}
	if len(d.RunSpecs) != 0 {
		t.Errorf("Run called %d times after a failed removal, want 0", len(d.RunSpecs))
	}
}

func TestInModelResolution(t *testing.T) {
	// The top-level default reaches the container when the profile sets no model.
	t.Run("default", func(t *testing.T) {
		claudeHome(t)
		cfg := &config.Config{
			Model:    "claude-sonnet-5",
			Profiles: map[domain.Agent]config.Profile{"alex": {}},
		}
		d := &fakeDocker{RunningErr: errors.New("no such container")}
		app := testApp(testEnv(t), cfg, d, &fakeTmux{HasResult: false}, &fakeGit{})

		if err := in(context.Background(), app, "alex", "jack", false); err != nil {
			t.Fatalf("in returned error: %v", err)
		}
		if got := d.RunSpecs[0].Env["ANTHROPIC_MODEL"]; got != "claude-sonnet-5" {
			t.Errorf("ANTHROPIC_MODEL = %q, want claude-sonnet-5 (top-level default)", got)
		}
	})

	// A per-profile model overrides the top-level default.
	t.Run("override", func(t *testing.T) {
		claudeHome(t)
		cfg := &config.Config{
			Model:    "claude-sonnet-5",
			Profiles: map[domain.Agent]config.Profile{"alex": {Model: "claude-opus-4-8"}},
		}
		d := &fakeDocker{RunningErr: errors.New("no such container")}
		app := testApp(testEnv(t), cfg, d, &fakeTmux{HasResult: false}, &fakeGit{})

		if err := in(context.Background(), app, "alex", "jack", false); err != nil {
			t.Fatalf("in returned error: %v", err)
		}
		if got := d.RunSpecs[0].Env["ANTHROPIC_MODEL"]; got != "claude-opus-4-8" {
			t.Errorf("ANTHROPIC_MODEL = %q, want claude-opus-4-8 (profile override)", got)
		}
	})
}

func TestInPermissionResolution(t *testing.T) {
	// The top-level default flows into the launch command when the profile is bare.
	t.Run("default", func(t *testing.T) {
		claudeHome(t)
		cfg := &config.Config{
			Permission: config.PermissionBypass,
			Profiles:   map[domain.Agent]config.Profile{"alex": {}},
		}
		tm := &fakeTmux{HasResult: false}
		app := testApp(testEnv(t), cfg, &fakeDocker{RunningErr: errors.New("no such container")}, tm, &fakeGit{})

		if err := in(context.Background(), app, "alex", "jack", false); err != nil {
			t.Fatalf("in returned error: %v", err)
		}
		if len(tm.CreateCalls) != 1 {
			t.Fatalf("Create calls = %d, want 1", len(tm.CreateCalls))
		}
		if !strings.Contains(tm.CreateCalls[0].Cmd, "--dangerously-skip-permissions") {
			t.Errorf("launch cmd = %q, want it to contain --dangerously-skip-permissions", tm.CreateCalls[0].Cmd)
		}
	})

	// A per-profile permission overrides the top-level default.
	t.Run("override", func(t *testing.T) {
		claudeHome(t)
		cfg := &config.Config{
			Permission: config.PermissionBypass,
			Profiles:   map[domain.Agent]config.Profile{"alex": {Permission: config.PermissionAcceptEdits}},
		}
		tm := &fakeTmux{HasResult: false}
		app := testApp(testEnv(t), cfg, &fakeDocker{RunningErr: errors.New("no such container")}, tm, &fakeGit{})

		if err := in(context.Background(), app, "alex", "jack", false); err != nil {
			t.Fatalf("in returned error: %v", err)
		}
		cmd := tm.CreateCalls[0].Cmd
		if !strings.Contains(cmd, "--permission-mode acceptEdits") {
			t.Errorf("launch cmd = %q, want --permission-mode acceptEdits", cmd)
		}
		if strings.Contains(cmd, "dangerously") {
			t.Errorf("launch cmd = %q, should not contain the bypass flag", cmd)
		}
	})
}
