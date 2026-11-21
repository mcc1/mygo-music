package speech

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CI can require a real bundled CPU executable, including a run with CPU extensions disabled.
func TestWhisperPackagedCPU(t *testing.T) {
	if os.Getenv("MYGO_REQUIRE_WHISPER_BUNDLE") == "" {
		t.Skip("set MYGO_REQUIRE_WHISPER_BUNDLE to check the packaged executable")
	}
	for _, backend := range []string{"cpu", "cpu-avx2", "vulkan"} {
		if len(linuxWhisperRuntimes[backend].data) == 0 {
			t.Fatalf("missing packaged backend %s", backend)
		}
	}
	r := linuxWhisperCPURuntime()
	if want := os.Getenv("WHISPER_EXPECT_CPU_RUNTIME"); want != "" && r.name != want {
		t.Fatalf("CPU executable = %s, want %s", r.name, want)
	}
	path, err := r.install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, path, "--help").CombinedOutput(); err != nil {
		t.Fatalf("packaged CPU executable: %v, %s", err, output)
	}
	t.Logf("CPU executable = %s", r.name)
}

// A saved CUDA selection from Windows must not silently enable Auto on Linux.
func TestWhisperLinuxRejectsCUDA(t *testing.T) {
	oldWeights, oldRuntimes := whisperWeights, linuxWhisperRuntimes
	t.Cleanup(func() { whisperWeights, linuxWhisperRuntimes = oldWeights, oldRuntimes })
	whisperWeights.size = 1
	linuxWhisperRuntimes = map[string]linuxWhisperRuntime{"cpu": {data: []byte("cpu")}}
	root := t.TempDir()
	dir := filepath.Join(root, WhisperModelName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, whisperWeights.path), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"cuda", "obsolete"} {
		if _, err := NewWhisperWithBackend(root, backend); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s selection error = %v", backend, err)
		}
	}
}

// Cancelling a Vulkan attempt must not launch the CPU fallback.
func TestWhisperLinuxCancellation(t *testing.T) {
	old := linuxWhisperRuntimes
	t.Cleanup(func() { linuxWhisperRuntimes = old })
	root := t.TempDir()
	ready, calls := filepath.Join(root, "ready"), filepath.Join(root, "calls")
	t.Setenv("MYGO_WHISPER_TEST_READY", ready)
	t.Setenv("MYGO_WHISPER_TEST_LOG", calls)
	data := []byte("#!/bin/sh\necho probe >> \"$MYGO_WHISPER_TEST_LOG\"\necho ready > \"$MYGO_WHISPER_TEST_READY\"\nexec sleep 30\n")
	cpu := []byte("#!/bin/sh\necho cpu >> \"$MYGO_WHISPER_TEST_LOG\"\nexit 3\n")
	linuxWhisperRuntimes = map[string]linuxWhisperRuntime{
		"vulkan": {data, sha256.Sum256(data), "whisper-cli-vulkan"},
		"cpu":    {cpu, sha256.Sum256(cpu), "whisper-cli-cpu"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Whisper{dir: root, backends: []string{"vulkan"}}
	done := make(chan error, 1)
	go func() { done <- w.transcribeChunk(ctx, root, []float32{0}, 0, time.Second, func(Segment) {}) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("probe exited before starting: %v", err)
		case <-deadline.C:
			t.Fatal("probe did not signal readiness")
		case <-tick.C:
		}
	}
	cancel()
	var err error
	select {
	case err = <-done:
	case <-deadline.C:
		t.Fatal("cancelled probe did not exit")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled transcription = %v", err)
	}
	if len(w.backends) != 1 {
		t.Fatal("cancellation discarded the GPU backend")
	}
	got, readErr := os.ReadFile(calls)
	if readErr != nil || string(got) != "probe\n" {
		t.Fatalf("cancelled runtime calls = %q, %v", got, readErr)
	}
}

// An opt-in real-runtime check verifies the packaged backend produces timed speech segments.
func TestWhisperTranscribeIntegration(t *testing.T) {
	root, wav := os.Getenv("WHISPER_ROOT"), os.Getenv("WHISPER_WAV")
	if root == "" || wav == "" {
		t.Skip("set WHISPER_ROOT and WHISPER_WAV to test the packaged runtime")
	}
	w, err := NewWhisperWithLanguage(root, os.Getenv("WHISPER_TEST_BACKEND"), "en")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var segments []Segment
	var heard time.Duration
	err = w.Transcribe(ctx, openWAV(t, wav), 0, 0, func(s Segment) { segments = append(segments, s) }, func(at time.Duration) { heard = at })
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) == 0 || heard <= 0 {
		t.Fatalf("no timed speech: %v, heard %v", segments, heard)
	}
	for _, s := range segments {
		if s.Start < 0 || s.End <= s.Start || s.End > heard {
			t.Fatalf("bad segment: %v, heard %v", s, heard)
		}
		t.Logf("%v-%v %s", s.Start, s.End, s.Text)
	}
	if want := os.Getenv("WHISPER_EXPECT_BACKEND"); want != "" && w.Backend() != want {
		t.Fatalf("backend = %q, want %q", w.Backend(), want)
	}
	t.Logf("backend = %s, heard = %v", w.Backend(), heard)
}

