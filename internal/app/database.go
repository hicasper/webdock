package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"webdock/internal/docker"
	"webdock/internal/instance"
)

func (a *App) cmdDB(ctx context.Context, cfg instance.Config, args []string) error {
	if len(args) != 2 || args[0] != "backup" {
		return fmt.Errorf("usage: webdock db backup mysql|postgres")
	}
	engine := args[1]
	if engine != instance.ComponentMySQL && engine != instance.ComponentPostgres {
		return fmt.Errorf("database backup supports mysql or postgres, not %q", engine)
	}
	if err := instance.ValidateLayout(cfg); err != nil {
		return err
	}
	component, ok := cfg.Component(engine)
	if !ok || !component.Enabled {
		return fmt.Errorf("%s is disabled; run webdock enable %s and webdock up first", engine, engine)
	}
	running, err := a.runningServices(ctx, a.compose(cfg), docker.AllProfiles())
	if err != nil {
		return err
	}
	if !running[engine] {
		return fmt.Errorf("%s is not running; run webdock start %s first", engine, engine)
	}
	destination, err := a.databaseBackupPath(cfg.Root, engine, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := a.writeDatabaseBackup(ctx, cfg, engine, destination); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Database backup written to %s\n", destination)
	return nil
}

func (a *App) databaseBackupPath(root, engine string, now time.Time) (string, error) {
	dir := filepath.Join(root, "backups", "db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create database backup directory: %w", err)
	}
	extension := ".dump"
	if engine == instance.ComponentMySQL {
		extension = ".sql.gz"
	}
	base := "webdock-" + engine + "-" + now.Format("20060102-150405.000000000") + extension
	return filepath.Join(dir, base), nil
}

func (a *App) writeDatabaseBackup(ctx context.Context, cfg instance.Config, engine, destination string) error {
	tmp, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary database backup: %w", err)
	}
	tmpName := tmp.Name()
	complete := false
	defer func() {
		_ = tmp.Close()
		if !complete {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temporary database backup: %w", err)
	}

	var stdout io.Writer = tmp
	var gz *gzip.Writer
	if engine == instance.ComponentMySQL {
		gz = gzip.NewWriter(tmp)
		stdout = gz
	}
	var stderr bytes.Buffer
	command := databaseDumpCommand(engine)
	if err := a.compose(cfg).Attach(ctx, []string{engine}, nil, stdout, &stderr, command...); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return fmt.Errorf("dump %s: %w", engine, err)
		}
		return fmt.Errorf("dump %s: %w: %s", engine, err, message)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return fmt.Errorf("finish MySQL backup compression: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync database backup: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close database backup: %w", err)
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return fmt.Errorf("install database backup: %w", err)
	}
	complete = true
	return nil
}

func databaseDumpCommand(engine string) []string {
	switch engine {
	case instance.ComponentMySQL:
		return []string{
			"exec", "-T", "mysql", "sh", "-c",
			`MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqldump --single-transaction --quick --routines --events --triggers --set-gtid-purged=OFF --default-character-set=utf8mb4 --databases webdock -h 127.0.0.1 -uroot`,
		}
	case instance.ComponentPostgres:
		return []string{
			"exec", "-T", "postgres", "sh", "-c",
			`PGPASSWORD="$POSTGRES_PASSWORD" exec pg_dump -h 127.0.0.1 -U postgres -d webdock --format=custom`,
		}
	default:
		return nil
	}
}
