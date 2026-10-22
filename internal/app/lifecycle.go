package app

import (
	"context"
	"fmt"

	"webdock/internal/docker"
	"webdock/internal/instance"
)

func (a *App) cmdUp(ctx context.Context, cfg instance.Config, pull bool) (err error) {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	compose := a.compose(cfg)
	profiles := docker.Profiles(cfg.DBEngine())

	oldValkey, err := a.renderRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	upAttempted := false
	defer func() {
		if err == nil || !upAttempted {
			return
		}
		if restoreErr := a.restoreValkey(ctx, cfg, oldValkey); restoreErr != nil {
			fmt.Fprintf(a.Stderr, "webdock: warning: restore previous Valkey config: %v\n", restoreErr)
		}
	}()

	runningBefore, err := a.runningServices(ctx, compose, docker.AllProfiles())
	if err != nil {
		return err
	}
	if err = a.checkStoppedPortConflicts(cfg, runningBefore); err != nil {
		return err
	}
	valkeyChanged, err := valkeyConfigChanged(cfg.Root, oldValkey)
	if err != nil {
		return err
	}
	if err = a.preflight(ctx, compose); err != nil {
		return err
	}
	upArgs := []string{"up", "-d", "--wait", "--wait-timeout", "300"}
	if pull {
		upArgs = append(upArgs, "--pull", "always")
	}
	upAttempted = true
	if _, err = compose.Output(ctx, profiles, upArgs...); err != nil {
		return err
	}
	if valkeyChanged && runningBefore["valkey"] {
		if _, err = compose.Output(ctx, profiles, "restart", "valkey"); err != nil {
			return err
		}
		if _, err = compose.Output(ctx, profiles, "up", "-d", "--wait", "--wait-timeout", "300"); err != nil {
			return err
		}
	}
	if err = a.verifyRuntime(ctx, cfg, profiles); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "Webdock is up.")
	return nil
}

func (a *App) cmdRestart(ctx context.Context, cfg instance.Config) (err error) {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	compose := a.compose(cfg)
	profiles := docker.Profiles(cfg.DBEngine())
	oldValkey, err := a.renderRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	restartAttempted := false
	defer func() {
		if err == nil || !restartAttempted {
			return
		}
		if restoreErr := a.restoreValkey(ctx, cfg, oldValkey); restoreErr != nil {
			fmt.Fprintf(a.Stderr, "webdock: warning: restore previous Valkey config: %v\n", restoreErr)
		}
	}()
	if err = a.preflight(ctx, compose); err != nil {
		return err
	}
	restartAttempted = true
	if _, err = compose.Output(ctx, profiles, "restart"); err != nil {
		return err
	}
	if _, err = compose.Output(ctx, profiles, "up", "-d", "--wait", "--wait-timeout", "300"); err != nil {
		return err
	}
	if err = a.verifyRuntime(ctx, cfg, profiles); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "Webdock restarted.")
	return nil
}

func (a *App) cmdDown(ctx context.Context, cfg instance.Config) error {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	_, err := a.compose(cfg).Output(ctx, docker.AllProfiles(), "down", "--remove-orphans")
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "Webdock is down.")
	return nil
}

func (a *App) startStack(ctx context.Context, cfg instance.Config) error {
	profiles := docker.Profiles(cfg.DBEngine())
	if _, err := a.compose(cfg).Output(ctx, profiles, "up", "-d", "--wait", "--wait-timeout", "300"); err != nil {
		return err
	}
	return a.verifyRuntime(ctx, cfg, profiles)
}

func (a *App) recreateStack(ctx context.Context, cfg instance.Config) error {
	profiles := docker.Profiles(cfg.DBEngine())
	if _, err := a.compose(cfg).Output(ctx, profiles, "up", "-d", "--force-recreate", "--wait"); err != nil {
		return err
	}
	return a.verifyRuntime(ctx, cfg, profiles)
}
