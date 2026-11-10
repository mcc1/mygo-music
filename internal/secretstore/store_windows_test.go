package secretstore

import (
	"bytes"
	"os"
	"testing"
)

// DPAPI stores ciphertext on disk and can reopen it under the same user.
func TestDPAPISessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	for _, value := range []string{"synthetic-first-credential", "synthetic-next-credential"} {
		if err := store.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(string(store.(dpapiStore)))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(value)) {
			t.Fatal("DPAPI file contained plaintext")
		}
		got, err := Open(dir).Read()
		if err != nil || !bytes.Equal(got, []byte(value)) {
			t.Fatalf("reopen failed: %v", err)
		}
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if got, err := Open(dir).Read(); err != nil || len(got) != 0 {
		t.Fatalf("deleted session reopened: %v", err)
	}
}
