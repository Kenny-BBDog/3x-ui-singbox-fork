package sub

import (
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// realityPublicKeyFromPrivate derives the client-side REALITY public key from
// the inbound's server private key.
//
// A REALITY inbound stores only realitySettings.privateKey; the matching
// publicKey normally lives in realitySettings.settings.publicKey, which the
// panel's inbound form writes. An inbound created by hand, by an import, or by
// a node sync can lack that field, and every subscription then silently ships a
// REALITY node with no public-key — mihomo rejects the whole profile with
// "'reality-opts' has unset fields: public-key" and share links lose `pbk`.
// Deriving it here makes those inbounds work instead of poisoning the profile.
//
// Xray derives the same value with curve25519 (RFC 7748, clamped scalar), and
// REALITY keys are URL-safe base64 without padding.
func realityPublicKeyFromPrivate(privateKey string) string {
	priv, ok := decodeRealityKey(privateKey)
	if !ok {
		return ""
	}
	var pub [32]byte
	curve25519.ScalarBaseMult(&pub, &priv)
	return base64.RawURLEncoding.EncodeToString(pub[:])
}

// decodeRealityKey accepts a URL-safe or standard base64 32-byte key with or
// without padding, which covers every form the panel or an operator may store.
func decodeRealityKey(key string) ([32]byte, bool) {
	var out [32]byte
	key = strings.TrimSpace(key)
	if key == "" {
		return out, false
	}
	trimmed := strings.TrimRight(key, "=")
	encodings := []*base64.Encoding{
		base64.RawURLEncoding,
		base64.RawStdEncoding,
	}
	if strings.ContainsAny(trimmed, "+/") {
		encodings = []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding}
	}
	for _, enc := range encodings {
		if raw, err := enc.DecodeString(trimmed); err == nil && len(raw) == 32 {
			copy(out[:], raw)
			return out, true
		}
	}
	return out, false
}

// realityPublicKeyFor resolves the public key for a REALITY inbound's
// client-side settings block, preferring the stored value and falling back to a
// derivation from the inbound's own private key.
func realityPublicKeyFor(clientSettings, inboundReality map[string]any) string {
	if clientSettings != nil {
		if pk, ok := clientSettings["publicKey"].(string); ok && strings.TrimSpace(pk) != "" {
			return pk
		}
	}
	if inboundReality != nil {
		if priv, ok := inboundReality["privateKey"].(string); ok && strings.TrimSpace(priv) != "" {
			return realityPublicKeyFromPrivate(priv)
		}
	}
	return ""
}
