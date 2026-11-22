package config

import (
	"fmt"
	"time"
)

// Defaults of the limits that end a full history load. A chat that reaches
// either one counts as completely loaded.
const (
	// DefaultHistoryLimitSince is the date, as YYYY-MM-DD, before which a full
	// load does not go.
	DefaultHistoryLimitSince = "2022-02-02"
	// DefaultHistoryLimitMessages is the number of saved messages of one chat at
	// which a full load stops.
	DefaultHistoryLimitMessages = 200000
	// MaxHistoryLimitMessages bounds history_limit.messages.
	MaxHistoryLimitMessages = 100000000
)

const historyLimitDateLayout = "2006-01-02"

// ParseHistoryLimitSince reads history_limit.since: a calendar date as
// YYYY-MM-DD, taken as 00:00 UTC of that day, or an empty string for no date
// limit (the zero time).
func ParseHistoryLimitSince(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(historyLimitDateLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be a date as YYYY-MM-DD, or empty for no date limit")
	}
	return t, nil
}

// CheckHistoryLimitMessages checks history_limit.messages: 0 turns the count
// limit off.
func CheckHistoryLimitMessages(n int) error {
	if n < 0 || n > MaxHistoryLimitMessages {
		return fmt.Errorf("must be a whole number from 0 to %d; 0 turns the limit off", MaxHistoryLimitMessages)
	}
	return nil
}
