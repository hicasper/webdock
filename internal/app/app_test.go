package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webdock/internal/backup"
	"webdock/internal/doctor"
	"webdock/internal/instance"
)

type fakeRunner struct {
	handle func(name string, args []string) ([]byte, error)
	attach func(name string, args []string, stdout, stderr io.Writer) error
	calls  []string
}

func (f *fakeRunner) Output(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if f.handle == nil {
		return nil, nil
	}
	return f.handle(name, args)
}

func (f *fakeRunner) Attach(_ context.Context, _ string, _ []string, _ io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if f.attach == nil {
		return nil
	}
	return f.attach(name, args, stdout, stderr)
}

func testApp(stdout, stderr *bytes.Buffer, runner *fakeRunner) *App {
	return &App{
		Stdout:   stdout,
		Stderr:   stderr,
		Stdin:    strings.NewReader(""),
		Runner:   runner,
		Getwd:    func() (string, error) { return "", nil },
		LocalIPs: func() ([]net.IP, error) { return []net.IP{net.ParseIP("172.17.0.1")}, nil },
		Platform: "darwin",
	}
}

func initInstance(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := instance.Init(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func createBackup(t *testing.T, root string) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := backup.Create(root, archive, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	return archive
}

func stoppedStackRunner() *fakeRunner {
	return &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if call == "docker network inspect bridge" {
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		}
		return nil, nil
	}}
}

func imageOK(name string, args []string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	if strings.Contains(call, " image inspect ") || strings.Contains(call, " manifest inspect ") {
		return []byte("[]"), nil
	}
	return nil, errors.New("unexpected command: " + call)
}

func TestRunInitAndRejectDBFlag(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if call == "docker network inspect bridge" {
			return []byte(`[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`), nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C=" + root, "init"}); code != 0 {
		t.Fatalf("init exit = %d, stderr = %s", code, stderr.String())
	}
	cfg, err := instance.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.EnabledComponents(); len(got) != 1 || got[0] != instance.ComponentNginx {
		t.Fatalf("enabled = %v", got)
	}
	stdout.Reset()
	stderr.Reset()
	code := a.Run(context.Background(), []string{"-C=" + root, "init", "--db", "mysql"})
	if code == 0 || !strings.Contains(stderr.String(), "unknown init option") {
		t.Fatalf("init --db exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestEnableWithVersionUsesImageCheck(t *testing.T) {
	root := initInstance(t)
	var stdout, stderr bytes.Buffer
	runner := &fakeRunner{handle: imageOK}
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "enable", "redis", "7"}); code != 0 {
		t.Fatalf("enable exit = %d, stderr = %s", code, stderr.String())
	}
	cfg, err := instance.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	redis, _ := cfg.Component(instance.ComponentRedis)
	if !redis.Enabled || redis.Version != "7" || redis.Image != "valkey/valkey:7" {
		t.Fatalf("redis = %+v", redis)
	}
	if !strings.Contains(stdout.String(), "Enabled redis 7") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	badRoot := initInstance(t)
	badRunner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		return nil, errors.New("not found")
	}}
	stdout.Reset()
	stderr.Reset()
	a = testApp(&stdout, &stderr, badRunner)
	code := a.Run(context.Background(), []string{"-C", badRoot, "enable", "redis", "6"})
	if code == 0 || !strings.Contains(stderr.String(), "not available") {
		t.Fatalf("missing image exit = %d, stderr = %q", code, stderr.String())
	}
	cfg, err = instance.Load(badRoot)
	if err != nil {
		t.Fatal(err)
	}
	redis, _ = cfg.Component(instance.ComponentRedis)
	if redis.Enabled {
		t.Fatal("failed image check changed component state")
	}
}

func TestUpStopsDisabledComponentsAndStartsEnabledOnly(t *testing.T) {
	root := initInstance(t)
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		case strings.Contains(call, " image inspect "):
			return nil, nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("nginx\nredis\n"), nil
		case strings.Contains(call, " config --quiet"):
			return nil, nil
		case strings.Contains(call, "up -d --wait"):
			return nil, nil
		case strings.Contains(call, " exec -T nginx "):
			return nil, nil
		case strings.Contains(call, " stop redis"):
			return nil, nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "up"}); code != 0 {
		t.Fatalf("up exit = %d, stderr = %s", code, stderr.String())
	}
	var stoppedRedis, startedNginx, startedRedis bool
	for _, call := range runner.calls {
		stoppedRedis = stoppedRedis || strings.Contains(call, "stop redis")
		startedNginx = startedNginx || (strings.Contains(call, "up -d --wait") && strings.Contains(call, "nginx"))
		startedRedis = startedRedis || (strings.Contains(call, "up -d --wait") && strings.Contains(call, "redis"))
	}
	if !stoppedRedis || !startedNginx || startedRedis {
		t.Fatalf("lifecycle calls = %v", runner.calls)
	}
}

