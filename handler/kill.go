package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/zoobzio/jack/config"
	"github.com/zoobzio/jack/core"
	"github.com/zoobzio/jack/domain"
)

// Kill builds the `jack kill` command and mounts it onto the app's root command.
func Kill(app *core.App) {
	cmd := &cobra.Command{
		Use:   "kill",
		Short: "Tear down an agent's container, volume, and clone",
		Long:  "Remove everything jack created for an agent-repo: its session, container, tools volume, on-disk clone, and registry entry. This erases the agent's memories and any uncommitted local changes.\nWith no flags, interactively select the agent and project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agent, _ := cmd.Flags().GetString("agent")
			project, _ := cmd.Flags().GetString("project")
			force, _ := cmd.Flags().GetBool("force")
			return kill(cmd.Context(), app, domain.Agent(agent), domain.Repo(project), force)
		},
	}
	cmd.Flags().StringP("agent", "a", "", "agent name")
	cmd.Flags().StringP("project", "p", "", "project name")
	cmd.Flags().BoolP("force", "f", false, "skip the confirmation prompt")
	app.Root().AddCommand(cmd)
}

// kill tears down everything jack created for an agent-repo: its tmux session,
// container, tools volume, on-disk clone, and registry entry. An empty agent or
// project is resolved from the registry, interactively when there is more than
// one choice. Because this destroys the agent's memories and uncommitted work,
// it asks for confirmation first unless force is set.
func kill(ctx context.Context, app *core.App, agent domain.Agent, repo domain.Repo, force bool) error {
	reg, err := config.NewRegistry(app.Env().RegistryPath)
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	agent, repo, err = resolve(reg, agent, repo)
	if err != nil {
		return err
	}

	id, err := domain.NewIdentity(agent, repo)
	if err != nil {
		return err
	}

	if !force {
		title := fmt.Sprintf(
			"Kill %s for agent %s?\nThis removes the container, tools volume, and clone — erasing the agent's memories and any uncommitted local changes.",
			repo, agent,
		)
		choice, cerr := selectOne(title, []string{"Cancel", "Kill it"})
		if cerr != nil {
			return fmt.Errorf("confirming: %w", cerr)
		}
		if choice != "Kill it" {
			fmt.Println("cancelled")
			return nil
		}
	}

	// Kill the session if it is running.
	if has, herr := app.Tmux().Has(ctx, id.Session); herr != nil {
		return herr
	} else if has {
		if kerr := app.Tmux().Kill(ctx, id.Session); kerr != nil {
			return fmt.Errorf("killing session %s: %w", id.Session, kerr)
		}
	}

	// Remove the container and its persistent tools volume. These are tolerated
	// failures: a missing container or volume should not block cleaning up the
	// rest.
	if serr := app.Docker().Stop(ctx, id.Container); serr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not remove container %s: %v\n", id.Container, serr)
	}
	if verr := app.Docker().RemoveVolume(ctx, id.ToolsVolume()); verr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not remove volume %s: %v\n", id.ToolsVolume(), verr)
	}

	// Delete the clone and forget the registry entry.
	dir := filepath.Join(app.Env().DataDir, string(agent), string(repo))
	if rerr := os.RemoveAll(dir); rerr != nil {
		return fmt.Errorf("removing %s: %w", dir, rerr)
	}
	reg.Remove(agent, repo)
	if serr := reg.Save(); serr != nil {
		return fmt.Errorf("saving registry: %w", serr)
	}

	// The agent's last repo takes the rest of the agent directory with it: the
	// workspace .claude copy and the agent's private Claude state — the
	// "memories" the confirmation prompt warned about. (Credentials there are a
	// hard link, so the host login is unaffected.)
	if len(reg.ReposForAgent(agent)) == 0 {
		agentDir := filepath.Join(app.Env().DataDir, string(agent))
		if rerr := os.RemoveAll(agentDir); rerr != nil {
			return fmt.Errorf("removing %s: %w", agentDir, rerr)
		}
	}

	fmt.Printf("killed %s for agent %s\n", repo, agent)
	return nil
}
