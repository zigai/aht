package processinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const leadingProcessArguments = 2

type Scanner struct {
	root         string
	boot         string
	entries      map[int]*processFiles
	generation   uint64
	statData     []byte
	argumentData []byte
	reader       linuxProcessReader
}

type processFiles struct {
	path       string
	inode      uint64
	stat       *os.File
	arguments  *os.File
	process    Process
	generation uint64
	detailed   bool
}

func (s *Scanner) List(
	ctx context.Context,
	keys []string,
	includeEnv func(Process) bool,
	allArguments func(Process) bool,
) ([]Process, error) {
	boot := s.boot
	if s.root != procRoot || boot == "" {
		var err error

		boot, err = linuxBootIdentity()
		if err != nil {
			return nil, err
		}
	}

	processes, err := s.scan(ctx, procRoot, boot, keys, includeEnv, allArguments)
	if errors.Is(err, unix.EMFILE) || errors.Is(err, unix.ENFILE) {
		s.Close()
		return ListWithEnv(ctx, keys, includeEnv)
	}

	return processes, err
}

func (s *Scanner) Details(ctx context.Context, pid int) (Process, bool, error) {
	entry := s.entries[pid]
	if entry == nil || entry.generation != s.generation {
		return Process{}, false, nil
	}

	if entry.detailed {
		return entry.process, true, nil
	}

	path := entry.path

	info, err := os.Stat(path)
	if err != nil {
		if isLinuxNotExist(err) {
			return Process{}, false, nil
		}

		return Process{}, false, classifyProcessError(path, err)
	}

	native, ok := info.Sys().(*syscall.Stat_t)
	if !ok || native.Ino != entry.inode || int(native.Uid) != os.Geteuid() {
		return Process{}, false, nil
	}

	process, err := s.read(ctx, pid, entry, nil, nil, nil)
	if err != nil {
		if isLinuxProcessExited(path, pid, err) {
			return Process{}, false, nil
		}

		return Process{}, false, err
	}

	if process.StartIdentity != entry.process.StartIdentity {
		return Process{}, false, nil
	}

	entry.process = process
	entry.detailed = true

	return process, true, nil
}

func (s *Scanner) Close() {
	for _, entry := range s.entries {
		entry.close()
	}

	s.entries = nil
}

func (s *Scanner) scan(
	ctx context.Context,
	root string,
	boot string,
	keys []string,
	includeEnv func(Process) bool,
	allArguments func(Process) bool,
) ([]Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("process listing context: %w", err)
	}

	if s.root != root || s.boot != boot {
		s.Close()
		s.root = root
		s.boot = boot
	}

	infos, current, err := ownedLinuxProcesses(root)
	if err != nil {
		return nil, err
	}

	s.retainProcesses(current)
	s.generation++

	processes := make([]Process, 0, len(current))

	for _, info := range infos {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("process listing context: %w", err)
		}

		pid, err := strconv.Atoi(info.Name())
		if err != nil || current[pid] == 0 {
			continue
		}

		process, found, err := s.readEntry(ctx, pid, current[pid], keys, includeEnv, allArguments)
		if err != nil {
			return nil, err
		}

		if found {
			processes = append(processes, process)
		}
	}

	return processes, nil
}

func (s *Scanner) retainProcesses(current map[int]uint64) {
	if s.entries == nil {
		s.entries = make(map[int]*processFiles)
	}

	for pid, entry := range s.entries {
		if current[pid] != entry.inode {
			entry.close()
			delete(s.entries, pid)
		}
	}
}

func ownedLinuxProcesses(root string) ([]os.FileInfo, map[int]uint64, error) {
	dir, err := os.Open(root)
	if err != nil {
		return nil, nil, classifyProcessError(root, err)
	}

	infos, err := dir.Readdir(-1)
	_ = dir.Close()

	if err != nil {
		return nil, nil, classifyProcessError(root, err)
	}

	slices.SortFunc(infos, func(a, b os.FileInfo) int {
		return strings.Compare(a.Name(), b.Name())
	})

	euid := os.Geteuid()
	if euid < 0 || euid > maxUint32 {
		return nil, nil, fmt.Errorf("%d: %w", euid, errInvalidEffectiveUserID)
	}

	uid := uint32(euid)

	current := make(map[int]uint64, len(infos))
	for _, info := range infos {
		pid, err := strconv.Atoi(info.Name())
		if err != nil || pid <= 0 || !info.IsDir() {
			continue
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if ok && stat.Uid == uid {
			current[pid] = stat.Ino
		}
	}

	return infos, current, nil
}

func (s *Scanner) readEntry(
	ctx context.Context,
	pid int,
	inode uint64,
	keys []string,
	includeEnv func(Process) bool,
	allArguments func(Process) bool,
) (Process, bool, error) {
	entry := s.entries[pid]
	if entry == nil {
		path := filepath.Join(s.root, strconv.Itoa(pid))

		var err error

		entry, err = openProcessFiles(path, inode)
		if err != nil {
			if isLinuxNotExist(err) {
				return Process{}, false, nil
			}

			return Process{}, false, err
		}

		s.entries[pid] = entry
	}

	process, err := s.read(ctx, pid, entry, keys, includeEnv, allArguments)
	if err != nil {
		if isLinuxProcessExited(entry.path, pid, err) {
			entry.close()
			delete(s.entries, pid)

			return Process{}, false, nil
		}

		return Process{}, false, err
	}

	entry.process = process
	entry.generation = s.generation

	return process, true, nil
}

func openProcessFiles(path string, inode uint64) (*processFiles, error) {
	stat, err := os.Open(filepath.Join(path, "stat"))
	if err != nil {
		return nil, classifyProcessError(path, err)
	}

	arguments, err := os.Open(filepath.Join(path, "cmdline"))
	if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
		_ = stat.Close()
		return nil, classifyProcessError(path, err)
	}

	current, statErr := os.Stat(path)
	if statErr != nil {
		_ = stat.Close()
		if arguments != nil {
			_ = arguments.Close()
		}

		return nil, classifyProcessError(path, statErr)
	}

	native, ok := current.Sys().(*syscall.Stat_t)
	if !ok || native.Ino != inode {
		_ = stat.Close()
		if arguments != nil {
			_ = arguments.Close()
		}

		return nil, os.ErrNotExist
	}

	entry := new(processFiles)
	entry.path = path
	entry.inode = inode
	entry.stat = stat
	entry.arguments = arguments

	return entry, nil
}

