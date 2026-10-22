package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"webdock/internal/backup"
	"webdock/internal/docker"
	"webdock/internal/instance"
	"webdock/internal/render"
)

func (a *App) cmdBackup(ctx context.Context, cfg instance.Config, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	withSites := fs.Bool("with-sites", false, "include sites")
	withLogs := fs.Bool("with-logs", false, "include logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("backup does not accept positional arguments")
	}
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	compose := a.compose(cfg)
	wasRunning, err := a.projectContainersRunning(ctx)
	if err != nil {
		return err
	}
	if wasRunning {
		if _, err := compose.Output(ctx, docker.AllProfiles(), "stop"); err != nil {
			return fmt.Errorf("stop stack for backup: %w", err)
		}
	}
	destination := filepath.Join(cfg.Root, "backups", backupFilename(cfg.Root, time.Now().UTC()))
	archiveErr := backup.Create(cfg.Root, destination, backup.Options{
		WithSites: *withSites,
		WithLogs:  *withLogs,
	})
	var restartErr error
	if wasRunning {
		restartErr = a.startStack(ctx, cfg)
	}
	if archiveErr != nil && restartErr != nil {
		return fmt.Errorf("backup failed: %v; restart failed: %w", archiveErr, restartErr)
	}
	if archiveErr != nil {
		return archiveErr
	}
	if restartErr != nil {
		return fmt.Errorf("backup written to %s but stack restart failed: %w", destination, restartErr)
	}
	fmt.Fprintf(a.Stdout, "Backup written to %s\n", destination)
	return nil
}

func (a *App) cmdRestore(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	force := fs.Bool("force", false, "replace existing instance data")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: webdock restore [--force] <archive>")
	}
	archive, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("resolve archive path: %w", err)
	}
	if root == "" {
		root, err = a.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve instance directory: %w", err)
	}

	var current instance.Config
	var wasRunning bool
	containersRunning, err := a.projectContainersRunning(ctx)
	if err != nil {
		return err
	}
	if containersRunning {
		cfg, loadErr := instance.Load(root)
		if loadErr != nil {
			return fmt.Errorf("cannot restore while Webdock containers are running; stop them first and repair webdock.env: %w", loadErr)
		}
		current = cfg
		if _, err := os.Stat(filepath.Join(root, "compose.yaml")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("cannot restore while Webdock containers are running; compose.yaml is missing")
			}
			return fmt.Errorf("inspect compose.yaml: %w", err)
		}
		if _, err := a.compose(cfg).Output(ctx, docker.AllProfiles(), "stop"); err != nil {
			return fmt.Errorf("stop stack for restore: %w", err)
		}
		wasRunning = true
		stillRunning, err := a.projectContainersRunning(ctx)
		if err != nil {
			return err
		}
		if stillRunning {
			return fmt.Errorf("cannot restore: Webdock containers are still running after stop")
		}
	}

	validate := func(stage string) error {
		cfg, err := instance.Load(stage)
		if err != nil {
			return err
		}
		if err := render.Compose(stage); err != nil {
			return err
		}
		return instance.ValidateLayout(cfg)
	}
	if err := backup.RestoreValidated(root, archive, *force, validate); err != nil {
		if wasRunning {
			if restartErr := a.startStack(ctx, current); restartErr != nil {
				return fmt.Errorf("restore failed: %v; restart current stack: %w", err, restartErr)
			}
		}
		return err
	}
	cfg, err := instance.Load(root)
	if err != nil {
		return fmt.Errorf("load restored instance: %w", err)
	}
	if _, err := a.renderRuntime(ctx, cfg); err != nil {
		return err
	}
	if err := instance.ValidateLayout(cfg); err != nil {
		return fmt.Errorf("validate restored instance: %w", err)
	}
	if wasRunning {
		if err := a.recreateStack(ctx, cfg); err != nil {
			return fmt.Errorf("restored data but failed to start stack: %w", err)
		}
	}
	fmt.Fprintf(a.Stdout, "Restored Webdock instance at %s\n", cfg.Root)
	if !wasRunning {
		fmt.Fprintln(a.Stdout, "Next: webdock up")
	}
	return nil
}

func backupFilename(root string, now time.Time) string {
	base := "webdock-" + now.Format("20060102-150405") + ".tar.gz"
	if _, err := os.Stat(filepath.Join(root, "backups", base)); errors.Is(err, os.ErrNotExist) {
		return base
	}
	return "webdock-" + now.Format("20060102-150405.000000000") + ".tar.gz"
}
