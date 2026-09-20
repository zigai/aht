//go:build compatibility

package hostcompat

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func (host isolatedHost) startOpenClawGateway(t *testing.T, port int) {
	t.Helper()

	logFile, err := os.Create(filepath.Join(host.root, "openclaw-gateway.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(host.hostPath, "gateway", "run", "--bind", "loopback", "--port", fmt.Sprint(port), "--auth", "none")
	command.Env = host.env
	command.Dir = host.work
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_ = command.Wait()
		_ = logFile.Close()
	})
	address := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		connection, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	output, _ := os.ReadFile(filepath.Join(host.root, "openclaw-gateway.log"))
	t.Fatalf("OpenClaw gateway did not listen on %s\n%s", address, output)
}