func TestStartPHPRequiresNginxRunning(t *testing.T) {
	root := initInstance(t)
	if _, err := instance.SetComponentState(root, instance.ComponentPHP, "true"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if strings.Contains(call, " ps --status running --services") {
			return []byte(""), nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", root, "start", "php"})
	if code == 0 || !strings.Contains(stderr.String(), "requires nginx") {
		t.Fatalf("start php exit = %d, stderr = %q", code, stderr.String())
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "up -d") {
			t.Fatalf("PHP started without Nginx: %v", runner.calls)
		}
	}
}

func TestDataVersionConfirmation(t *testing.T) {
	root := initInstance(t)
	if _, err := instance.SetComponentState(root, instance.ComponentNginx, "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.SetComponentState(root, instance.ComponentRedis, "true"); err != nil {
		t.Fatal(err)
	}
	if err := instance.WriteVersionState(root, instance.Values{"REDIS": "7"}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		case strings.Contains(call, " image inspect "):
			return nil, nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("redis\n"), nil
		case strings.Contains(call, " config --quiet"):
			return nil, nil
		case strings.Contains(call, "up -d --wait"):
			return nil, nil
		case strings.Contains(call, " exec -T redis "):
			return nil, nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	a.Stdin = strings.NewReader("y\n")
	if code := a.Run(context.Background(), []string{"-C", root, "up"}); code != 0 {
		t.Fatalf("up exit = %d, stderr = %s", code, stderr.String())
	}
	versions, err := instance.LoadVersionState(root)
	if err != nil {
		t.Fatal(err)
	}
	if versions["REDIS"] != "8" {
		t.Fatalf("redis data version = %q", versions["REDIS"])
	}

	cancelRoot := initInstance(t)
	if _, err := instance.SetComponentState(cancelRoot, instance.ComponentNginx, "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.SetComponentState(cancelRoot, instance.ComponentRedis, "true"); err != nil {
		t.Fatal(err)
	}
	if err := instance.WriteVersionState(cancelRoot, instance.Values{"REDIS": "7"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	a = testApp(&stdout, &stderr, runner)
	a.Stdin = strings.NewReader("n\n")
	code := a.Run(context.Background(), []string{"-C", cancelRoot, "up"})
	if code == 0 || !strings.Contains(stderr.String(), "startup cancelled") {
		t.Fatalf("cancel exit = %d, stderr = %q", code, stderr.String())
	}
	versions, err = instance.LoadVersionState(cancelRoot)
	if err != nil {
		t.Fatal(err)
	}
	if versions["REDIS"] != "7" {
		t.Fatalf("cancelled run changed redis data version to %q", versions["REDIS"])
	}
}

func TestDatabaseLogicalBackup(t *testing.T) {
	root := initInstance(t)
	if _, err := instance.SetComponentState(root, instance.ComponentMySQL, "true"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		handle: func(name string, args []string) ([]byte, error) {
			call := strings.Join(append([]string{name}, args...), " ")
			if strings.Contains(call, " ps --status running --services") {
				return []byte("mysql\n"), nil
			}
			return nil, nil
		},
		attach: func(_ string, _ []string, stdout, _ io.Writer) error {
			_, err := io.WriteString(stdout, "CREATE TABLE demo(id int);\n")
			return err
		},
	}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "db", "backup", "mysql"}); code != 0 {
		t.Fatalf("db backup exit = %d, stderr = %s", code, stderr.String())
	}
	files, err := filepath.Glob(filepath.Join(root, "backups", "db", "*.sql.gz"))
	if err != nil || len(files) != 1 {
		t.Fatalf("backup files = %v, err = %v", files, err)
	}
	file, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gz)
	_ = gz.Close()
	_ = file.Close()
	if err != nil || !strings.Contains(string(data), "CREATE TABLE") {
		t.Fatalf("backup contents = %q, err = %v", data, err)
	}
}

func TestDatabaseDumpCommandsUseContainerEnvironment(t *testing.T) {
	checks := map[string]string{
		instance.ComponentMySQL:    "MYSQL_PWD",
		instance.ComponentPostgres: "PGPASSWORD",
	}
	for engine, envName := range checks {
		command := strings.Join(databaseDumpCommand(engine), " ")
		if !strings.Contains(command, envName) || strings.Contains(command, "PASSWORD="+envName) {
			t.Fatalf("%s dump command = %q", engine, command)
		}
	}
}

func TestBackupRestartsOnlyPreviouslyRunningComponents(t *testing.T) {
	root := initInstance(t)
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.Contains(call, " ps --status running --services"):
			return []byte("nginx\n"), nil
		case strings.Contains(call, " config --quiet"):
			return nil, nil
		case strings.Contains(call, "up -d --wait"):
			return nil, nil
		case strings.Contains(call, " exec -T nginx "):
			return nil, nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "backup"}); code != 0 {
		t.Fatalf("backup exit = %d, stderr = %s", code, stderr.String())
	}
	var restartedNginx, restartedPHP bool
	for _, call := range runner.calls {
		restartedNginx = restartedNginx || (strings.Contains(call, "up -d --wait") && strings.Contains(call, "nginx"))
		restartedPHP = restartedPHP || (strings.Contains(call, "up -d --wait") && strings.Contains(call, "php"))
	}
	if !restartedNginx || restartedPHP {
		t.Fatalf("restart calls = %v", runner.calls)
	}
}

