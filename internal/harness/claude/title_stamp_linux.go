package claude

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func stampTitleSource(info os.FileInfo) (titleSourceStamp, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return titleSourceStamp{}, fmt.Errorf("inspect Claude transcript identity: %w", errors.ErrUnsupported)
	}
	return titleSourceStamp{Device: stat.Dev, Inode: stat.Ino, Size: info.Size(), Mtime: info.ModTime().UnixNano(), Ctime: stat.Ctim.Sec*1e9 + stat.Ctim.Nsec}, nil
}
