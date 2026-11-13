package app

import (
	"os/exec"
	"syscall"
)

func hideTranslationConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
