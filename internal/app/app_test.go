package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"webdock/internal/backup"
	"webdock/internal/doctor"
	"webdock/internal/instance"
)

type fakeRunner struct {
	handle func(name string, args []string) ([]byte, error)
	attach func(name string, args []string) error
	calls  []string
}

func (f *fakeRunner) Output(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	return f.handle(name, args)
}

func (f *fakeRunner) Attach(_ context.Context, _ string, _ []string, _ io.Reader, _ io.Writer, _ io.Writer, name string, args ...string) error {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if f.attach != nil {
		return f.attach(name, args)
	}
	return nil
}

func TestRunInitWithGlobalDirectory(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		if name == "docker" && len(args) >= 3 && args[0] == "network" && args[1] == "inspect" {
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		}
		return nil, nil
	}})
	code := a.Run(context.Background(), []string{"-C=" + root, "init", "--db", "postgres"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Initialized Webdock instance") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	cfg, err := instance.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBEngine() != "postgres" {
		t.Fatalf("DB engine = %q", cfg.DBEngine())
	}
}

func TestRunVersion(t *testing.T) {
	for _, arg := range []string{"version", "-v", "--version"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			a := testApp(&stdout, &stderr, &fakeRunner{})
			a.Version = "0.1.0"

			code := a.Run(context.Background(), []string{arg})
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
			}
			if got, want := stdout.String(), "webdock 0.1.0\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestUpRendersBridgeAndVerifiesServices(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`), nil
		case strings.Contains(call, " config --quiet"):
			return nil, nil
		case strings.Contains(call, " up -d --wait"):
			return nil, nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("edge\nphp\nvalkey\n"), nil
		case strings.Contains(call, " exec -T "):
			return nil, nil
		case strings.Contains(call, " restart valkey"):
			return nil, nil
		default:
			return nil, errors.New("unexpected command: " + call)
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "up"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Webdock is up") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	valkey, err := os.ReadFile(filepath.Join(root, "config", "valkey", "valkey.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(valkey), "bind 127.0.0.1 172.17.0.1") {
		t.Fatalf("Valkey config = %s", valkey)
	}
	var sawUp, sawEdgeProbe, sawPHPProbe, sawValkeyProbe, sawValkeyUser, sawValkeyRestart bool
	for _, call := range runner.calls {
		sawUp = sawUp || strings.Contains(call, " up -d --wait")
		sawEdgeProbe = sawEdgeProbe || strings.Contains(call, "exec -T edge nginx -t")
		sawPHPProbe = sawPHPProbe || strings.Contains(call, "exec -T php php-fpm -t")
		sawValkeyProbe = sawValkeyProbe || strings.Contains(call, "exec -T valkey")
		sawValkeyUser = sawValkeyUser || strings.Contains(call, "--user webdock")
		sawValkeyRestart = sawValkeyRestart || strings.Contains(call, " restart valkey")
	}
	if !sawUp || !sawEdgeProbe || !sawPHPProbe || !sawValkeyProbe || !sawValkeyUser || !sawValkeyRestart {
		t.Fatalf("missing lifecycle calls: %v", runner.calls)
	}
}

func TestUpPullUsesPullPolicy(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("edge\nphp\nvalkey\n"), nil
		case strings.Contains(call, " exec -T "):
			return nil, nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "up", "--pull"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	found := false
	for _, call := range runner.calls {
		if strings.Contains(call, "up -d --wait --wait-timeout 300 --pull always") {
			found = true
		}
	}
	if !found {
		t.Fatalf("pull policy missing: %v", runner.calls)
	}
}

