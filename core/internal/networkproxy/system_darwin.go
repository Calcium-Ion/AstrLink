package networkproxy

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

func systemSettings() (settings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return settings{}, fmt.Errorf("macOS proxy query failed: %w", err)
	}
	return parseMacSettings(string(output))
}
