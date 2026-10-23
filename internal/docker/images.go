package docker

import (
	"context"
	"fmt"
)

func ImageAvailable(ctx context.Context, runner Runner, image string) error {
	if _, err := runner.Output(ctx, "", nil, "docker", "image", "inspect", image); err == nil {
		return nil
	}
	if _, err := runner.Output(ctx, "", nil, "docker", "manifest", "inspect", image); err != nil {
		return fmt.Errorf("docker image %q is not available: %w", image, err)
	}
	return nil
}
