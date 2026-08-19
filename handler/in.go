package handler

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/zoobzio/jack/config"
	"github.com/zoobzio/jack/core"
	"github.com/zoobzio/jack/domain"
	"github.com/zoobzio/jack/tools"
)

// In builds the `jack in` command and mounts it onto the app's root command.
func In(app *core.App) {
	cmd := &cobra.Command{
		Use:   "in",
		Short: "Enter a session",
		Long:  "Attach to an existing session or create one.\nWith no arguments, interactively select an agent and project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agent, _ := cmd.Flags().GetString("agent")
			project, _ := cmd.Flags().GetString("project")
			reseed, _ := cmd.Flags().GetBool("reseed")
			return in(cmd.Context(), app, domain.Agent(agent), domain.Repo(project), reseed)
		},
	}
	cmd.Flags().StringP("agent", "a", "", "agent name")
	cmd.Flags().StringP("project", "p", "", "project name")
	cmd.Flags().Bool("reseed", false, "replace the agent's Claude credentials with a fresh link to the host login")
	app.Root().AddCommand(cmd)
}

// in attaches to a session, creating it (and its container) on demand. An empty
// agent or project is resolved from the registry, interactively when there is
// more than one choice. With reseed set, the agent's Claude credentials are
// relinked from the host login first — the recovery path for a credential copy
// that drifted from the host's after a token rotation.
func in(ctx context.Context, app *core.App, agent domain.Agent, repo domain.Repo, reseed bool) error {
	reg, err := config.NewRegistry(app.Env().RegistryPath)
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	agent, repo, err = resolve(reg, agent, repo)
	if err != nil {
		return err
	}

	profile, ok := app.Config().Profiles[agent]
	if !ok {
		return fmt.Errorf("unknown agent %q (no matching profile)", agent)
	}
	// Fall back to the top-level defaults when the profile sets none.
	if profile.Model == "" {
		profile.Model = app.Config().Model
	}
	if profile.Permission == "" {
		profile.Permission = app.Config().Permission
	}

	id, err := domain.NewIdentity(agent, repo)
	if err != nil {
		return err
	}

	if reseed {
		if rerr := app.Env().ReseedClaudeCredentials(agent); rerr != nil {
			return fmt.Errorf("reseeding credentials: %w", rerr)
		}
		fmt.Printf("reseeded Claude credentials for agent %s\n", agent)
	}

	// Attach to the session if it already exists.
	if has, herr := app.Tmux().Has(ctx, id.Session); herr != nil {
		return herr
	} else if has {
		return app.Tmux().Attach(ctx, id.Session)
	}

	scr := tools.For(id)

	// Ensure the container is up. Running errors when the container does not
	// exist (or docker itself is unavailable, which the Run below then reports);
	// (false, nil) means it exists but is stopped — e.g. after a host reboot —
	// and would collide with the fresh `docker run`, so remove the remnant and
	// rebuild it. The tools volume survives; the container is disposable.
	running, rerr := app.Docker().Running(ctx, id.Container)
	if !running {
		if rerr == nil {
			if serr := app.Docker().Stop(ctx, id.Container); serr != nil {
				return fmt.Errorf("removing stopped container %s: %w", id.Container, serr)
			}
		}
		// Seed the agent's private Claude state (auth plus a fresh memory) before
		// the container mounts it.
		if serr := app.Env().EnsureClaudeState(agent); serr != nil {
			return serr
		}

		spec := core.NewSpec(id, profile, app.Env(), app.Config().CA)
		if rerr := app.Docker().Run(ctx, *spec); rerr != nil {
			return fmt.Errorf("starting container: %w", rerr)
		}

		// Bootstrap the agent certificate when a CA is configured; its renew
		// daemon then keeps it fresh for the life of the container.
		if app.Config().CA.URL != "" {
			fmt.Println("bootstrapping agent certificate...")
			if berr := app.Docker().Exec(ctx, id.Container, scr.Bootstrap()); berr != nil {
				_ = app.Docker().Stop(ctx, id.Container)
				return fmt.Errorf("bootstrapping agent certificate: %w", berr)
			}
		}

		// Run the setup scripts that exist on the host, in order.
		for _, s := range scr.Setup(app.Env().ConfigDir) {
			if _, statErr := os.Stat(s.HostPath); statErr != nil {
				continue
			}
			fmt.Printf("running %s...\n", s.Label)
			if eerr := app.Docker().Exec(ctx, id.Container, s.Command); eerr != nil {
				_ = app.Docker().Stop(ctx, id.Container)
				return fmt.Errorf("running %s: %w", s.Label, eerr)
			}
		}
	}

	// tmux drives a `docker exec` into the session's workdir, launching claude in
	// the agent's permission mode.
	launch := "claude"
	if flags := profile.Permission.Flags(); flags != "" {
		launch += " " + flags
	}
	tmuxCmd := fmt.Sprintf("docker exec -it -w %s %s %s", id.RepoPath(), id.Container, launch)
	if cerr := app.Tmux().Create(ctx, id.Session, tmuxCmd); cerr != nil {
		if !running {
			_ = app.Docker().Stop(ctx, id.Container)
		}
		return cerr
	}

	return app.Tmux().Attach(ctx, id.Session)
}
