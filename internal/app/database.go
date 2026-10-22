package app

import (
	"context"
	"fmt"

	"webdock/internal/docker"
	"webdock/internal/instance"
)

func (a *App) cmdDB(ctx context.Context, cfg instance.Config, args []string) error {
	if len(args) != 2 || args[0] != "use" {
		return fmt.Errorf("usage: webdock db use none|mysql|postgres")
	}
	engine := args[1]
	if err := instance.ValidateDBEngine(engine); err != nil {
		return err
	}
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	if cfg.DBEngine() == engine {
		fmt.Fprintf(a.Stdout, "Database is already %s.\n", engine)
		return nil
	}
	oldEngine := cfg.DBEngine()
	compose := a.compose(cfg)
	running, err := a.runningServices(ctx, compose, docker.AllProfiles())
	if err != nil {
		return err
	}
	stackRunning := len(running) != 0
	if stackRunning && oldEngine != "none" {
		if _, err := compose.Output(ctx, []string{oldEngine}, "stop", oldEngine); err != nil {
			return fmt.Errorf("stop %s: %w", oldEngine, err)
		}
	}
	next, err := instance.SetDBEngine(cfg.Root, engine)
	if err != nil {
		if stackRunning && oldEngine != "none" {
			if _, restartErr := compose.Output(ctx, []string{oldEngine}, "up", "-d", "--wait", "--wait-timeout", "300", oldEngine); restartErr != nil {
				fmt.Fprintf(a.Stderr, "webdock: warning: restart previous %s database: %v\n", oldEngine, restartErr)
			}
		}
		return err
	}
	if !stackRunning || engine == "none" {
		fmt.Fprintf(a.Stdout, "Database changed from %s to %s.\n", oldEngine, engine)
		return nil
	}
	if _, err := compose.Output(ctx, []string{engine}, "up", "-d", "--wait", "--wait-timeout", "300", engine); err != nil {
		a.rollbackDatabase(ctx, cfg, oldEngine, engine)
		return fmt.Errorf("start %s: %w", engine, err)
	}
	if err := a.probeService(ctx, next, engine); err != nil {
		a.rollbackDatabase(ctx, cfg, oldEngine, engine)
		return err
	}
	fmt.Fprintf(a.Stdout, "Database changed from %s to %s.\n", oldEngine, engine)
	return nil
}

func (a *App) rollbackDatabase(ctx context.Context, cfg instance.Config, oldEngine, failedEngine string) {
	if failedEngine != "none" {
		if _, err := a.compose(cfg).Output(ctx, []string{failedEngine}, "stop", failedEngine); err != nil {
			fmt.Fprintf(a.Stderr, "webdock: warning: stop failed %s database: %v\n", failedEngine, err)
		}
	}
	if _, err := instance.SetDBEngine(cfg.Root, oldEngine); err != nil {
		fmt.Fprintf(a.Stderr, "webdock: warning: restore database setting: %v\n", err)
		return
	}
	if oldEngine == "none" {
		return
	}
	compose := a.compose(cfg)
	if _, err := compose.Output(ctx, []string{oldEngine}, "up", "-d", "--wait", "--wait-timeout", "300", oldEngine); err != nil {
		fmt.Fprintf(a.Stderr, "webdock: warning: restart previous %s database: %v\n", oldEngine, err)
	}
}
