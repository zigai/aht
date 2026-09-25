package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/command"
)

// RunCommand executes a command with bounded output and a caller-specified timeout.
// It never returns a partial successful response when the output budget is exceeded.
func RunCommand(ctx context.Context, timeout time.Duration, outputLimit int, executable string, args ...string) ([]byte, error) {
	output, err := command.RunWithLimits(ctx, timeout, outputLimit, executable, nil, args...)
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, fmt.Errorf("harness command: %w", err)
	}
	return output, nil
}
