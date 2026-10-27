package hasher

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"time"

	"golang.org/x/crypto/pbkdf2"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/adapter/outbound/metrics"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/internal/core/port"
)

const (
	saltLen    = 16
	keyLen     = 64
	iterations = 210000
	algorithm  = "pbkdf2-sha512"
)

// PBKDF2Hasher implements port.PasswordHasher using PBKDF2-SHA512 with
// 210,000 iterations and a 16-byte random salt (LLD §10.6, C-4).
// The output matches Keycloak's credential format; RP-1 maps it to
// secretData/credentialData directly.
type PBKDF2Hasher struct {
	iters int
}

var _ port.PasswordHasher = (*PBKDF2Hasher)(nil)

// New creates a PBKDF2Hasher. iters must be >= 210000 (validated at startup).
func New(iters int) *PBKDF2Hasher {
	if iters <= 0 {
		iters = iterations
	}
	return &PBKDF2Hasher{iters: iters}
}

func (h *PBKDF2Hasher) Hash(password string) (*port.HashedPasswordSeed, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	start := time.Now()
	dk := pbkdf2.Key([]byte(password), salt, h.iters, keyLen, sha512.New)
	metrics.ObservePasswordHash(time.Since(start)) // LLD §11.2, §10.6

	seed := &port.HashedPasswordSeed{
		Algorithm:  algorithm,
		Iterations: h.iters,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Hash:       base64.StdEncoding.EncodeToString(dk),
	}

	// Zero sensitive data where practical
	for i := range dk {
		dk[i] = 0
	}

	return seed, nil
}