func TestUpFailureRestoresPreviousValkeyConfig(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	valkeyPath := filepath.Join(root, "config", "valkey", "valkey.conf")
	before, err := os.ReadFile(valkeyPath)
	if err != nil {
		t.Fatal(err)
	}
	networkCalls := 0
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case call == "docker network inspect bridge":
			networkCalls++
			if networkCalls == 1 {
				return []byte(`[{"IPAM":{"Config":[{"Gateway":"172.18.0.1"}]}}]`), nil
			}
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		case strings.Contains(call, " up -d --wait"):
			return nil, errors.New("port conflict")
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "up"})
	if code == 0 {
		t.Fatal("up unexpectedly succeeded")
	}
	after, err := os.ReadFile(valkeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "bind 127.0.0.1\n") || strings.Contains(string(after), "172.18.0.1") {
		t.Fatalf("Valkey config did not fall back to loopback\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestDoctorAllowsStoppedStack(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if strings.Contains(call, " ps --status running --services") {
			return nil, nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "doctor"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "stack is stopped") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestLogsAndShellUseAttach(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(string, []string) ([]byte, error) { return nil, nil }}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "logs", "php"}); code != 0 {
		t.Fatalf("logs exit = %d, stderr = %s", code, stderr.String())
	}
	if code := a.Run(context.Background(), []string{"-C", root, "shell", "php"}); code != 0 {
		t.Fatalf("shell exit = %d, stderr = %s", code, stderr.String())
	}
	var sawLogs, sawShell bool
	for _, call := range runner.calls {
		sawLogs = sawLogs || strings.Contains(call, "logs --tail 200 --follow php")
		sawShell = sawShell || strings.Contains(call, "exec php sh")
	}
	if !sawLogs || !sawShell {
		t.Fatalf("attach calls missing: %v", runner.calls)
	}
}

func TestProjectContainersRunningRecognizesFixedNames(t *testing.T) {
	runner := &fakeRunner{handle: func(_ string, _ []string) ([]byte, error) {
		return []byte("unrelated\trunning\nwebdock-php\tpaused\n"), nil
	}}
	a := &App{Runner: runner}
	running, err := a.projectContainersRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !running {
		t.Fatal("projectContainersRunning() = false, want true")
	}
	if len(runner.calls) != 1 || !strings.Contains(runner.calls[0], "ps --all --format") {
		t.Fatalf("unexpected Docker call: %v", runner.calls)
	}

	stoppedRunner := &fakeRunner{handle: func(_ string, _ []string) ([]byte, error) {
		return []byte("webdock-php\texited\n"), nil
	}}
	stopped := &App{Runner: stoppedRunner}
	running, err = stopped.projectContainersRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if running {
		t.Fatal("projectContainersRunning() = true for an exited container")
	}
}

func testApp(stdout, stderr *bytes.Buffer, runner *fakeRunner) *App {
	return &App{
		Stdout:   stdout,
		Stderr:   stderr,
		Stdin:    strings.NewReader(""),
		Runner:   runner,
		Getwd:    func() (string, error) { return "", nil },
		LocalIPs: func() ([]net.IP, error) { return []net.IP{net.ParseIP("172.17.0.1"), net.ParseIP("172.18.0.1")}, nil },
		Platform: "darwin",
	}
}

