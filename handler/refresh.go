package handler

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/zoobzio/jack/config"
	"github.com/zoobzio/jack/core"
	"github.com/zoobzio/jack/domain"
)

// Refresh builds the `jack refresh` command and mounts it onto the app's root
// command.
func Refresh(app *core.App) {
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Sync an agent's config, secrets, and Claude credentials from the host",
		Long: "Bring an agent back in step with the host: re-apply its config (agents/<name>/ into its workspace .claude), " +
			"refresh the account keys in its claude.json, re-render its secrets into the session env, " +
			"and re-link its Claude credentials from the host login.\n" +
			"Everything is updated in place, so a running container sees the result through its existing mounts — " +
			"this is the recovery path when an agent's login has expired inside its container but not on the host, " +
			"and the way to rotate a secret (e.g. GH_TOKEN) without recreating the container.\n" +
			"Config and credentials land immediately; secrets land on the next claude launch, since a running process keeps the env it started with.\n" +
			"The agent's memory (history, project state) is untouched.\nWith no flags, interactively select the agent.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agent, _ := cmd.Flags().GetString("agent")
			return refresh(app, domain.Agent(agent))
		},
	}
	cmd.Flags().StringP("agent", "a", "", "agent name")
	app.Root().AddCommand(cmd)
}

// refresh re-syncs an agent's config, secrets, and Claude login from the host.
// An empty agent is resolved from the registry, interactively when there is
// more than one choice. Config, account keys, session env, and credentials are
// each replaced in place within paths a running container has mounted, so no
// container rebuild is needed: config and credentials land immediately, and
// the session env is sourced at claude launch, so a rotated secret lands the
// next time claude starts.
func refresh(app *core.App, agent domain.Agent) error {
	reg, err := config.NewRegistry(app.Env().RegistryPath)
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	agent, err = resolveAgent(reg, agent)
	if err != nil {
		return err
	}

	if aerr := app.Env().ApplyAgent(agent); aerr != nil {
		return fmt.Errorf("applying config for agent %s: %w", agent, aerr)
	}
	fmt.Printf("applied config for agent %s\n", agent)

	if jerr := app.Env().ReseedClaudeJSON(agent); jerr != nil {
		return fmt.Errorf("refreshing claude.json for agent %s: %w", agent, jerr)
	}
	fmt.Printf("refreshed account keys for agent %s\n", agent)

	// Re-render the session env from the agent's secrets file. The container
	// sources it at claude launch, so the swap lands on the next launch — a
	// running claude keeps the env it started with.
	if serr := app.Env().WriteSessionEnv(agent); serr != nil {
		return fmt.Errorf("rendering session env for agent %s: %w", agent, serr)
	}
	fmt.Printf("rendered session env for agent %s (a running session keeps its env until claude relaunches)\n", agent)

	// A host without a credentials file (e.g. macOS, where the login lives in
	// the keychain) has nothing to re-link; the config half of the refresh
	// still applied, so report the skip rather than failing.
	if cerr := app.Env().ReseedClaudeCredentials(agent); cerr != nil {
		if errors.Is(cerr, fs.ErrNotExist) {
			fmt.Println("no host credentials file to reseed from (host login may live in the keychain); skipped")
			return nil
		}
		return fmt.Errorf("reseeding credentials for agent %s: %w", agent, cerr)
	}
	fmt.Printf("reseeded Claude credentials for agent %s\n", agent)

	return nil
}
