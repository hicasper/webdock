package docker

import (
	"context"
	"io"
)

type Compose struct {
	Runner Runner
	Dir    string
}

func (c Compose) Output(ctx context.Context, profiles []string, args ...string) ([]byte, error) {
	return c.Runner.Output(ctx, c.Dir, nil, "docker", c.args(profiles, args...)...)
}

func (c Compose) Attach(ctx context.Context, profiles []string, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	return c.Runner.Attach(ctx, c.Dir, nil, stdin, stdout, stderr, "docker", c.args(profiles, args...)...)
}

func (c Compose) args(profiles []string, args ...string) []string {
	result := []string{
		"compose",
		"--ansi", "never",
		"--env-file", "webdock.env",
		"-f", "compose.yaml",
	}
	for _, profile := range profiles {
		if profile != "" && profile != "none" {
			result = append(result, "--profile", profile)
		}
	}
	return append(result, args...)
}

func Profiles(engine string) []string {
	if engine == "" || engine == "none" {
		return nil
	}
	return []string{engine}
}

func AllProfiles() []string {
	return []string{"mysql", "postgres"}
}
