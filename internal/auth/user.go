package auth

import "github.com/tuxedocurly/wledger/internal/db"

// User represents the context of the current requester.
type User struct {
	ID                     int64
	Email                  string
	Role                   string // "admin", "editor", "viewer", "guest"
	IsGuest                bool   // true if not logged in
	ChangePasswordRequired bool
}

// Guest returns a default anonymous user
func Guest() User {
	return User{IsGuest: true, Role: "guest"}
}

// IsAdmin returns true if the user has full system access.
func (u User) IsAdmin() bool {
	return u.Role == "admin"
}

// CanWrite returns true if the user can modify inventory (Editor or Admin).
func (u User) CanWrite() bool {
	return u.Role == "admin" || u.Role == "editor"
}

// CanDelete returns true if the user can delete items.
// (Currently, allow Editors to delete parts, but not hardware)
func (u User) CanDelete() bool {
	return u.Role == "admin" || u.Role == "editor"
}

// CanConfigure returns true if user can change hardware/system settings.
func (u User) CanConfigure() bool {
	return u.Role == "admin"
}

// CanRead determines if the user is allowed to view data.
// Returns true if authenticated, or if settings allow guests.
func (u User) CanRead(s db.Setting) bool {
	if u.IsAuthenticated() {
		return true
	}
	return !s.RequireAuthForRead.Bool
}

// IsAuthenticated serves as a quick check for login status
func (u User) IsAuthenticated() bool {
	return !u.IsGuest && u.ID != 0
}
