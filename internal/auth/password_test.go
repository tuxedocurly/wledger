package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "empty", password: "", wantErr: true},
		{name: "too_short_7_bytes", password: "1234567", wantErr: true},
		{name: "min_8_bytes", password: "12345678", wantErr: false},
		{name: "max_72_bytes", password: strings.Repeat("a", 72), wantErr: false},
		{name: "too_long_73_bytes", password: strings.Repeat("a", 73), wantErr: true},
		// Multi-byte UTF-8: 25 * 3 = 75 bytes > 72 byte bcrypt limit
		{name: "multibyte_over_72_bytes", password: strings.Repeat("€", 25), wantErr: true},
		// Multi-byte UTF-8: 3 runes < 8 characters even though 9 bytes
		{name: "multibyte_under_8_chars", password: strings.Repeat("€", 3), wantErr: true},
		// Multi-byte UTF-8: 8 runes = 24 bytes <= 72 byte bcrypt limit
		{name: "multibyte_8_chars_24_bytes", password: strings.Repeat("€", 8), wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidatePassword(%d bytes) error = %v, wantErr %v", len(tc.password), err, tc.wantErr)
			}
		})
	}
}

func TestHashAndCheckPassword(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("expected error hashing empty password")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected error hashing short password")
	}
	if _, err := HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Fatal("expected error hashing >72 byte password")
	}

	pw := "correct-horse-battery"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("bcrypt.Cost failed: %v", err)
	}
	if cost != BcryptPasswordCost {
		t.Fatalf("bcrypt cost = %d, want %d", cost, BcryptPasswordCost)
	}

	if !CheckPassword(pw, hash) {
		t.Fatal("CheckPassword returned false for matching password")
	}
	if CheckPassword("wrong-password", hash) {
		t.Fatal("CheckPassword returned true for mismatched password")
	}
}