func TestUpRejectsWildcardValkeyListener(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("edge\nphp\nvalkey\n"), nil
		case strings.Contains(call, " exec -T "):
			return nil, nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	a.Platform = "linux"
	a.ReadListeners = func() ([]doctor.Listener, error) {
		return []doctor.Listener{
			{IP: net.IPv4zero, Port: 80},
			{IP: net.IPv4zero, Port: 443},
			{IP: net.ParseIP("127.0.0.1"), Port: 9000},
			{IP: net.ParseIP("127.0.0.1"), Port: 9001},
			{IP: net.IPv4zero, Port: 6379},
		}, nil
	}
	code := a.Run(context.Background(), []string{"-C", root, "up"})
	if code == 0 {
		t.Fatal("up accepted a wildcard Valkey listener")
	}
	if !strings.Contains(stderr.String(), "0.0.0.0:6379") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDoctorRejectsStoppedPortConflict(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(string, []string) ([]byte, error) { return nil, nil }}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	a.Platform = "linux"
	a.ReadListeners = func() ([]doctor.Listener, error) {
		return []doctor.Listener{{IP: net.ParseIP("0.0.0.0"), Port: 80}}, nil
	}
	code := a.Run(context.Background(), []string{"-C", root, "doctor"})
	if code == 0 || !strings.Contains(stderr.String(), "port 80") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestDBUseSwitchesRunningDatabase(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "mysql"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.Contains(call, " ps --status running --services"):
			return []byte("edge\nphp\nvalkey\nmysql\n"), nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "db", "use", "postgres"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	cfg, err := instance.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBEngine() != "postgres" {
		t.Fatalf("DB engine = %q", cfg.DBEngine())
	}
	var stoppedMySQL, startedPostgres bool
	for _, call := range runner.calls {
		stoppedMySQL = stoppedMySQL || strings.Contains(call, "--profile mysql stop mysql")
		startedPostgres = startedPostgres || strings.Contains(call, "--profile postgres up -d --wait --wait-timeout 300 postgres")
	}
	if !stoppedMySQL || !startedPostgres {
		t.Fatalf("database switch calls missing: %v", runner.calls)
	}
}

func TestDBUseRollsBackOnStartFailure(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "mysql"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.Contains(call, " ps --status running --services"):
			return []byte("edge\nphp\nvalkey\nmysql\n"), nil
		case strings.Contains(call, "--profile postgres up -d --wait --wait-timeout 300 postgres"):
			return nil, errors.New("postgres failed")
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "db", "use", "postgres"})
	if code == 0 {
		t.Fatal("db use unexpectedly succeeded")
	}
	cfg, err := instance.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBEngine() != "mysql" {
		t.Fatalf("DB engine = %q, want rollback to mysql", cfg.DBEngine())
	}
	stoppedPostgres := false
	for _, call := range runner.calls {
		stoppedPostgres = stoppedPostgres || strings.Contains(call, "--profile postgres stop postgres")
	}
	if !stoppedPostgres {
		t.Fatalf("failed postgres container was not stopped: %v", runner.calls)
	}
}

func TestDBUseRejectsIncompleteLayout(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "mysql"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "config", "php", "php.ini")); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(string, []string) ([]byte, error) { return nil, nil }}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "db", "use", "postgres"})
	if code == 0 {
		t.Fatal("db use unexpectedly accepted an incomplete layout")
	}
	cfg, err := instance.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBEngine() != "mysql" {
		t.Fatalf("DB engine = %q, want mysql", cfg.DBEngine())
	}
}

