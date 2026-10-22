package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Runner interface {
	Output(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)
	Attach(ctx context.Context, dir string, env []string, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error
}

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			return nil, fmt.Errorf("%s: %w", strings.Join(append([]string{name}, args...), " "), err)
		}
		return nil, fmt.Errorf("%s: %w: %s", strings.Join(append([]string{name}, args...), " "), err, message)
	}
	return out, nil
}

func (ExecRunner) Attach(ctx context.Context, dir string, env []string, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(append([]string{name}, args...), " "), err)
	}
	return nil
}
