package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"webdock/internal/docker"
	"webdock/internal/doctor"
	"webdock/internal/instance"
	"webdock/internal/network"
	"webdock/internal/render"
)

func (a *App) checkStoppedPortConflicts(cfg instance.Config, running map[string]bool) error {
	if len(running) != 0 || a.Platform != "linux" || a.ReadListeners == nil {
		return nil
	}
	listeners, err := a.ReadListeners()
	if err != nil {
		return fmt.Errorf("read listening sockets: %w", err)
	}
	ports := []int{80, 443, 9000, 9001, 6379}
	switch cfg.DBEngine() {
	case "mysql":
		ports = append(ports, 3306)
	case "postgres":
		ports = append(ports, 5432)
	}
	if problems := doctor.PortConflicts(listeners, ports); len(problems) != 0 {
		return reportProblems(problems)
	}
	return nil
}

func (a *App) preflight(ctx context.Context, compose docker.Compose) error {
	if _, err := compose.Output(ctx, nil, "config", "--quiet"); err != nil {
		return fmt.Errorf("compose validation failed: %w", err)
	}
	return nil
}

func (a *App) renderRuntime(ctx context.Context, cfg instance.Config) ([]byte, error) {
	oldConfig, readErr := os.ReadFile(filepath.Join(cfg.Root, "config", "valkey", "valkey.conf"))
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read Valkey config: %w", readErr)
	}
	if err := a.renderValkeyConfig(ctx, cfg); err != nil {
		return nil, err
	}
	if err := render.Compose(cfg.Root); err != nil {
		return nil, err
	}
	return oldConfig, nil
}

func valkeyConfigChanged(root string, previous []byte) (bool, error) {
	current, err := os.ReadFile(filepath.Join(root, "config", "valkey", "valkey.conf"))
	if err != nil {
		return false, fmt.Errorf("read rendered Valkey config: %w", err)
	}
	return !bytes.Equal(previous, current), nil
}

func (a *App) renderValkeyConfig(ctx context.Context, cfg instance.Config) error {
	gateways, warnings := network.Discover(ctx, a.Runner, cfg.BridgeNetworks(), a.LocalIPs)
	for _, warning := range warnings {
		fmt.Fprintf(a.Stderr, "webdock: warning: %s\n", warning)
	}
	bind := []string{"127.0.0.1"}
	bind = append(bind, gateways...)
	return render.ValkeyConfig(cfg.Root, render.ValkeyData{
		Bind:      strings.Join(bind, " "),
		MaxMemory: cfg.Values["VALKEY_MAXMEMORY"],
		Password:  cfg.Values["VALKEY_PASSWORD"],
	})
}

func (a *App) restoreValkey(ctx context.Context, cfg instance.Config, previous []byte) error {
	if len(previous) == 0 {
		if err := a.renderValkeyConfig(ctx, cfg); err != nil {
			return err
		}
	} else if err := render.WriteFile(filepath.Join(cfg.Root, "config", "valkey", "valkey.conf"), previous, 0o644); err != nil {
		return fmt.Errorf("restore previous Valkey config: %w", err)
	}
	_, err := a.compose(cfg).Output(ctx, docker.Profiles(cfg.DBEngine()), "restart", "valkey")
	return err
}

func (a *App) verifyRuntime(ctx context.Context, cfg instance.Config, profiles []string) error {
	running, err := a.runningServices(ctx, a.compose(cfg), profiles)
	if err != nil {
		return err
	}
	return a.verifyRunning(ctx, cfg, running)
}

