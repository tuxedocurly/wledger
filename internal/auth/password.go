package auth

import (
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// General note on "Why bcrypt?":
// I considered using Argon2 here instead; however, since this server is
// expected to be performant on low-end, memory constrained hardware (rpi),
// I didn't see much benefit in using Argon2 over bcrypt given I would need
// to reduce the Argon2 parameters to make it usable in the worst-case,
// resource constrained scenario.

const (
	BcryptPasswordCost = 12
	MinPasswordLength  = 8
	MaxBcryptBytes     = 72 // bcrypt has a hard limit of 72 bytes
)

// HashPassword validates the password length and generates a bcrypt hash.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), BcryptPasswordCost)
	return string(bytes), err
}

// CheckPassword compares a hash with a plaintext password
// returns true if matched, false otherwise
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ValidatePassword enforces complexity rules (e.g. min length)
func ValidatePassword(password string) error {
	// check if password exceeds bcrypt byte size limit
	if len(password) > MaxBcryptBytes {
		return fmt.Errorf("password is too long (max %d bytes)", MaxBcryptBytes)
	}
	// check rune count (characters) for user-friendly validation
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters long", MinPasswordLength)
	}
	return nil
}
