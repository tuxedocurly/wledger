package store

import "time"

// parseTime attempts to parse a time string from SQLite in various formats
// fixes an issue with the SQLite drive crashing after a restore operation
func parseTime(tStr string) time.Time {
	// Try SQL standard format (common default)
	t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", tStr)
	if err == nil {
		return t
	}
	// Try without timezone
	t, err = time.Parse("2006-01-02 15:04:05", tStr)
	if err == nil {
		return t
	}
	// Try RFC3339 (common in JSON/Backups)
	t, err = time.Parse(time.RFC3339, tStr)
	if err == nil {
		return t
	}
	return time.Time{} // Return zero time on failure
}
