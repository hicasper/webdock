package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"webdock/internal/docker"
	"webdock/internal/instance"
)

func (a *App) cmdUp(ctx context.Context, cfg instance.Config, pull bool) (err error) {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	compose := a.compose(cfg)
	enabled := enabledComponents(cfg)
	names := componentNames(enabled)
	if err := a.checkComponentImages(ctx, enabled); err != nil {
		return err
	}

	runningBefore, err := a.runningServices(ctx, compose, docker.AllProfiles())
	if err != nil {
		return err
	}
	if err = a.checkStoppedPortConflicts(cfg, runningBefore); err != nil {
		return err
	}
	var previousRedis []byte
	if len(names) != 0 {
		previousRedis, err = a.prepareStart(ctx, cfg, names)
		if err != nil {
			return err
		}
	}

	var disabledRunning []string
	for name := range runningBefore {
		component, ok := cfg.Component(name)
		if ok && !component.Enabled {
			disabledRunning = append(disabledRunning, name)
		}
	}
	if len(disabledRunning) != 0 {
		if _, err = compose.Output(ctx, docker.AllProfiles(), append([]string{"stop"}, disabledRunning...)...); err != nil {
			return fmt.Errorf("stop disabled components: %w", err)
		}
	}

	if len(names) != 0 {
		if err = a.executePreparedStart(ctx, cfg, names, previousRedis, startOptions{pull: pull, restartRedis: runningBefore[instance.ComponentRedis]}); err != nil {
			return err
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(a.Stdout, "No components are enabled; disabled running components were stopped.")
	} else {
		fmt.Fprintln(a.Stdout, "Webdock is up.")
	}
	return nil
}

func (a *App) cmdRestart(ctx context.Context, cfg instance.Config, args []string) (err error) {
	if len(args) > 1 {
		return fmt.Errorf("usage: webdock restart [component]")
	}
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	compose := a.compose(cfg)
	runningBefore, err := a.runningServices(ctx, compose, docker.AllProfiles())
	if err != nil {
		return err
	}
	var names []string
	if len(args) == 1 {
		component, err := componentByName(cfg, args[0])
		if err != nil {
			return err
		}
		if !component.Enabled {
			return fmt.Errorf("%s is disabled; run webdock enable %s first", component.Name, component.Name)
		}
		if err := requirePHPFrontend(component.Name, runningBefore); err != nil {
			return err
		}
		names = []string{component.Name}
	} else {
		for _, component := range cfg.Components {
			if component.Enabled && runningBefore[component.Name] {
				names = append(names, component.Name)
			}
		}
		if slices.Contains(names, instance.ComponentPHP) {
			if err := requirePHPFrontend(instance.ComponentPHP, runningBefore); err != nil {
				return err
			}
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(a.Stdout, "No running enabled components to restart.")
		return nil
	}
	components := make([]instance.Component, 0, len(names))
	for _, name := range names {
		component, _ := cfg.Component(name)
		components = append(components, component)
	}
	if err := a.checkComponentImages(ctx, components); err != nil {
		return err
	}
	previousRedis, err := a.prepareStart(ctx, cfg, names)
	if err != nil {
		return err
	}
	if err = a.executePreparedStart(ctx, cfg, names, previousRedis, startOptions{forceRecreate: true, restartRedis: runningBefore[instance.ComponentRedis]}); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "Webdock restarted.")
	return nil
}

func (a *App) cmdDown(ctx context.Context, cfg instance.Config) error {
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	if _, err := a.compose(cfg).Output(ctx, docker.AllProfiles(), "down", "--remove-orphans"); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "Webdock is down.")
	return nil
}

func (a *App) cmdStart(ctx context.Context, cfg instance.Config, args []string) (err error) {
	if len(args) != 1 {
		return fmt.Errorf("usage: webdock start <component>")
	}
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	component, err := componentByName(cfg, args[0])
	if err != nil {
		return err
	}
	if !component.Enabled {
		return fmt.Errorf("%s is disabled; run webdock enable %s first", component.Name, component.Name)
	}
	if component.Name == instance.ComponentPHP {
		running, err := a.runningServices(ctx, a.compose(cfg), docker.AllProfiles())
		if err != nil {
			return err
		}
		if err := requirePHPFrontend(component.Name, running); err != nil {
			return err
		}
	}
	if err = a.checkComponentImages(ctx, []instance.Component{component}); err != nil {
		return err
	}
	previousRedis, err := a.prepareStart(ctx, cfg, []string{component.Name})
	if err != nil {
		return err
	}
	if err = a.executePreparedStart(ctx, cfg, []string{component.Name}, previousRedis, startOptions{}); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "%s is up.\n", component.Name)
	return nil
}

