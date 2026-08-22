package handler

import (
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/zoobzio/jack/config"
	"github.com/zoobzio/jack/domain"
)

// resolve fills in an empty agent and/or project from the registry, prompting
// the user to choose when there is more than one option. A single option is
// taken automatically and none is an error. It backs the commands that address
// an existing agent-repo (in, kill).
func resolve(reg *config.Registry, agent domain.Agent, repo domain.Repo) (domain.Agent, domain.Repo, error) {
	agent, err := resolveAgent(reg, agent)
	if err != nil {
		return "", "", err
	}

	if repo == "" {
		repos := reg.ReposForAgent(agent)
		switch len(repos) {
		case 0:
			return "", "", fmt.Errorf("no projects cloned for agent %q", agent)
		case 1:
			repo = repos[0]
		default:
			opts := make([]string, len(repos))
			for i, r := range repos {
				opts[i] = string(r)
			}
			chosen, err := selectOne(fmt.Sprintf("Select a project for %s", agent), opts)
			if err != nil {
				return "", "", fmt.Errorf("selecting project: %w", err)
			}
			repo = domain.Repo(chosen)
		}
	}

	return agent, repo, nil
}

// resolveAgent fills in an empty agent from the registry, prompting the user
// to choose when there is more than one. A single agent is taken automatically
// and none is an error. It backs the commands that address an agent without a
// project (refresh) and the agent half of resolve.
func resolveAgent(reg *config.Registry, agent domain.Agent) (domain.Agent, error) {
	if agent != "" {
		return agent, nil
	}
	agents := reg.Agents()
	switch len(agents) {
	case 0:
		return "", fmt.Errorf("no projects cloned — run jack clone first")
	case 1:
		return agents[0], nil
	default:
		opts := make([]string, len(agents))
		for i, a := range agents {
			opts[i] = string(a)
		}
		chosen, err := selectOne("Select an agent", opts)
		if err != nil {
			return "", fmt.Errorf("selecting agent: %w", err)
		}
		return domain.Agent(chosen), nil
	}
}

// selectOne prompts the user to pick one of options under the given title. It is
// the single-choice list used by every interactive command, so agent, project,
// and confirmation prompts all look and behave the same.
func selectOne(title string, options []string) (string, error) {
	var chosen string
	err := huh.NewSelect[string]().
		Title(title).
		Options(huh.NewOptions(options...)...).
		Value(&chosen).
		Run()
	return chosen, err
}
