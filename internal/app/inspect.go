package app

import (
	"context"
	"fmt"

	"webdock/internal/docker"
	"webdock/internal/instance"
)

func (a *App) cmdStatus(ctx context.Context, cfg instance.Config) error {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "Components:")
	for _, component := range cfg.Components {
		state := "disabled"
		if component.Enabled {
			state = "enabled"
		}
		fmt.Fprintf(a.Stdout, "  %-8s %-8s", component.Name, state)
		if component.Enabled {
			fmt.Fprintf(a.Stdout, " %-8s %s", component.Version, component.Image)
		}
		fmt.Fprintln(a.Stdout)
	}
	out, err := a.compose(cfg).Output(ctx, docker.AllProfiles(), "ps")
	if err != nil {
		return err
	}
	_, err = a.Stdout.Write(out)
	return err
}

func (a *App) cmdLogs(ctx context.Context, cfg instance.Config, args []string) error {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	if len(args) > 1 {
		return fmt.Errorf("logs accepts at most one component")
	}
	command := []string{"logs", "--tail", "200", "--follow"}
	if len(args) == 1 {
		if !validService(args[0]) {
			return fmt.Errorf("unknown component %q", args[0])
		}
		command = append(command, args[0])
	}
	return a.compose(cfg).Attach(ctx, docker.AllProfiles(), a.Stdin, a.Stdout, a.Stderr, command...)
}

func (a *App) cmdShell(ctx context.Context, cfg instance.Config, args []string) error {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: webdock shell <component>")
	}
	if !validService(args[0]) {
		return fmt.Errorf("unknown component %q", args[0])
	}
	return a.compose(cfg).Attach(ctx, docker.AllProfiles(), a.Stdin, a.Stdout, a.Stderr, "exec", args[0], "sh")
}

func (a *App) cmdUI(cfg instance.Config) error {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "Nginx UI: http://127.0.0.1:9001")
	fmt.Fprintln(a.Stdout, "From your workstation: ssh -L 9001:127.0.0.1:9001 <user>@<server>")
	return nil
}

func validService(service string) bool {
	switch service {
	case "nginx", "php", "redis", "mysql", "postgres":
		return true
	default:
		return false
	}
}
