package billing

import (
	"fmt"
	"strings"
	"time"
)

func locationOrUTC(location *time.Location) *time.Location {
	if location == nil {
		return time.UTC
	}
	return location
}

// Location loads a configured IANA timezone. Empty values use Keygate's UTC default.
func Location(value string) (*time.Location, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.UTC, nil
	}
	location, err := time.LoadLocation(value)
	if err != nil {
		return nil, fmt.Errorf("invalid billing timezone %q: %w", value, err)
	}
	return location, nil
}

// EndOfDay returns 23:59:59 on value's calendar day in location.
func EndOfDay(value time.Time, location *time.Location) time.Time {
	location = locationOrUTC(location)
	local := value.In(location)
	year, month, day := local.Date()
	return time.Date(year, month, day, 23, 59, 59, 0, location)
}

// ParseExpiry accepts a calendar date or RFC3339 instant and normalizes it to
// the end of that date in the configured billing location.
func ParseExpiry(value string, location *time.Location) (time.Time, error) {
	location = locationOrUTC(location)
	value = strings.TrimSpace(value)
	if date, err := time.ParseInLocation("2006-01-02", value, location); err == nil {
		return EndOfDay(date, location), nil
	}
	instant, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expiry must be YYYY-MM-DD or an RFC 3339 timestamp")
	}
	return EndOfDay(instant, location), nil
}

// AddPeriod advances by one calendar month or year in location and returns
// 23:59:59 on the target date. Missing target days clamp to month end.
func AddPeriod(value time.Time, interval string, location *time.Location) (time.Time, error) {
	location = locationOrUTC(location)
	local := value.In(location)
	year, month, day := local.Date()
	targetYear, targetMonth := year, month
	switch interval {
	case "month":
		targetMonth++
		if targetMonth > time.December {
			targetMonth = time.January
			targetYear++
		}
	case "year":
		targetYear++
	default:
		return time.Time{}, fmt.Errorf("unsupported billing interval %q", interval)
	}

	lastDay := time.Date(targetYear, targetMonth+1, 0, 0, 0, 0, 0, location).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(
		targetYear, targetMonth, day,
		23, 59, 59, 0,
		location,
	), nil
}
