package speech

import (
	"os/exec"
	"syscall"
)

func hideWhisperConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
