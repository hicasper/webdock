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

var componentPorts = map[string][]int{
	instance.ComponentNginx:    {80, 443, 9001},
	instance.ComponentPHP:      {9000},
	instance.ComponentRedis:    {6379},
	instance.ComponentMySQL:    {3306},
	instance.ComponentPostgres: {5432},
}

func (a *App) checkStoppedPortConflicts(cfg instance.Config, running map[string]bool) error {
	if len(running) != 0 || a.Platform != "linux" || a.ReadListeners == nil {
		return nil
	}
	listeners, err := a.ReadListeners()
	if err != nil {
		return fmt.Errorf("read listening sockets: %w", err)
	}
	ports := make([]int, 0, 6)
	for _, component := range cfg.Components {
		if component.Enabled {
			ports = append(ports, componentPorts[component.Name]...)
		}
	}
	if problems := doctor.PortConflicts(listeners, ports); len(problems) != 0 {
		return reportProblems(problems)
	}
	return nil
}

func (a *App) preflight(ctx context.Context, compose docker.Compose) error {
	if _, err := compose.Output(ctx, docker.AllProfiles(), "config", "--quiet"); err != nil {
		return fmt.Errorf("compose validation failed: %w", err)
	}
	return nil
}

func (a *App) renderRuntime(ctx context.Context, cfg instance.Config) ([]byte, error) {
	oldConfig, readErr := os.ReadFile(filepath.Join(cfg.Root, "config", "redis", "valkey.conf"))
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read Redis config: %w", readErr)
	}
	if err := a.renderRedisConfig(ctx, cfg); err != nil {
		return nil, err
	}
	if err := render.Compose(cfg.Root, cfg.ComposeData()); err != nil {
		return nil, err
	}
	return oldConfig, nil
}

func redisConfigChanged(root string, previous []byte) (bool, error) {
	current, err := os.ReadFile(filepath.Join(root, "config", "redis", "valkey.conf"))
	if err != nil {
		return false, fmt.Errorf("read rendered Redis config: %w", err)
	}
	return !bytes.Equal(previous, current), nil
}

func (a *App) renderRedisConfig(ctx context.Context, cfg instance.Config) error {
	gateways, warnings := network.Discover(ctx, a.Runner, cfg.BridgeNetworks(), a.LocalIPs)
	for _, warning := range warnings {
		fmt.Fprintf(a.Stderr, "webdock: warning: %s\n", warning)
	}
	bind := []string{"127.0.0.1"}
	bind = append(bind, gateways...)
	return render.RedisConfig(cfg.Root, render.RedisData{
		Bind:      strings.Join(bind, " "),
		MaxMemory: cfg.Values["REDIS_MAXMEMORY"],
		Password:  cfg.Values["REDIS_PASSWORD"],
	})
}

func (a *App) restoreRedis(ctx context.Context, cfg instance.Config, previous []byte, restart bool) error {
	if len(previous) == 0 {
		if err := a.renderRedisConfig(ctx, cfg); err != nil {
			return err
		}
	} else if err := render.WriteFile(filepath.Join(cfg.Root, "config", "redis", "valkey.conf"), previous, 0o644); err != nil {
		return fmt.Errorf("restore previous Redis config: %w", err)
	}
	if !restart {
		return nil
	}
	_, err := a.compose(cfg).Output(ctx, docker.Profiles([]string{instance.ComponentRedis}), "restart", instance.ComponentRedis)
	return err
}

func (a *App) verifyRuntime(ctx context.Context, cfg instance.Config, required []string) error {
	running, err := a.runningServices(ctx, a.compose(cfg), docker.AllProfiles())
	if err != nil {
		return err
	}
	return a.verifyRunning(ctx, cfg, running, required)
}

func (a *App) verifyRunning(ctx context.Context, cfg instance.Config, running map[string]bool, required []string) error {
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

func (a *App) checkComponentImages(ctx context.Context, components []instance.Component) error {
	for _, component := range components {
		if !component.Enabled {
			continue
		}
		if err := docker.ImageAvailable(ctx, a.Runner, component.Image); err != nil {
			return fmt.Errorf("%s %s: %w", component.Name, component.Version, err)
		}
	}
	return nil
}

func componentByName(cfg instance.Config, name string) (instance.Component, error) {
	component, ok := cfg.Component(name)
	if !ok {
		return instance.Component{}, fmt.Errorf("unknown component %q", name)
	}
	return component, nil
}

func enabledComponents(cfg instance.Config) []instance.Component {
	result := make([]instance.Component, 0, len(cfg.Components))
	for _, component := range cfg.Components {
		if component.Enabled {
			result = append(result, component)
		}
	}
	return result
}

func componentNames(components []instance.Component) []string {
	result := make([]string, 0, len(components))
	for _, component := range components {
		result = append(result, component.Name)
	}
	return result
}

func (a *App) probeService(ctx context.Context, cfg instance.Config, service string) error {
	compose := a.compose(cfg)
	var args []string
	switch service {
	case instance.ComponentNginx:
		args = []string{"exec", "-T", "nginx", "nginx", "-t"}
	case instance.ComponentPHP:
		args = []string{"exec", "-T", "php", "php-fpm", "-t"}
	case instance.ComponentRedis:
		args = []string{"exec", "-T", "redis", "sh", "-c", `REDISCLI_AUTH="$REDIS_PASSWORD" valkey-cli -h 127.0.0.1 -p 6379 --user webdock ping | grep -q PONG`}
	case instance.ComponentMySQL:
		args = []string{"exec", "-T", "mysql", "sh", "-c", `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqladmin ping -h 127.0.0.1 -uroot --silent`}
	case instance.ComponentPostgres:
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
			return true, nil
		}
		switch strings.ToLower(strings.TrimSpace(fields[1])) {
		case "exited", "created", "dead":
			continue
		default:
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
	case "webdock-nginx", "webdock-php", "webdock-redis", "webdock-mysql", "webdock-postgres":
		return true
	default:
		return false
	}
}

func (a *App) listenerRules(cfg instance.Config, running map[string]bool) (map[int]doctor.Rule, error) {
	rules := map[int]doctor.Rule{
		80:   {Name: "Nginx HTTP", Required: running[instance.ComponentNginx], AllowWildcard: true},
		443:  {Name: "Nginx HTTPS", Required: running[instance.ComponentNginx], AllowWildcard: true},
		9000: {Name: "PHP-FPM", Required: running[instance.ComponentPHP]},
		9001: {Name: "Nginx UI", Required: running[instance.ComponentNginx]},
	}
	if running[instance.ComponentRedis] {
		allowed, err := redisBindAddresses(filepath.Join(cfg.Root, "config", "redis", "valkey.conf"))
		if err != nil {
			return nil, err
		}
		rules[6379] = doctor.Rule{Name: "Redis", Required: true, AllowedIPs: allowed}
	}
	if running[instance.ComponentMySQL] {
		rules[3306] = doctor.Rule{Name: "MySQL", Required: true}
		rules[33060] = doctor.Rule{Name: "MySQL X Protocol", Forbidden: true}
	}
	if running[instance.ComponentPostgres] {
		rules[5432] = doctor.Rule{Name: "PostgreSQL", Required: true}
	}
	return rules, nil
}

func redisBindAddresses(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Redis bind addresses: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "bind" {
			return fields[1:], nil
		}
	}
	return nil, fmt.Errorf("redis config has no bind directive")
}

func reportProblems(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(problems, "; "))
}
