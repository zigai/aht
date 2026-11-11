package history

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func fileIdentity(info os.FileInfo) (string, string) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}

	// The device number is left out because block device minors can change
	// across reboots, which would make every unchanged file look replaced.
	return strconv.FormatUint(stat.Ino, 10), fmt.Sprintf(
		"%d:%d",
		stat.Ctimespec.Sec,
		stat.Ctimespec.Nsec,
	)
}
