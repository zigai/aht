package history

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(info os.FileInfo) (string, string) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), fmt.Sprintf("%d:%d", stat.Ctim.Sec, stat.Ctim.Nsec)
}