func (a *App) cmdStop(ctx context.Context, cfg instance.Config, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: webdock stop <component>")
	}
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	component, err := componentByName(cfg, args[0])
	if err != nil {
		return err
	}
	if _, err := a.compose(cfg).Output(ctx, docker.AllProfiles(), "stop", component.Name); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "%s is stopped.\n", component.Name)
	return nil
}

func (a *App) cmdEnable(ctx context.Context, cfg instance.Config, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: webdock enable <component> [version]")
	}
	name := args[0]
	setting := "true"
	if len(args) == 2 {
		if strings.EqualFold(strings.TrimSpace(args[1]), "false") {
			return fmt.Errorf("enable requires a version or true; use webdock disable %s", name)
		}
		setting = args[1]
	}
	component, err := instance.ResolveComponent(name, setting)
	if err != nil {
		return err
	}
	if name == instance.ComponentPHP {
		nginx, ok := cfg.Component(instance.ComponentNginx)
		if !ok || !nginx.Enabled {
			return fmt.Errorf("php requires nginx to be enabled; run webdock enable nginx first")
		}
	}
	if err := a.checkComponentImages(ctx, []instance.Component{component}); err != nil {
		return err
	}
	if _, err := instance.SetComponentState(cfg.Root, name, setting); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Enabled %s %s. Run webdock up to apply.\n", name, component.Version)
	return nil
}

func (a *App) cmdDisable(cfg instance.Config, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: webdock disable <component>")
	}
	if _, err := instance.SetComponentState(cfg.Root, args[0], "false"); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Disabled %s. Run webdock up to apply.\n", args[0])
	return nil
}

type startOptions struct {
	forceRecreate bool
	pull          bool
	restartRedis  bool
}

func (a *App) prepareStart(ctx context.Context, cfg instance.Config, names []string) ([]byte, error) {
	oldRedis, err := a.renderRuntime(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := a.checkDataVersions(cfg, names); err != nil {
		return nil, err
	}
	if err := a.preflight(ctx, a.compose(cfg)); err != nil {
		return nil, err
	}
	return oldRedis, nil
}

func (a *App) executePreparedStart(ctx context.Context, cfg instance.Config, names []string, previousRedis []byte, options startOptions) (err error) {
	startAttempted := false
	defer func() {
		if err == nil || !startAttempted {
			return
		}
		if restoreErr := a.restoreRedis(ctx, cfg, previousRedis, options.restartRedis); restoreErr != nil {
			fmt.Fprintf(a.Stderr, "webdock: warning: restore previous Redis config: %v\n", restoreErr)
		}
	}()

	startAttempted = true
	if err = a.startComponents(ctx, cfg, names, options); err != nil {
		return err
	}
	redisChanged, err := redisConfigChanged(cfg.Root, previousRedis)
	if err != nil {
		return err
	}
	if redisChanged && options.restartRedis && slices.Contains(names, instance.ComponentRedis) {
		if _, err = a.compose(cfg).Output(ctx, docker.Profiles([]string{instance.ComponentRedis}), "restart", instance.ComponentRedis); err != nil {
			return err
		}
		if err = a.verifyRuntime(ctx, cfg, names); err != nil {
			return err
		}
	}
	return a.recordDataVersions(cfg, names)
}

func (a *App) startComponents(ctx context.Context, cfg instance.Config, names []string, options startOptions) error {
	if len(names) == 0 {
		return nil
	}
	profiles := docker.Profiles(names)
	upArgs := []string{"up", "-d", "--wait", "--wait-timeout", "300"}
	if options.pull {
		upArgs = append(upArgs, "--pull", "always")
	}
	if options.forceRecreate {
		upArgs = append(upArgs, "--force-recreate")
	}
	upArgs = append(upArgs, names...)
	if _, err := a.compose(cfg).Output(ctx, profiles, upArgs...); err != nil {
		return err
	}
	for _, name := range names {
		if !validService(name) {
			return fmt.Errorf("unknown component %q", name)
		}
	}
	return a.verifyRuntime(ctx, cfg, names)
}
