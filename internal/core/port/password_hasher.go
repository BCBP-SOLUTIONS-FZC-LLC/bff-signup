package port

// HashedPasswordSeed is the JSON credential representation sent to RP-1 (LLD §10.6, C-4).
// RP-1 maps it to Keycloak secretData/credentialData so the user can log in.
type HashedPasswordSeed struct {
	Algorithm  string `json:"algorithm"`  // "pbkdf2-sha512"
	Iterations int    `json:"iterations"` // 210000
	Salt       string `json:"salt"`       // base64 16 bytes
	Hash       string `json:"hash"`       // base64 64 bytes
}

// PasswordHasher hashes a plain-text password into the RP-1 seed format.
// The plain password and resulting seed are zeroed after use where practical
// and are never logged, stored in Valkey, or sent anywhere except RP-1 (SB-INV-2).
type PasswordHasher interface {
	Hash(password string) (*HashedPasswordSeed, error)
}
