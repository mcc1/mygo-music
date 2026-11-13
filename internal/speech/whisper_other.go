//go:build !windows

package speech

import "os/exec"

func hideWhisperConsole(*exec.Cmd) {}
