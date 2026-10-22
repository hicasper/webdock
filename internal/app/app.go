package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"webdock/internal/docker"
	"webdock/internal/doctor"
	"webdock/internal/instance"
	"webdock/internal/network"
)

type App struct {
	Stdout        io.Writer
	Stderr        io.Writer
	Stdin         io.Reader
	Runner        docker.Runner
	Getwd         func() (string, error)
	LocalIPs      network.LocalIPProvider
	ReadListeners func() ([]doctor.Listener, error)
	Platform      string
}

func New() *App {
	return &App{
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		Stdin:         os.Stdin,
		Runner:        docker.ExecRunner{},
		Getwd:         os.Getwd,
		LocalIPs:      nil,
		ReadListeners: doctor.ReadListeners,
		Platform:      runtime.GOOS,
	}
}

func (a *App) Run(ctx context.Context, args []string) int {
	root, args, err := parseGlobalArgs(args)
	if err != nil {
		fmt.Fprintf(a.Stderr, "webdock: %v\n", err)
		return 2
	}
	if len(args) == 0 {
		a.usage()
		return 0
	}
	command := args[0]
	args = args[1:]

	switch command {
	case "help", "-h", "--help":
		a.usage()
		return 0
	case "init":
		err = a.cmdInit(ctx, root, args)
	case "up":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			fs := flag.NewFlagSet("up", flag.ContinueOnError)
			fs.SetOutput(a.Stderr)
			pull := fs.Bool("pull", false, "pull image updates before starting")
			if err := fs.Parse(args); err != nil {
				return err
			}
			if fs.NArg() != 0 {
				return fmt.Errorf("up does not accept positional arguments")
			}
			return a.cmdUp(ctx, cfg, *pull)
		})
	case "down":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			if len(args) != 0 {
				return fmt.Errorf("down does not accept arguments")
			}
			return a.cmdDown(ctx, cfg)
		})
	case "restart":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			if len(args) != 0 {
				return fmt.Errorf("restart does not accept arguments")
			}
			return a.cmdRestart(ctx, cfg)
		})
	case "status":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			if len(args) != 0 {
				return fmt.Errorf("status does not accept arguments")
			}
			return a.cmdStatus(ctx, cfg)
		})
	case "logs":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			return a.cmdLogs(ctx, cfg, args)
		})
	case "doctor":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			if len(args) != 0 {
				return fmt.Errorf("doctor does not accept arguments")
			}
			return a.cmdDoctor(ctx, cfg)
		})
	case "shell":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			return a.cmdShell(ctx, cfg, args)
		})
	case "ui":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			if len(args) != 0 {
				return fmt.Errorf("ui does not accept arguments")
			}
			return a.cmdUI(cfg)
		})
	case "db":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			return a.cmdDB(ctx, cfg, args)
		})
	case "backup":
		err = a.withInstance(ctx, root, func(cfg instance.Config) error {
			return a.cmdBackup(ctx, cfg, args)
		})
	case "restore":
		err = a.cmdRestore(ctx, root, args)
	default:
		err = fmt.Errorf("unknown command %q", command)
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "webdock: %v\n", err)
		return 1
	}
	return 0
}

func (a *App) withInstance(ctx context.Context, root string, fn func(instance.Config) error) error {
	if root == "" {
		var err error
		root, err = a.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	cfg, err := instance.Load(root)
	if err != nil {
		return err
	}
	return fn(cfg)
}

func (a *App) compose(cfg instance.Config) docker.Compose {
	return docker.Compose{Runner: a.Runner, Dir: cfg.Root}
}

func (a *App) usage() {
	fmt.Fprint(a.Stdout, `Usage: webdock [-C DIR] <command> [options]

Commands:
  init [DIR] [--db none|mysql|postgres]  Create an instance
  up [--pull]                            Start or update the stack
  down                                   Stop and remove the stack
  restart                                Restart the stack without pulling
  status                                 Show container status
  logs [service]                         Follow container logs
  doctor                                 Run read-only checks
  shell <service>                        Open a shell in a service
  db use none|mysql|postgres             Change the database engine
  backup [--with-sites] [--with-logs]    Create an offline backup
  restore [--force] <archive>            Restore an offline backup
  ui                                     Show the Nginx UI URL and tunnel command

Global options:
  -C, --dir DIR                          Instance directory (default: current directory)
`)
}

func parseGlobalArgs(args []string) (string, []string, error) {
	var root string
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "-C" || arg == "--dir":
			if len(args) < 2 || args[1] == "" {
				return "", nil, fmt.Errorf("%s requires a directory", arg)
			}
			root = args[1]
			args = args[2:]
		case strings.HasPrefix(arg, "-C="):
			root = strings.TrimPrefix(arg, "-C=")
			if root == "" {
				return "", nil, fmt.Errorf("-C requires a directory")
			}
			args = args[1:]
		case strings.HasPrefix(arg, "--dir="):
			root = strings.TrimPrefix(arg, "--dir=")
			if root == "" {
				return "", nil, fmt.Errorf("--dir requires a directory")
			}
			args = args[1:]
		default:
			return root, args, nil
		}
	}
	return root, args, nil
}
