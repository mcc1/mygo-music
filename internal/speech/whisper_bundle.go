package speech

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/cpu"
)

// Linux release builds populate this directory before compiling MyGo.
//
//go:embed whisper_runtime/*
var whisperRuntimeBundle embed.FS

type linuxWhisperRuntime struct {
	data []byte
	hash [sha256.Size]byte
	name string
}

var linuxWhisperRuntimes = func() map[string]linuxWhisperRuntime {
	runtimes := make(map[string]linuxWhisperRuntime)
	for _, backend := range []string{"cpu", "cpu-avx2", "vulkan"} {
		name := "whisper-cli-" + backend
		data, err := whisperRuntimeBundle.ReadFile("whisper_runtime/" + name)
		if err == nil && len(data) > 0 {
			runtimes[backend] = linuxWhisperRuntime{data, sha256.Sum256(data), name}
		}
	}
	return runtimes
}()

func linuxWhisperCPURuntime() linuxWhisperRuntime {
	if cpu.X86.HasAVX && cpu.X86.HasAVX2 && cpu.X86.HasFMA && cpu.X86.HasF16C && cpu.X86.HasBMI2 &&
		cpu.X86.HasSSE3 && cpu.X86.HasSSSE3 && cpu.X86.HasSSE41 && cpu.X86.HasSSE42 && cpu.X86.HasPOPCNT {
		if r := linuxWhisperRuntimes["cpu-avx2"]; len(r.data) > 0 {
			return r
		}
	}
	return linuxWhisperRuntimes["cpu"]
}

func (r linuxWhisperRuntime) install(root string) (string, error) {
	if len(r.data) == 0 {
		return "", ErrUnavailable
	}
	dir := filepath.Join(root, "runtime", fmt.Sprintf("%x", r.hash))
	path := filepath.Join(dir, r.name)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() &&
		info.Size() == int64(len(r.data)) && info.Mode().Perm()&0100 != 0 &&
		digest(path) == fmt.Sprintf("%x", r.hash) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".whisper-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(r.data)
	chmodErr := tmp.Chmod(0700)
	closeErr := tmp.Close()
	for _, err := range []error{writeErr, chmodErr, closeErr} {
		if err != nil {
			return "", err
		}
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
