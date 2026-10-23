package app

import (
	"bufio"
	"fmt"
	"strings"

	"webdock/internal/instance"
)

func (a *App) checkDataVersions(cfg instance.Config, names []string) error {
	versions, err := instance.LoadVersionState(cfg.Root)
	if err != nil {
		return err
	}
	for _, name := range names {
		key, ok := instance.VersionStateKey(name)
		if !ok {
			continue
		}
		component, ok := cfg.Component(name)
		if !ok || !component.Enabled {
			continue
		}
		previous := strings.TrimSpace(versions[key])
		if previous == "" || previous == component.Version {
			continue
		}
		ok, err = a.confirm(fmt.Sprintf(
			"%s: data was initialized with %s, configuration now uses %s. Continue anyway? [y/N] ",
			name, previous, component.Version,
		))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("startup cancelled for %s", name)
		}
	}
	return nil
}

func (a *App) recordDataVersions(cfg instance.Config, names []string) error {
	versions, err := instance.LoadVersionState(cfg.Root)
	if err != nil {
		return err
	}
	changed := false
	for _, name := range names {
		key, ok := instance.VersionStateKey(name)
		if !ok {
			continue
		}
		component, ok := cfg.Component(name)
		if !ok || !component.Enabled {
			continue
		}
		if strings.TrimSpace(versions[key]) == component.Version {
			continue
		}
		versions[key] = component.Version
		changed = true
	}
	if !changed {
		return nil
	}
	if err := instance.WriteVersionState(cfg.Root, versions); err != nil {
		return fmt.Errorf("record data version state: %w", err)
	}
	return nil
}

func (a *App) confirm(prompt string) (bool, error) {
	if a.promptReader == nil {
		a.promptReader = bufio.NewReader(a.Stdin)
	}
	for {
		fmt.Fprint(a.Stderr, prompt)
		line, err := a.promptReader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return false, fmt.Errorf("read confirmation: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "", "n", "no":
			return false, nil
		}
	}
}

func requirePHPFrontend(name string, running map[string]bool) error {
	if name == instance.ComponentPHP && !running[instance.ComponentNginx] {
		return fmt.Errorf("php requires nginx to be running; start nginx first")
	}
	return nil
}
