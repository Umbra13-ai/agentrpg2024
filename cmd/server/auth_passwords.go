package main

import (
	"golang.org/x/crypto/bcrypt"
)

// Passwords are optional. When one is set it is stored as a bcrypt hash (the
// salt column is left empty). The old salted SHA-256 hashes are no longer
// accepted. An empty hash means a token-only account and never matches.

// newPasswordHash returns the stored hash and salt for a new password.
func newPasswordHash(password string) (hash, salt string) {
	pw := []byte(password)
	if len(pw) > 72 { // bcrypt reads at most 72 bytes
		pw = pw[:72]
	}
	b, err := bcrypt.GenerateFromPassword(pw, bcrypt.DefaultCost)
	if err != nil {
		return "", "" // no password is set rather than a weak one
	}
	return string(b), ""
}

// checkPassword reports whether the password matches a bcrypt hash.
func checkPassword(password, hash string) bool {
	if len(hash) < 4 || hash[:2] != "$2" {
		return false
	}
	pw := []byte(password)
	if len(pw) > 72 {
		pw = pw[:72]
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), pw) == nil
}
