package billing

import (
	"fmt"
	"time"
)

// AddPeriod advances a billing boundary by one calendar month or year.
// Days that do not exist in the target month are clamped to its last day.
func AddPeriod(value time.Time, interval string) (time.Time, error) {
	year, month, day := value.Date()
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

	lastDay := time.Date(targetYear, targetMonth+1, 0, 0, 0, 0, 0, value.Location()).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(
		targetYear, targetMonth, day,
		value.Hour(), value.Minute(), value.Second(), value.Nanosecond(),
		value.Location(),
	), nil
}
