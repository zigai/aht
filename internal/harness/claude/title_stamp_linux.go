package claude

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func stampTitleSource(info os.FileInfo) (titleSourceStamp, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return titleSourceStamp{}, fmt.Errorf("inspect Claude transcript identity: %w", errors.ErrUnsupported)
	}
	return titleSourceStamp{Device: strconv.FormatUint(stat.Dev, 10), Inode: stat.Ino, Size: info.Size(), Mtime: info.ModTime().UnixNano(), Ctime: stat.Ctim.Sec*1e9 + stat.Ctim.Nsec}, nil
}
