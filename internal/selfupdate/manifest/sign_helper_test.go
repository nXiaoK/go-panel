package manifest

import "crypto/ed25519"

func ed25519Sign(priv ed25519.PrivateKey, message []byte) []byte {
	return ed25519.Sign(priv, message)
}
