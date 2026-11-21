package speech

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The probe invokes the runtime without model arguments and distinguishes
// a reported absence of a GPU from a driver that stops responding.
func TestWhisperVulkanProbe(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
		deadline         time.Duration
	}{
		{"available", "exit 0", "", time.Second},
		{"unavailable", "exit 2", "no usable Vulkan GPU", time.Second},
		{"stalled", "exec sleep 10", "Vulkan device probe: context deadline exceeded", 100 * time.Millisecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "whisper")
			script := "#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = --mygo-probe-vulkan ] || exit 3\n" + tt.body + "\n"
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			err := runWhisperVulkanProbe(context.Background(), path, tt.deadline)
			if tt.want == "" && err != nil || tt.want != "" && (err == nil || err.Error() != tt.want) {
				t.Fatalf("probe error = %v, want %q", err, tt.want)
			}
			if tt.name == "stalled" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("probe should preserve the deadline error, got %v", err)
			}
		})
	}
}
