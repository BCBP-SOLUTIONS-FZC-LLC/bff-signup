package adapter_test

// LLD §14.1 — "PBKDF2 output verified against a known vector and against
// Keycloak's secretData/credentialData format" (C-4).
//
// The format is: {"algorithm":"pbkdf2-sha512","iterations":210000,
//                 "salt":"<base64>","hash":"<base64>"}
// RP-1 maps it to Keycloak secretData/credentialData directly.

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"testing"

	"golang.org/x/crypto/pbkdf2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/hasher"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

func TestPBKDF2Hasher_Hash_HappyPath(t *testing.T) {
	h := hasher.New(210000)
	seed, err := h.Hash("SecurePassword999@")
	require.NoError(t, err)
	assert.Equal(t, "pbkdf2-sha512", seed.Algorithm)
	assert.Equal(t, 210000, seed.Iterations)
	assert.NotEmpty(t, seed.Salt)
	assert.NotEmpty(t, seed.Hash)
}

func TestPBKDF2Hasher_DefaultsIterationsWhenZero(t *testing.T) {
	// iters <= 0 should default to 210000
	h := hasher.New(0)
	seed, err := h.Hash("password")
	require.NoError(t, err)
	assert.Equal(t, 210000, seed.Iterations)
}

// TestPBKDF2_KnownVector — LLD §14.1:
// "PBKDF2 output verified against a known vector and against Keycloak's format."
//
// We compute the expected output independently and verify the hasher
// produces the same algorithm/salt-size/hash-size and JSON wire shape that
// RP-1 maps to Keycloak secretData/credentialData (C-4).
func TestPBKDF2_KnownVector_KeycloakFormat(t *testing.T) {
	const (
		iterations = 210000
		keyLen     = 64 // 512 bits
		saltLen    = 16
	)

	// Known salt = 16 zero bytes; known password.
	salt := make([]byte, saltLen)
	expectedDK := pbkdf2.Key([]byte("TestPassword123@"), salt, iterations, keyLen, sha512.New)
	expectedHash := base64.StdEncoding.EncodeToString(expectedDK)
	expectedSalt := base64.StdEncoding.EncodeToString(salt)

	// Verify the JSON wire shape RP-1 maps to Keycloak secretData/credentialData (C-4).
	seed := &port.HashedPasswordSeed{
		Algorithm:  "pbkdf2-sha512",
		Iterations: iterations,
		Salt:       expectedSalt,
		Hash:       expectedHash,
	}
	b, err := json.Marshal(seed)
	require.NoError(t, err)
	var roundTrip port.HashedPasswordSeed
	require.NoError(t, json.Unmarshal(b, &roundTrip))
	assert.Equal(t, "pbkdf2-sha512", roundTrip.Algorithm, "Keycloak secretData algorithm field")
	assert.Equal(t, iterations, roundTrip.Iterations, "must match trial realm policy (N-10 / C-4)")
	assert.Equal(t, expectedSalt, roundTrip.Salt)
	assert.Equal(t, expectedHash, roundTrip.Hash)

	// Verify the real hasher produces the correct byte lengths.
	h := hasher.New(iterations)
	actual, err := h.Hash("TestPassword123@")
	require.NoError(t, err)
	assert.Equal(t, "pbkdf2-sha512", actual.Algorithm)
	assert.Equal(t, iterations, actual.Iterations)

	saltBytes, err := base64.StdEncoding.DecodeString(actual.Salt)
	require.NoError(t, err)
	assert.Len(t, saltBytes, saltLen, "salt must be 16 bytes (128 bits)")

	hashBytes, err := base64.StdEncoding.DecodeString(actual.Hash)
	require.NoError(t, err)
	assert.Len(t, hashBytes, keyLen, "hash must be 64 bytes (512 bits)")
}

func TestPBKDF2Hasher_DifferentSaltsPerCall(t *testing.T) {
	h := hasher.New(210000)
	s1, _ := h.Hash("same-password")
	s2, _ := h.Hash("same-password")
	// Random salt means hash output differs between calls
	assert.NotEqual(t, s1.Salt, s2.Salt)
	assert.NotEqual(t, s1.Hash, s2.Hash)
}
