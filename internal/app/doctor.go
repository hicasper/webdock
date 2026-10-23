package app

import (
	"context"
	"fmt"

	"webdock/internal/docker"
	"webdock/internal/instance"
)

func (a *App) cmdDoctor(ctx context.Context, cfg instance.Config) error {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	compose := a.compose(cfg)
	if err := a.preflight(ctx, compose); err != nil {
		return err
	}
	running, err := a.runningServices(ctx, compose, docker.AllProfiles())
	if err != nil {
		return err
	}
	if len(running) == 0 {
		if err := a.checkStoppedPortConflicts(cfg, running); err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, "Doctor found no problems. The stack is stopped.")
		return nil
	}
	if err := a.verifyRunning(ctx, cfg, running, cfg.EnabledComponents()); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "Doctor found no problems.")
	return nil
}