func (s *Scanner) read(
	ctx context.Context,
	pid int,
	entry *processFiles,
	keys []string,
	includeEnv func(Process) bool,
	allArguments func(Process) bool,
) (Process, error) {
	data, err := readProcessData(
		ctx,
		entry.stat,
		&s.statData,
		func(data []byte) bool { return len(data) > 0 && data[len(data)-1] == '\n' },
	)
	if err != nil {
		return Process{}, classifyProcessError(entry.stat.Name(), err)
	}

	process, err := parseLinuxStat(string(data))
	if err != nil {
		return Process{}, &TableError{Path: entry.stat.Name(), Err: err}
	}

	process.PID = pid
	process.StartIdentity = s.boot + ":" + process.StartIdentity

	process.Executable = readLinuxLink(filepath.Join(entry.path, "exe"))
	if err := s.readArguments(ctx, entry, &process, allArguments); err != nil {
		return Process{}, err
	}

	entry.detailed = includeEnv == nil || includeEnv(process)
	if entry.detailed {
		process.CWD = readLinuxLink(filepath.Join(entry.path, "cwd"))
		if tty := readLinuxLink(filepath.Join(entry.path, "fd", "0")); isLinuxTTY(tty) {
			process.TTY = tty
		}

		if len(keys) > 0 {
			process.Env = s.reader.readEnvironment(filepath.Join(entry.path, "environ"), keys)
		}
	}

	return process, nil
}

func (s *Scanner) readArguments(
	ctx context.Context,
	entry *processFiles,
	process *Process,
	allArguments func(Process) bool,
) error {
	if entry.arguments == nil {
		path := filepath.Join(entry.path, "cmdline")

		var err error

		entry.arguments, err = os.Open(path)
		if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
			return classifyProcessError(path, err)
		}
	}

	if entry.arguments == nil {
		return nil
	}

	full := allArguments == nil || allArguments(*process)
	prefix := 0
	complete := func(data []byte) bool {
		if full {
			return false
		}

		prefix = argumentPrefix(data)
		if prefix == 0 {
			return false
		}

		process.Args = splitLinuxArgs(data[:prefix])
		full = allArguments(*process)

		return !full
	}

	args, err := readProcessData(ctx, entry.arguments, &s.argumentData, complete)
	if err != nil {
		process.Args = nil
	} else if full || prefix == 0 {
		process.Args = splitLinuxArgs(args)
	}

	return nil
}

func readProcessData(
	ctx context.Context,
	file *os.File,
	buffer *[]byte,
	complete func([]byte) bool,
) ([]byte, error) {
	data := (*buffer)[:0]
	if cap(data) == 0 {
		data = make([]byte, 0, os.Getpagesize())
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read process data: %w", err)
		}

		if len(data) == cap(data) {
			data = slices.Grow(data, cap(data))
		}

		capacity := data[:cap(data)]

		n, err := unix.Pread(int(file.Fd()), capacity[len(data):], int64(len(data)))
		if errors.Is(err, unix.EINTR) {
			continue
		}

		if n > 0 {
			data = capacity[:len(data)+n]
		}

		*buffer = data

		if err != nil {
			return nil, &os.PathError{Op: "read", Path: file.Name(), Err: err}
		}

		if n == 0 || complete(data) {
			return data, nil
		}
	}
}

func argumentPrefix(data []byte) int {
	start, count := 0, 0

	for index, value := range data {
		if value != 0 {
			continue
		}

		if index > start {
			count++
		}

		start = index + 1
		if count == leadingProcessArguments {
			return start
		}
	}

	return 0
}

func (p *processFiles) close() {
	_ = p.stat.Close()
	if p.arguments != nil {
		_ = p.arguments.Close()
	}
}
