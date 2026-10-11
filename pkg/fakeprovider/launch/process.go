package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

var errExitedEarly = errors.New("process exited before it was ready")

type process struct {
	name    string
	logPath string
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
}

func (h *Harness) startProcess(ctx context.Context, name, executable string, args ...string) (*process, error) {
	logFile, err := h.createLog(name)
	if err != nil {
		return nil, err
	}
	cmd := h.configure(exec.CommandContext(context.WithoutCancel(ctx), executable, args...))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(fmt.Errorf("start %s: %w", name, err), logFile.Close())
	}
	p := &process{
		name:    name,
		logPath: logFile.Name(),
		cmd:     cmd,
		done:    make(chan struct{}),
		waitErr: nil,
	}
	go func() {
		p.waitErr = errors.Join(cmd.Wait(), logFile.Close())
		close(p.done)
	}()
	return p, nil
}

func (p *process) stop() error {
	if err := killGroup(p.cmd); err != nil {
		return fmt.Errorf("stop %s: %w", p.name, err)
	}
	<-p.done
	return nil
}

func (h *Harness) runLogged(cmd *exec.Cmd, name string) error {
	logFile, err := h.createLog(name)
	if err != nil {
		return err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	runErr := cmd.Run()
	killErr := killGroup(cmd)
	closeErr := logFile.Close()
	if err := errors.Join(runErr, killErr, closeErr); err != nil {
		return fmt.Errorf("%s failed, see %s: %w\n%s", name, logFile.Name(), err, logTail(logFile.Name()))
	}
	return nil
}

func logTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > logTailBytes {
		data = data[len(data)-logTailBytes:]
	}
	return string(data)
}

func (h *Harness) createLog(name string) (*os.File, error) {
	log, err := os.OpenFile(filepath.Join(h.Root, "logs", name+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create log %s: %w", name, err)
	}
	return log, nil
}

func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group %d: %w", cmd.Process.Pid, err)
	}
	return nil
}
