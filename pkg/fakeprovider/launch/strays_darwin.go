package launch

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zigai/aht/v2/internal/processinfo"
)

func launchProcesses(ctx context.Context, marker []byte) ([]int, error) {
	processes, err := processinfo.ListWithEnv(ctx, []string{"AHT_STORE"})
	if err != nil {
		return nil, fmt.Errorf("list launch processes: %w", err)
	}

	store := strings.TrimPrefix(string(marker), "AHT_STORE=")
	self := os.Getpid()

	var pids []int

	for _, process := range processes {
		if process.PID != self && process.Env["AHT_STORE"] == store {
			pids = append(pids, process.PID)
		}
	}

	return pids, nil
}