// A damaged executable with the right size must not be reused from the runtime cache.
func TestWhisperRuntimeRepairsChangedExecutable(t *testing.T) {
	data := []byte("#!/bin/sh\nexit 0\n")
	r := linuxWhisperRuntime{data: data, hash: sha256.Sum256(data), name: "whisper-cli-cpu"}
	root := t.TempDir()
	path, err := r.install(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", len(data))), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := r.install(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("cached executable was not repaired: %q, %v", got, err)
	}
}

// Auto probes before loading a model, while a forced Vulkan backend fails without using CPU.
func TestWhisperLinuxBackendSelection(t *testing.T) {
	for _, tc := range []struct {
		name, forced, probeExit, runExit, wantBackend, wantLog string
		wantError                                              bool
	}{
		{"vulkan", "", "0", "0", "vulkan", "probe\nvulkan\nvulkan", false},
		{"no GPU", "", "2", "0", "cpu", "probe\ncpu\ncpu", false},
		{"GPU run fails", "", "0", "1", "cpu", "probe\nvulkan\ncpu\ncpu", false},
		{"forced no GPU", "vulkan", "2", "0", "", "probe", true},
		{"forced GPU run fails", "vulkan", "0", "1", "", "probe\nvulkan", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			logPath := filepath.Join(root, "calls")
			t.Setenv("MYGO_WHISPER_TEST_LOG", logPath)
			old := linuxWhisperRuntimes
			t.Cleanup(func() { linuxWhisperRuntimes = old })
			linuxWhisperRuntimes = make(map[string]linuxWhisperRuntime)
			for _, backend := range []string{"cpu", "vulkan"} {
				script := "#!/bin/sh\n"
				if backend == "vulkan" {
					script += "if [ \"$1\" = --mygo-probe-vulkan ]; then\n echo probe >> \"$MYGO_WHISPER_TEST_LOG\"\n exit " + tc.probeExit + "\nfi\n"
				}
				script += "echo " + backend + " >> \"$MYGO_WHISPER_TEST_LOG\"\n"
				if backend == "vulkan" {
					script += "[ " + tc.runExit + " = 0 ] || exit " + tc.runExit + "\necho 'using vulkan' >&2\n"
				}
				script += "while [ \"$#\" -gt 0 ]; do\n if [ \"$1\" = -of ]; then\n  printf '%s' '{\"transcription\":[{\"text\":\"hello\",\"offsets\":{\"from\":0,\"to\":1000}}]}' > \"$2.json\"\n  exit 0\n fi\n shift\ndone\nexit 3\n"
				data := []byte(script)
				linuxWhisperRuntimes[backend] = linuxWhisperRuntime{data, sha256.Sum256(data), "whisper-cli-" + backend}
			}
			w := &Whisper{dir: root, language: "zh", forced: tc.forced, backends: []string{"vulkan"}}
			var segments []Segment
			for range 2 {
				err := w.transcribeChunk(context.Background(), root, []float32{0}, 0, time.Second, func(s Segment) { segments = append(segments, s) })
				if (err != nil) != tc.wantError {
					t.Fatalf("transcribe error = %v, want error %v", err, tc.wantError)
				}
				if tc.wantError {
					break
				}
			}
			got, err := os.ReadFile(logPath)
			if err != nil || strings.TrimSpace(string(got)) != tc.wantLog {
				t.Fatalf("runtime calls = %q, %v; want %q", got, err, tc.wantLog)
			}
			if w.Backend() != tc.wantBackend || !tc.wantError && len(segments) != 2 {
				t.Fatalf("backend = %q, segments = %v", w.Backend(), segments)
			}
		})
	}
}
