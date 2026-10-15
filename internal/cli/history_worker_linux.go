package cli

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func setHistoryPriority() error {
	const (
		lowestNice           = 19
		ioPriorityWhoProcess = 1
		ioPriorityClassIdle  = 3
		ioPriorityClassShift = 13
	)
	if err := unix.Setpriority(unix.PRIO_PROCESS, 0, lowestNice); err != nil {
		return fmt.Errorf("set history refresh priority: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOPRIO_SET, ioPriorityWhoProcess, 0, ioPriorityClassIdle<<ioPriorityClassShift); errno != 0 {
		return fmt.Errorf("set history refresh I/O priority: %w", errno)
	}
	return nil
}