func (a *App) verifyRunning(ctx context.Context, cfg instance.Config, running map[string]bool) error {
	required := []string{"nginx", "php", "valkey"}
	if cfg.DBEngine() != "none" {
		required = append(required, cfg.DBEngine())
	}
	var missing []string
	for _, service := range required {
		if !running[service] {
			missing = append(missing, service)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("required services are not running: %s", strings.Join(missing, ", "))
	}
	for _, service := range required {
		if err := a.probeService(ctx, cfg, service); err != nil {
			return err
		}
	}
	if a.Platform == "linux" && a.ReadListeners != nil {
		listeners, err := a.ReadListeners()
		if err != nil {
			return fmt.Errorf("read listening sockets: %w", err)
		}
		rules, err := a.listenerRules(cfg, running)
		if err != nil {
			return err
		}
		if problems := doctor.ValidateListeners(listeners, rules); len(problems) != 0 {
			return reportProblems(problems)
		}
	}
	return nil
}

func (a *App) probeService(ctx context.Context, cfg instance.Config, service string) error {
	compose := a.compose(cfg)
	var args []string
	switch service {
	case "nginx":
		args = []string{"exec", "-T", "nginx", "nginx", "-t"}
	case "php":
		args = []string{"exec", "-T", "php", "php-fpm", "-t"}
	case "valkey":
		args = []string{"exec", "-T", "valkey", "sh", "-c", `REDISCLI_AUTH="$VALKEY_PASSWORD" valkey-cli -h 127.0.0.1 -p 6379 --user webdock ping | grep -q PONG`}
	case "mysql":
		args = []string{"exec", "-T", "mysql", "sh", "-c", `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqladmin ping -h 127.0.0.1 -uroot --silent`}
	case "postgres":
		args = []string{"exec", "-T", "postgres", "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "webdock"}
	default:
		return fmt.Errorf("unknown service %q", service)
	}
	if _, err := compose.Output(ctx, docker.AllProfiles(), args...); err != nil {
		return fmt.Errorf("%s probe failed: %w", service, err)
	}
	return nil
}

func (a *App) runningServices(ctx context.Context, compose docker.Compose, profiles []string) (map[string]bool, error) {
	out, err := compose.Output(ctx, profiles, "ps", "--status", "running", "--services")
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		service := strings.TrimSpace(scanner.Text())
		if service != "" {
			result[service] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *App) projectContainersRunning(ctx context.Context) (bool, error) {
	out, err := a.Runner.Output(ctx, "", nil, "docker", "ps", "--all", "--format", "{{.Names}}\t{{.State}}")
	if err != nil {
		return false, fmt.Errorf("list running containers: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), "\t", 2)
		name := strings.TrimSpace(fields[0])
		if !isWebdockContainer(name) {
			continue
		}
		if len(fields) == 1 {
			// Be conservative if a Docker formatter omits State unexpectedly.
			return true, nil
		}
		switch strings.ToLower(strings.TrimSpace(fields[1])) {
		case "exited", "created", "dead":
			continue
		default:
			// Treat unknown Docker states conservatively as active.
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("parse running containers: %w", err)
	}
	return false, nil
}

func isWebdockContainer(name string) bool {
	switch name {
	case "webdock-nginx", "webdock-php", "webdock-valkey", "webdock-mysql", "webdock-postgres":
		return true
	default:
		return false
	}
}

func (a *App) listenerRules(cfg instance.Config, running map[string]bool) (map[int]doctor.Rule, error) {
	rules := map[int]doctor.Rule{
		80:   {Name: "Nginx HTTP", Required: running["nginx"], AllowWildcard: true},
		443:  {Name: "Nginx HTTPS", Required: running["nginx"], AllowWildcard: true},
		9000: {Name: "PHP-FPM", Required: running["php"]},
		9001: {Name: "Nginx UI", Required: running["nginx"]},
	}
	if running["valkey"] {
		allowed, err := valkeyBindAddresses(filepath.Join(cfg.Root, "config", "valkey", "valkey.conf"))
		if err != nil {
			return nil, err
		}
		rules[6379] = doctor.Rule{Name: "Valkey", Required: true, AllowedIPs: allowed}
	}
	if running["mysql"] {
		rules[3306] = doctor.Rule{Name: "MySQL", Required: true}
		rules[33060] = doctor.Rule{Name: "MySQL X Protocol", Forbidden: true}
	}
	if running["postgres"] {
		rules[5432] = doctor.Rule{Name: "PostgreSQL", Required: true}
	}
	return rules, nil
}

func valkeyBindAddresses(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Valkey bind addresses: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "bind" {
			return fields[1:], nil
		}
	}
	return nil, fmt.Errorf("valkey config has no bind directive")
}

func reportProblems(problems []string) error {
	return fmt.Errorf("doctor found problems:\n  - %s", strings.Join(problems, "\n  - "))
}
