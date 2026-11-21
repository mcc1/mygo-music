package speech

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

func probeWhisperVulkan(ctx context.Context, root string) error {
	executable, err := linuxWhisperRuntimes["vulkan"].install(root)
	if err != nil {
		return err
	}
	return runWhisperVulkanProbe(ctx, executable, 5*time.Second)
}

func runWhisperVulkanProbe(ctx context.Context, executable string, timeout time.Duration) error {
	attempt, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(attempt, executable, "--mygo-probe-vulkan")
	cmd.Dir = filepath.Dir(executable)
	err := cmd.Run()
	if attempt.Err() != nil {
		return fmt.Errorf("Vulkan device probe: %w", attempt.Err())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 2 {
		return errors.New("no usable Vulkan GPU")
	}
	if err != nil {
		return fmt.Errorf("Vulkan device probe: %w", err)
	}
	return nil
}
