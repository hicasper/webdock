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
	runningBefore, err := a.runningServices(ctx, compose, docker.AllProfiles())
	if err != nil {
		return err
	}
	if len(runningBefore) != 0 {
		if _, err := compose.Output(ctx, docker.AllProfiles(), "stop"); err != nil {
			return fmt.Errorf("stop stack for backup: %w", err)
		}
	}
	now := time.Now().UTC()
	destination := filepath.Join(cfg.Root, "backups", backupFilename(cfg.Root, now))
	archiveErr := backup.Create(cfg.Root, destination, backup.Options{
		WithSites: *withSites,
		WithLogs:  *withLogs,
	})
	var restartErr error
	if len(runningBefore) != 0 {
		restartErr = a.startComponents(ctx, cfg, mapKeys(runningBefore), startOptions{forceRecreate: true})
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
	if err := backup.CheckRestoreTarget(root, *force); err != nil {
		return err
	}

	running, err := a.projectContainersRunning(ctx)
	if err != nil {
		return err
	}
	current, currentOK, err := loadExistingConfig(root)
	if err != nil {
		if running {
			return fmt.Errorf("cannot restore while Webdock containers are running; load current instance: %w", err)
		}
		currentOK = false
	}
	var runningBefore map[string]bool
	if currentOK {
		if _, err := os.Stat(filepath.Join(root, "compose.yaml")); err == nil {
			runningBefore, err = a.runningServices(ctx, a.compose(current), docker.AllProfiles())
			if err != nil {
				return err
			}
		} else if running {
			return fmt.Errorf("cannot restore while Webdock containers are running; compose.yaml is missing")
		}
	}
	if runningBefore == nil {
		runningBefore = make(map[string]bool)
	}

	stage, err := backup.PrepareRestore(root, archive, func(stage string) error {
		cfg, err := instance.Load(stage)
		if err != nil {
			return err
		}
		if err := render.Compose(stage, cfg.ComposeData()); err != nil {
			return err
		}
		return instance.ValidateLayout(cfg)
	})
	if err != nil {
		return err
	}
	defer stage.Close()

	stageCfg, err := instance.Load(stage.Root())
	if err != nil {
		return err
	}

	keepCurrent, accepted, err := a.restoreDecisions(current, currentOK, stageCfg)
	if err != nil {
		return err
	}
	if err := a.checkComponentImages(ctx, accepted); err != nil {
		return err
	}

	mergedValues := stageCfg.Values
	if currentOK {
		mergedValues = instance.MergeComponentEnvironment(stageCfg.Values, current.Values, keepCurrent)
	}
	if _, err := instance.ValidateEnvironment(mergedValues); err != nil {
		return fmt.Errorf("merged restore environment is invalid: %w", err)
	}
	mergedVersions, err := mergedRestoreVersionState(root, currentOK, stage.Root(), accepted)
	if err != nil {
		return err
	}

	replace := make([]string, 0, 12)
	for _, component := range accepted {
		replace = append(replace, instance.ComponentPaths(component.Name)...)
	}
	for _, name := range []string{"sites", "logs"} {
		if _, err := os.Lstat(filepath.Join(stage.Root(), name)); err == nil {
			replace = append(replace, name)
		}
	}
	plan := backup.RestorePlan{
		Env:      instance.RenderEnv(mergedValues),
		Versions: instance.RenderVersionState(mergedVersions),
		Replace:  replace,
	}

	if len(runningBefore) != 0 {
		if _, err := a.compose(current).Output(ctx, docker.AllProfiles(), "stop"); err != nil {
			return fmt.Errorf("stop stack for restore: %w", err)
		}
		stillRunning, err := a.projectContainersRunning(ctx)
		if err != nil {
			return err
		}
		if stillRunning {
			return fmt.Errorf("cannot restore: Webdock containers are still running after stop")
		}
	}
	if err := stage.Apply(root, *force, plan); err != nil {
		if len(runningBefore) != 0 {
			if restartErr := a.startComponents(ctx, current, mapKeys(runningBefore), startOptions{forceRecreate: true}); restartErr != nil {
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
	restartNames := make([]string, 0, len(runningBefore))
	for _, name := range instance.ComponentNames() {
		component, ok := cfg.Component(name)
		if runningBefore[name] && ok && component.Enabled {
			restartNames = append(restartNames, name)
		}
	}
	if len(restartNames) != 0 {
		if err := a.startComponents(ctx, cfg, restartNames, startOptions{forceRecreate: true}); err != nil {
			return fmt.Errorf("restored data but failed to start stack: %w", err)
		}
		if err := a.recordDataVersions(cfg, restartNames); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.Stdout, "Restored Webdock instance at %s\n", cfg.Root)
	if len(runningBefore) == 0 {
		fmt.Fprintln(a.Stdout, "Next: webdock up")
	}
	return nil
}

func (a *App) restoreDecisions(current instance.Config, currentOK bool, archived instance.Config) (keep []string, accepted []instance.Component, err error) {
	for _, name := range instance.ComponentNames() {
		archivedComponent, ok := archived.Component(name)
		if !ok {
			return nil, nil, fmt.Errorf("archive is missing component %s", name)
		}
		if !archivedComponent.Enabled {
			if currentOK {
				keep = append(keep, name)
			}
			continue
		}
		if !currentOK {
			accepted = append(accepted, archivedComponent)
			continue
		}
		currentComponent, _ := current.Component(name)
		if currentComponent.Enabled && currentComponent.Version == archivedComponent.Version {
			accepted = append(accepted, archivedComponent)
			continue
		}
		display := "false"
		if currentComponent.Enabled {
			display = currentComponent.Version
		}
		overwrite, err := a.confirm(fmt.Sprintf(
			"%s: backup uses %s, current uses %s. Restore %s data and change version? [y/N] ",
			name, archivedComponent.Version, display, archivedComponent.Version,
		))
		if err != nil {
			return nil, nil, err
		}
		if overwrite {
			accepted = append(accepted, archivedComponent)
		} else {
			keep = append(keep, name)
		}
	}
	return keep, accepted, nil
}

func mergedRestoreVersionState(root string, currentOK bool, stage string, accepted []instance.Component) (instance.Values, error) {
	merged := make(instance.Values)
	if currentOK {
		current, err := instance.LoadVersionState(root)
		if err != nil {
			return nil, err
		}
		for key, version := range current {
			merged[key] = version
		}
	}
	archived, err := instance.LoadVersionState(stage)
	if err != nil {
		return nil, err
	}
	for _, component := range accepted {
		key, ok := instance.VersionStateKey(component.Name)
		if !ok {
			continue
		}
		if version, ok := archived[key]; ok {
			merged[key] = version
		} else {
			delete(merged, key)
		}
	}
	return merged, nil
}

func loadExistingConfig(root string) (instance.Config, bool, error) {
	envPath := filepath.Join(root, "webdock.env")
	if _, err := os.Lstat(envPath); errors.Is(err, os.ErrNotExist) {
		return instance.Config{}, false, nil
	} else if err != nil {
		return instance.Config{}, false, err
	}
	cfg, err := instance.Load(root)
	if err != nil {
		return instance.Config{}, false, err
	}
	return cfg, true, nil
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for _, name := range instance.ComponentNames() {
		if values[name] {
			result = append(result, name)
		}
	}
	return result
}

func backupFilename(root string, now time.Time) string {
	base := "webdock-" + now.Format("20060102-150405") + ".tar.gz"
	if _, err := os.Stat(filepath.Join(root, "backups", base)); errors.Is(err, os.ErrNotExist) {
		return base
	}
	return "webdock-" + now.Format("20060102-150405.000000000") + ".tar.gz"
}
