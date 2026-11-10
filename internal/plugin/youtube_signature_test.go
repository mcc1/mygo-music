package plugin

import (
	"context"
	"crypto/sha3"
	"encoding/hex"
	"net/url"
	"testing"
)

// The embedded release files must match the published solver hashes.
func TestYouTubeSignatureSolverAssets(t *testing.T) {
	for _, asset := range []struct {
		source []byte
		hash   string
	}{
		{youtubeSolverCore, "ee5b307d07f55e91e4723edf5ac205cc877a474187849d757dc1322e38427b157a9d706d510c1723d3670f98e5a3f8cbcde77874a80406bd7204bc9fea30f283"},
		{youtubeSolverLib, "8420c259ad16e99ce004e4651ac1bcabb53b4457bf5668a97a9359be9a998a789fee8ab124ee17f91a2ea8fd84e0f2b2fc8eabcaf0b16a186ba734cf422ad053"},
	} {
		sum := sha3.Sum512(asset.source)
		if got := hex.EncodeToString(sum[:]); got != asset.hash {
			t.Errorf("solver hash %s, want %s", got, asset.hash)
		}
	}
}

// A cipher for another host or endpoint is rejected before reading the player.
func TestYouTubeSignatureRejectsNonMediaURLs(t *testing.T) {
	for _, address := range []string{
		"https://googlevideo.com.example.com/videoplayback",
		"https://example.com/videoplayback",
		"http://rr.example.googlevideo.com/videoplayback",
		"https://somebody@rr.example.googlevideo.com/videoplayback",
		"https://rr.example.googlevideo.com:443/videoplayback",
		"https://rr.example.googlevideo.com/elsewhere",
	} {
		cipher := url.Values{"url": {address}, "s": {"challenge"}, "sp": {"sig"}}.Encode()
		if _, err := decipherYouTubeAudio(context.Background(), 1, cipher); err != errYouTubeSignature {
			t.Errorf("%s: got %v", address, err)
		}
	}
}
