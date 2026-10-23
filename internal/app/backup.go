package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	manifest := backupManifest(cfg, a.Version, now)
	archiveErr := backup.Create(cfg.Root, destination, backup.Options{
		WithSites: *withSites,
		WithLogs:  *withLogs,
	}, manifest)
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

func backupManifest(cfg instance.Config, version string, now time.Time) backup.Manifest {
	components := make(map[string]*string, len(cfg.Components))
	for _, component := range cfg.Components {
		if !component.Enabled {
			components[component.Name] = nil
			continue
		}
		value := component.Version
		components[component.Name] = &value
	}
	return backup.Manifest{
		CreatedAt:      now.Format(time.RFC3339),
		WebdockVersion: version,
		Components:     components,
	}
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

	manifest, err := backup.LoadManifest(stage.Root())
	if err != nil {
		return err
	}
	if err := manifest.Validate(instance.ComponentNames()); err != nil {
		return err
	}
	stageCfg, err := instance.Load(stage.Root())
	if err != nil {
		return err
	}

	keepCurrent, accepted, err := a.restoreDecisions(current, currentOK, manifest)
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
	for _, name := range instance.ComponentNames() {
		if manifestVersion(manifest, name) != "" {
			continue
		}
		key, ok := instance.ComponentEnvKey(name)
		if !ok {
			return fmt.Errorf("component %s has no environment key", name)
		}
		mergedValues[key] = "false"
	}
	for _, component := range accepted {
		key, ok := instance.ComponentEnvKey(component.Name)
		if !ok {
			return fmt.Errorf("component %s has no environment key", component.Name)
		}
		mergedValues[key] = component.Version
	}
	if _, err := instance.ValidateEnvironment(mergedValues); err != nil {
		return fmt.Errorf("merged restore environment is invalid: %w", err)
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
		Env:     instance.RenderEnv(mergedValues),
		Replace: replace,
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
		if err := a.recordVersionMarkers(cfg, restartNames); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.Stdout, "Restored Webdock instance at %s\n", cfg.Root)
	if len(runningBefore) == 0 {
		fmt.Fprintln(a.Stdout, "Next: webdock up")
	}
	return nil
}

func (a *App) restoreDecisions(current instance.Config, currentOK bool, manifest backup.Manifest) (keep []string, accepted []instance.Component, err error) {
	for _, name := range instance.ComponentNames() {
		version := manifestVersion(manifest, name)
		if version == "" {
			if currentOK {
				keep = append(keep, name)
			}
			continue
		}
		if strings.EqualFold(version, "true") || strings.EqualFold(version, "false") {
			return nil, nil, fmt.Errorf("invalid manifest version for %s", name)
		}
		archived, err := instance.ResolveComponent(name, version)
		if err != nil || !archived.Enabled {
			return nil, nil, fmt.Errorf("invalid manifest version for %s", name)
		}
		if !currentOK {
			accepted = append(accepted, archived)
			continue
		}
		currentComponent, _ := current.Component(name)
		if currentComponent.Enabled && currentComponent.Version == archived.Version {
			accepted = append(accepted, archived)
			continue
		}
		display := "false"
		if currentComponent.Enabled {
			display = currentComponent.Version
		}
		overwrite, err := a.confirm(fmt.Sprintf(
			"%s: backup uses %s, current uses %s. Restore %s data and change version? [y/N] ",
			name, archived.Version, display, archived.Version,
		))
		if err != nil {
			return nil, nil, err
		}
		if overwrite {
			accepted = append(accepted, archived)
		} else {
			keep = append(keep, name)
		}
	}
	return keep, accepted, nil
}

func manifestVersion(manifest backup.Manifest, name string) string {
	value := manifest.Components[name]
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
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
