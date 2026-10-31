package cli

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func setHistoryPriority() error {
	const lowestNice = 19
	if err := unix.Setpriority(unix.PRIO_PROCESS, 0, lowestNice); err != nil {
		return fmt.Errorf("set history refresh priority: %w", err)
	}

	return nil
}
