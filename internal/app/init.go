package app

import (
	"context"
	"fmt"
	"strings"

	"webdock/internal/instance"
)

func (a *App) cmdInit(ctx context.Context, root string, args []string) error {
	dbEngine := "none"
	dir := root
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--db" || arg == "-db":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", arg)
			}
			i++
			dbEngine = args[i]
		case strings.HasPrefix(arg, "--db="):
			dbEngine = strings.TrimPrefix(arg, "--db=")
		case strings.HasPrefix(arg, "-db="):
			dbEngine = strings.TrimPrefix(arg, "-db=")
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown init option %q", arg)
		default:
			if dir != "" {
				return fmt.Errorf("use either -C or init DIR, not both, and only one directory")
			}
			dir = arg
		}
	}
	if dir == "" {
		var err error
		dir, err = a.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	cfg, err := instance.Init(dir, dbEngine)
	if err != nil {
		return err
	}
	if err := a.renderValkeyConfig(ctx, cfg); err != nil {
		return fmt.Errorf("render initial Valkey config: %w", err)
	}
	fmt.Fprintf(a.Stdout, "Initialized Webdock instance at %s\n", cfg.Root)
	fmt.Fprintf(a.Stdout, "Next: cd %q && webdock up\n", cfg.Root)
	return nil
}
