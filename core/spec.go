package core

import (
	"os"
	"path/filepath"

	"github.com/zoobzio/jack/config"
	"github.com/zoobzio/jack/domain"
)

// home is the root path within a container, shared with the domain path methods
// so the container layout has a single source of truth.
const home = domain.ContainerHome

// Mount is a bind mount: a host path made available inside the container.
type Mount struct {
	Source   string // host path
	Target   string // container path
	ReadOnly bool   // mount read-only when true
}

// Volume is a named, Docker-managed volume mounted into the container. Unlike a
// Mount it has no host path; it persists across container restarts.
type Volume struct {
	Name   string // docker volume name
	Target string // container path
}

// Spec is the plain-data description of a container to run. It is produced by
// the builder functions below from the domain (Identity, Profile, Env) and
// consumed by Docker.Run, which formats it into docker CLI arguments.
type Spec struct {
	Env     map[string]string
	Name    string
	Mounts  []Mount
	Volumes []Volume
}

// NewSpec assembles the container Spec for an agent-repo session: the
// container name, the bind mounts, the persistent tools volume, and the
// environment. The agent's private Claude state paths are mounted as-is; the
// caller must have seeded them first (see Env.EnsureClaudeState). secrets are
// caller-supplied env vars (see Env.AgentSecrets) injected before jack's own,
// so they can never shadow the keys jack depends on.
func NewSpec(id *domain.Identity, profile config.Profile, env *config.Env, ca config.CAConfig, secrets map[string]string) *Spec {
	agentDir := filepath.Join(env.DataDir, string(id.Agent()))
	mounts := []Mount{
		{Source: env.ClaudeDir(id.Agent()), Target: home + "/.claude"},
		{Source: env.ClaudeJSON(id.Agent()), Target: home + "/.claude.json"},
		{Source: filepath.Join(agentDir, ".claude"), Target: home + "/workspace/.claude", ReadOnly: true},
		{Source: filepath.Join(agentDir, string(id.Repo())), Target: home + "/workspace/" + string(id.Repo())},
		// The user's jack config, read-only, so setup scripts can run from it.
		{Source: env.ConfigDir, Target: home + "/.config/jack", ReadOnly: true},
	}

	// Mount supporting repos that have been cloned for this agent. A repo that
	// isn't on disk is skipped so docker doesn't create an empty mount for it.
	for _, repoURL := range profile.Repos {
		name, err := domain.NewRepo(repoURL)
		if err != nil {
			continue
		}
		supportDir := filepath.Join(agentDir, string(name))
		if _, err := os.Stat(supportDir); err != nil {
			continue
		}
		mounts = append(mounts, Mount{Source: supportDir, Target: "/repos/" + string(name)})
	}

	tools := Volume{
		Name:   id.ToolsVolume(),
		Target: home + "/.jack/bin",
	}

	session := make(map[string]string, len(secrets)+8)
	for k, v := range secrets {
		session[k] = v
	}
	session["JACK_AGENT"] = string(id.Agent())

	// docker exec does not inherit the host's COLORTERM, so claude can't detect
	// 24-bit color support inside the container. Propagate it, defaulting to
	// truecolor so it is always set.
	colorterm := os.Getenv("COLORTERM")
	if colorterm == "" {
		colorterm = "truecolor"
	}
	session["COLORTERM"] = colorterm

	if profile.Git.Name != "" {
		session["GIT_AUTHOR_NAME"] = profile.Git.Name
		session["GIT_COMMITTER_NAME"] = profile.Git.Name
	}
	if profile.Git.Email != "" {
		session["GIT_AUTHOR_EMAIL"] = profile.Git.Email
		session["GIT_COMMITTER_EMAIL"] = profile.Git.Email
	}
	if ca.URL != "" {
		session["JACK_CA_URL"] = ca.URL
	}
	if ca.Fingerprint != "" {
		session["JACK_CA_FINGERPRINT"] = ca.Fingerprint
	}
	if ca.Provisioner != "" {
		session["JACK_CA_PROVISIONER"] = ca.Provisioner
	}
	if profile.Model != "" {
		session["ANTHROPIC_MODEL"] = profile.Model
	}

	return &Spec{
		Name:    id.Container,
		Mounts:  mounts,
		Volumes: []Volume{tools},
		Env:     session,
	}
}
