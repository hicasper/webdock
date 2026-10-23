package app

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"webdock/internal/instance"
	"webdock/internal/render"
)

var versionedComponents = map[string]bool{
	instance.ComponentRedis:    true,
	instance.ComponentMySQL:    true,
	instance.ComponentPostgres: true,
}

func (a *App) checkVersionMarkers(cfg instance.Config, names []string) error {
	for _, name := range names {
		if !versionedComponents[name] {
			continue
		}
		component, ok := cfg.Component(name)
		if !ok || !component.Enabled {
			continue
		}
		path := versionMarkerPath(cfg.Root, name)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s version marker: %w", name, err)
		}
		previous := strings.TrimSpace(string(data))
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

func (a *App) recordVersionMarkers(cfg instance.Config, names []string) error {
	for _, name := range names {
		if !versionedComponents[name] {
			continue
		}
		component, ok := cfg.Component(name)
		if !ok || !component.Enabled {
			continue
		}
		path := versionMarkerPath(cfg.Root, name)
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) == component.Version {
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read %s version marker: %w", name, err)
		}
		if err := render.WriteFile(path, []byte(component.Version+"\n"), 0o644); err != nil {
			return fmt.Errorf("record %s version marker: %w", name, err)
		}
	}
	return nil
}

func versionMarkerPath(root, name string) string {
	return filepath.Join(root, "data", name, ".webdock-version")
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