func TestBackupStopsAndRestartsRunningStack(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sites", "index.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.Contains(call, " ps --all --format "):
			return []byte("webdock-edge\trunning\n"), nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("edge\nphp\nvalkey\n"), nil
		case strings.Contains(call, " exec -T "):
			return nil, nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "backup", "--with-sites"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	archives, err := filepath.Glob(filepath.Join(root, "backups", "*.tar.gz"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives = %v, err = %v", archives, err)
	}
	var stopped, started bool
	for _, call := range runner.calls {
		stopped = stopped || strings.Contains(call, "--profile mysql --profile postgres stop")
		started = started || strings.Contains(call, "--ansi never --env-file webdock.env -f compose.yaml up -d --wait")
	}
	if !stopped || !started {
		t.Fatalf("backup lifecycle calls missing: %v", runner.calls)
	}
}

func TestRestoreIntoEmptyInstance(t *testing.T) {
	source := t.TempDir()
	if _, err := instance.Init(source, "none"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := backup.Create(source, archive, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if call == "docker network inspect bridge" {
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", target, "restore", archive})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	cfg, err := instance.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.ValidateLayout(cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Restored Webdock instance") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRestoreWithoutForceDoesNotStopRunningStack(t *testing.T) {
	source := t.TempDir()
	if _, err := instance.Init(source, "none"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := backup.Create(source, archive, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if _, err := instance.Init(target, "none"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if strings.Contains(call, " ps --all --format ") {
			return []byte("webdock-edge\trunning\n"), nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", target, "restore", archive})
	if code == 0 {
		t.Fatal("restore without force unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), backup.ErrForceRequired.Error()) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	for _, call := range runner.calls {
		if strings.Contains(call, " stop") {
			t.Fatalf("restore without force stopped the stack: %v", runner.calls)
		}
	}
}

func TestRestoreRefusesRunningContainersWithBrokenConfig(t *testing.T) {
	source := t.TempDir()
	if _, err := instance.Init(source, "none"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := backup.Create(source, archive, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if _, err := instance.Init(target, "none"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "webdock.env"), []byte("broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if strings.Contains(call, " ps --all --format ") {
			return []byte("webdock-edge\trunning\n"), nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", target, "restore", "--force", archive})
	if code == 0 || !strings.Contains(stderr.String(), "containers are running") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestRestoreStopsAndRestartsRunningStack(t *testing.T) {
	source := t.TempDir()
	if _, err := instance.Init(source, "none"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := backup.Create(source, archive, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if _, err := instance.Init(target, "none"); err != nil {
		t.Fatal(err)
	}
	psFormatCalls := 0
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.Contains(call, " ps --all --format "):
			psFormatCalls++
			if psFormatCalls == 1 {
				return []byte("webdock-edge\trunning\n"), nil
			}
			return nil, nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("edge\nphp\nvalkey\n"), nil
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", target, "restore", "--force", archive})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	var stopped, restarted bool
	for _, call := range runner.calls {
		stopped = stopped || strings.Contains(call, "stop")
		restarted = restarted || strings.Contains(call, "up -d --force-recreate")
	}
	if !stopped || !restarted {
		t.Fatalf("restore lifecycle calls missing: %v", runner.calls)
	}
}

func TestInitAcceptsDirectoryBeforeDBFlag(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, &fakeRunner{handle: func(string, []string) ([]byte, error) { return nil, nil }})
	code := a.Run(context.Background(), []string{"init", root, "--db", "mysql"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	cfg, err := instance.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBEngine() != "mysql" {
		t.Fatalf("DB engine = %q", cfg.DBEngine())
	}
}

func TestUpRejectsOccupiedPortBeforeDockerStart(t *testing.T) {
	root := t.TempDir()
	if _, err := instance.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if call == "docker network inspect bridge" {
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	a.Platform = "linux"
	a.ReadListeners = func() ([]doctor.Listener, error) {
		return []doctor.Listener{{IP: net.ParseIP("0.0.0.0"), Port: 80}}, nil
	}
	code := a.Run(context.Background(), []string{"-C", root, "up"})
	if code == 0 || !strings.Contains(stderr.String(), "port 80") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	for _, call := range runner.calls {
		if strings.Contains(call, " up -d ") {
			t.Fatalf("Docker start ran despite port conflict: %v", runner.calls)
		}
	}
}

func TestStoppedPortCheckOnlyUsesSelectedDatabase(t *testing.T) {
	tests := []struct {
		engine  string
		port    int
		wantErr bool
	}{
		{engine: "none", port: 3306, wantErr: false},
		{engine: "none", port: 5432, wantErr: false},
		{engine: "mysql", port: 3306, wantErr: true},
		{engine: "mysql", port: 5432, wantErr: false},
		{engine: "postgres", port: 5432, wantErr: true},
		{engine: "postgres", port: 3306, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.engine+"-"+strconv.Itoa(tt.port), func(t *testing.T) {
			root := t.TempDir()
			cfg, err := instance.Init(root, tt.engine)
			if err != nil {
				t.Fatal(err)
			}
			a := &App{Platform: "linux"}
			a.ReadListeners = func() ([]doctor.Listener, error) {
				return []doctor.Listener{{IP: net.ParseIP("127.0.0.1"), Port: tt.port}}, nil
			}
			err = a.checkStoppedPortConflicts(cfg, map[string]bool{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInitWritesDetectedBridgeGateway(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		if name == "docker" && len(args) >= 3 && args[0] == "network" && args[1] == "inspect" {
			return []byte(`[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`), nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "init"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	valkey, err := os.ReadFile(filepath.Join(root, "config", "valkey", "valkey.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(valkey), "bind 127.0.0.1 172.17.0.1") {
		t.Fatalf("Valkey config = %s", valkey)
	}
}
