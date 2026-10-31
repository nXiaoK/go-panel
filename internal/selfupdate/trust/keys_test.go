package trust

import (
	"testing"

	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
)

func TestReleaseKeysAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, encoded := range releaseKeys {
		key, err := manifest.ParsePublicKey(encoded)
		if err != nil {
			t.Fatalf("embedded release key %q is invalid: %v", encoded, err)
		}
		id := manifest.KeyID(key)
		if seen[id] {
			t.Fatalf("embedded release key %s is duplicated", id)
		}
		seen[id] = true
	}
	if got := len(releaseKeys); Default().Len() < got {
		t.Fatalf("Default() dropped embedded keys: %d < %d", Default().Len(), got)
	}
	if Configured() != (Default().Len() > 0) {
		t.Fatal("Configured must reflect whether any key is trusted")
	}
}