func TestRestoreVersionMismatchNoKeepsCurrentComponent(t *testing.T) {
	source := initInstance(t)
	if _, err := instance.SetComponentState(source, instance.ComponentMySQL, "8.4"); err != nil {
		t.Fatal(err)
	}
	if err := instance.WriteVersionState(source, instance.Values{"MYSQL": "8.4"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data", "mysql", "archive-data"), []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := createBackup(t, source)

	target := initInstance(t)
	if _, err := instance.SetComponentState(target, instance.ComponentMySQL, "5.7"); err != nil {
		t.Fatal(err)
	}
	if err := instance.WriteVersionState(target, instance.Values{"MYSQL": "5.7"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "data", "mysql", "current-data"), []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := stoppedStackRunner()
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	a.Stdin = strings.NewReader("n\n")
	if code := a.Run(context.Background(), []string{"-C", target, "restore", "--force", archive}); code != 0 {
		t.Fatalf("restore exit = %d, stderr = %s", code, stderr.String())
	}
	restored, err := instance.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	mysql, _ := restored.Component(instance.ComponentMySQL)
	if !mysql.Enabled || mysql.Version != "5.7" {
		t.Fatalf("mysql after n = %+v", mysql)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "mysql", "current-data")); err != nil {
		t.Fatalf("current mysql data was not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "mysql", "archive-data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive mysql data was copied despite n: %v", err)
	}
	versions, err := instance.LoadVersionState(target)
	if err != nil {
		t.Fatal(err)
	}
	if versions["MYSQL"] != "5.7" {
		t.Fatalf("skipped MySQL data version = %q", versions["MYSQL"])
	}
}

func TestRestoreVersionMismatchYesAcceptsArchiveComponent(t *testing.T) {
	source := initInstance(t)
	if _, err := instance.SetComponentState(source, instance.ComponentMySQL, "8.4"); err != nil {
		t.Fatal(err)
	}
	if err := instance.WriteVersionState(source, instance.Values{"MYSQL": "8.4"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data", "mysql", "archive-data"), []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := createBackup(t, source)

	target := initInstance(t)
	if _, err := instance.SetComponentState(target, instance.ComponentMySQL, "5.7"); err != nil {
		t.Fatal(err)
	}
	if err := instance.WriteVersionState(target, instance.Values{"MYSQL": "5.7"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "data", "mysql", "current-data"), []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := stoppedStackRunner()
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	a.Stdin = strings.NewReader("y\n")
	if code := a.Run(context.Background(), []string{"-C", target, "restore", "--force", archive}); code != 0 {
		t.Fatalf("restore exit = %d, stderr = %s", code, stderr.String())
	}
	restored, err := instance.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	mysql, _ := restored.Component(instance.ComponentMySQL)
	if !mysql.Enabled || mysql.Version != "8.4" {
		t.Fatalf("mysql after y = %+v", mysql)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "mysql", "archive-data")); err != nil {
		t.Fatalf("archive mysql data was not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "mysql", "current-data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current mysql data remains after y: %v", err)
	}
	versions, err := instance.LoadVersionState(target)
	if err != nil {
		t.Fatal(err)
	}
	if versions["MYSQL"] != "8.4" {
		t.Fatalf("accepted MySQL data version = %q", versions["MYSQL"])
	}
}

func TestMergedRestoreVersionStatePreservesSkippedComponents(t *testing.T) {
	currentRoot := initInstance(t)
	stageRoot := initInstance(t)
	if err := instance.WriteVersionState(currentRoot, instance.Values{
		"REDIS": "7", "MYSQL": "5.7", "POSTGRES": "16",
	}); err != nil {
		t.Fatal(err)
	}
	if err := instance.WriteVersionState(stageRoot, instance.Values{
		"REDIS": "8", "MYSQL": "8.4", "POSTGRES": "16",
	}); err != nil {
		t.Fatal(err)
	}
	merged, err := mergedRestoreVersionState(currentRoot, true, stageRoot, []instance.Component{
		{Name: instance.ComponentRedis},
		{Name: instance.ComponentPostgres},
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged["REDIS"] != "8" || merged["MYSQL"] != "5.7" || merged["POSTGRES"] != "16" {
		t.Fatalf("merged versions = %v", merged)
	}
}

func TestStoppedPortCheckOnlyUsesEnabledComponents(t *testing.T) {
	root := initInstance(t)
	cfg, err := instance.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Platform: "linux", ReadListeners: func() ([]doctor.Listener, error) {
		return []doctor.Listener{{IP: net.ParseIP("127.0.0.1"), Port: 3306}}, nil
	}}
	if err := a.checkStoppedPortConflicts(cfg, map[string]bool{}); err != nil {
		t.Fatalf("disabled MySQL caused a conflict: %v", err)
	}
	if _, err := instance.SetComponentState(root, instance.ComponentMySQL, "true"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = instance.Load(root)
	if err := a.checkStoppedPortConflicts(cfg, map[string]bool{}); err == nil {
		t.Fatal("enabled MySQL did not report a port conflict")
	}
}

func TestStopAndRedisNameRecognition(t *testing.T) {
	root := initInstance(t)
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) { return nil, nil }}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "stop", "redis"}); code != 0 {
		t.Fatalf("stop exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), " stop redis") {
		t.Fatalf("stop calls = %v", runner.calls)
	}
	if !isWebdockContainer("webdock-redis") || isWebdockContainer("webdock-valkey") {
		t.Fatal("redis container name recognition is incorrect")
	}
}

func TestConfirmDefaultNoAndEOF(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, &fakeRunner{})
	a.Stdin = strings.NewReader("\n")
	ok, err := a.confirm("continue? ")
	if err != nil || ok {
		t.Fatalf("blank confirmation = %v, %v", ok, err)
	}

	a = testApp(&stdout, &stderr, &fakeRunner{})
	a.Stdin = strings.NewReader("")
	if _, err := a.confirm("continue? "); err == nil {
		t.Fatal("EOF confirmation succeeded")
	}
}

func TestRunVersion(t *testing.T) {
	for _, arg := range []string{"version", "-v", "--version"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			a := testApp(&stdout, &stderr, &fakeRunner{})
			a.Version = "0.2.0"
			if code := a.Run(context.Background(), []string{arg}); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
			}
			if got, want := stdout.String(), "webdock 0.2.0\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestUpPullUsesPullPolicy(t *testing.T) {
	root := initInstance(t)
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.Contains(call, " image inspect "):
			return nil, nil
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("nginx\n"), nil
		case strings.Contains(call, " exec -T nginx "):
			return nil, nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "up", "--pull"}); code != 0 {
		t.Fatalf("up --pull exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "--pull always") {
		t.Fatalf("pull policy missing: %v", runner.calls)
	}
}

func TestUpFailureRestoresRedisConfig(t *testing.T) {
	root := initInstance(t)
	configPath := filepath.Join(root, "config", "redis", "valkey.conf")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.Contains(call, " image inspect "):
			return nil, nil
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[{"Gateway":"172.18.0.1"}]}}]`), nil
		case strings.Contains(call, " ps --status running --services"):
			return nil, nil
		case strings.Contains(call, "up -d --wait"):
			return nil, errors.New("start failed")
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "up"}); code == 0 {
		t.Fatal("up unexpectedly succeeded")
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("Redis config was not restored:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestLogsAndShellUseAttach(t *testing.T) {
	root := initInstance(t)
	runner := &fakeRunner{}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "logs", "nginx"}); code != 0 {
		t.Fatalf("logs exit = %d, stderr = %s", code, stderr.String())
	}
	if code := a.Run(context.Background(), []string{"-C", root, "shell", "redis"}); code != 0 {
		t.Fatalf("shell exit = %d, stderr = %s", code, stderr.String())
	}
	calls := strings.Join(runner.calls, "\n")
	if !strings.Contains(calls, "logs --tail 200 --follow nginx") || !strings.Contains(calls, "exec redis sh") {
		t.Fatalf("attach calls = %v", runner.calls)
	}
}

func TestProjectContainersRunningRecognizesFixedNames(t *testing.T) {
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		return []byte("other\trunning\nwebdock-redis\trunning\n"), nil
	}}
	a := testApp(&bytes.Buffer{}, &bytes.Buffer{}, runner)
	running, err := a.projectContainersRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !running {
		t.Fatal("webdock-redis was not recognized")
	}
}

func TestInitWritesDetectedRedisBridgeGateway(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		if call == "docker network inspect bridge" {
			return []byte(`[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`), nil
		}
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", root, "init"}); code != 0 {
		t.Fatalf("init exit = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "config", "redis", "valkey.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "bind 127.0.0.1 172.17.0.1") {
		t.Fatalf("Redis config = %s", data)
	}
}

func TestRestoreWithoutForceDoesNotStopStack(t *testing.T) {
	source := initInstance(t)
	archive := createBackup(t, source)
	target := initInstance(t)
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		return nil, nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", target, "restore", archive})
	if code == 0 || !strings.Contains(stderr.String(), backup.ErrForceRequired.Error()) {
		t.Fatalf("restore exit = %d, stderr = %q", code, stderr.String())
	}
	for _, call := range runner.calls {
		if strings.Contains(call, " stop") {
			t.Fatalf("restore without force stopped the stack: %v", runner.calls)
		}
	}
}

func TestRestoreRefusesRunningContainersWithBrokenConfig(t *testing.T) {
	source := initInstance(t)
	archive := createBackup(t, source)
	target := initInstance(t)
	if err := os.WriteFile(filepath.Join(target, "webdock.env"), []byte("broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		return []byte("webdock-nginx\trunning\n"), nil
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	code := a.Run(context.Background(), []string{"-C", target, "restore", "--force", archive})
	if code == 0 || !strings.Contains(stderr.String(), "containers are running") {
		t.Fatalf("restore exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestRestoreRestartsPreviouslyRunningComponents(t *testing.T) {
	source := initInstance(t)
	archive := createBackup(t, source)
	target := initInstance(t)
	psFormatCalls := 0
	runner := &fakeRunner{handle: func(name string, args []string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.Contains(call, " ps --all --format "):
			psFormatCalls++
			if psFormatCalls == 1 {
				return []byte("webdock-nginx\trunning\n"), nil
			}
			return nil, nil
		case strings.Contains(call, " ps --status running --services"):
			return []byte("nginx\n"), nil
		case strings.Contains(call, " image inspect "):
			return nil, nil
		case call == "docker network inspect bridge":
			return []byte(`[{"IPAM":{"Config":[]}}]`), nil
		case strings.Contains(call, " exec -T nginx "):
			return nil, nil
		default:
			return nil, nil
		}
	}}
	var stdout, stderr bytes.Buffer
	a := testApp(&stdout, &stderr, runner)
	if code := a.Run(context.Background(), []string{"-C", target, "restore", "--force", archive}); code != 0 {
		t.Fatalf("restore exit = %d, stderr = %s", code, stderr.String())
	}
	calls := strings.Join(runner.calls, "\n")
	if !strings.Contains(calls, " stop") || !strings.Contains(calls, "up -d --wait --wait-timeout 300 --force-recreate nginx") {
		t.Fatalf("restore lifecycle calls = %v", runner.calls)
	}
}
