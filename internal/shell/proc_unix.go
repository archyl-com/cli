//go:build !windows

package shell

import (
	"os/exec"
	"syscall"
)

// killTree makes cancelling cmd kill its whole process group, so the git
// processes an archyl command starts stop with it.
func killTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
