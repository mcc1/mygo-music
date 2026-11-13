//go:build !windows

package app

import "os/exec"

func hideTranslationConsole(*exec.Cmd) {}
