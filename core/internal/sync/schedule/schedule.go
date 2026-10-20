// Package schedule parses the cron expressions the sync settings accept and
// computes the next run. It is deliberately small and strict: the field set is
// the classic five-field form plus an optional leading seconds field, with
// lists, ranges, steps, and stars. Names, @shortcuts, and the "?" character are
// not accepted, so a stored expression always means the same thing.
//
// Day-of-month and day-of-week follow the classic rule: when both are
// restricted the schedule fires on a day matching either one, not both.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Expression is a parsed schedule.
type Expression struct {
	// raw is the accepted text, kept for error messages.
	raw  string
	secs []bool // 0-59, only used when hasSeconds
	mins []bool // 0-59
	hour []bool // 0-23
	dom  []bool // 1-31
	mon  []bool // 1-12
	dow  []bool // 0-6, Sunday = 0
	// hasSeconds says whether the leading seconds field was given.
	hasSeconds bool
	// domRestricted and dowRestricted decide the either-day rule.
	domRestricted, dowRestricted bool
}

// Parse reads a five-field ("minute hour day month weekday") or six-field
// ("second minute hour day month weekday") expression.
func Parse(text string) (Expression, error) {
	fields := strings.Fields(text)
	if len(fields) != 5 && len(fields) != 6 {
		return Expression{}, fmt.Errorf("cron expression needs 5 or 6 fields, got %d", len(fields))
	}
	expression := Expression{raw: text, hasSeconds: len(fields) == 6}
	if expression.hasSeconds {
		values, err := parseField(fields[0], 0, 59)
		if err != nil {
			return Expression{}, fmt.Errorf("cron seconds: %w", err)
		}
		expression.secs = values
		fields = fields[1:]
	}
	var err error
	if expression.mins, err = parseField(fields[0], 0, 59); err != nil {
		return Expression{}, fmt.Errorf("cron minutes: %w", err)
	}
	if expression.hour, err = parseField(fields[1], 0, 23); err != nil {
		return Expression{}, fmt.Errorf("cron hours: %w", err)
	}
	if expression.dom, err = parseField(fields[2], 1, 31); err != nil {
		return Expression{}, fmt.Errorf("cron day of month: %w", err)
	}
	if expression.mon, err = parseField(fields[3], 1, 12); err != nil {
		return Expression{}, fmt.Errorf("cron month: %w", err)
	}
	if expression.dow, err = parseField(fields[4], 0, 7); err != nil {
		return Expression{}, fmt.Errorf("cron weekday: %w", err)
	}
	// 7 and 0 both mean Sunday; fold 7 onto 0 so matching is one lookup.
	expression.dow[0] = expression.dow[0] || expression.dow[7]
	expression.dow = expression.dow[:7]
	expression.domRestricted = strings.TrimSpace(fields[2]) != "*"
	expression.dowRestricted = strings.TrimSpace(fields[4]) != "*"
	return expression, nil
}

// HasSeconds reports whether the expression carries a seconds field, so the
// caller knows whether a match can happen more than once a minute.
func (expression Expression) HasSeconds() bool { return expression.hasSeconds }

func parseField(text string, min, max int) ([]bool, error) {
	values := make([]bool, max+1)
	for _, part := range strings.Split(text, ",") {
		if part == "" {
			return nil, fmt.Errorf("empty list item")
		}
		step := 1
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			stepText := part[slash+1:]
			parsed, err := strconv.Atoi(stepText)
			if err != nil || parsed < 1 {
				return nil, fmt.Errorf("step %q is not a positive number", stepText)
			}
			step = parsed
			part = part[:slash]
		}
		low, high := min, max
		switch {
		case part == "*":
		case strings.ContainsRune(part, '-'):
			rangeText := strings.SplitN(part, "-", 2)
			lowValue, err := strconv.Atoi(rangeText[0])
			if err != nil {
				return nil, fmt.Errorf("range start %q is not a number", rangeText[0])
			}
			highValue, err := strconv.Atoi(rangeText[1])
			if err != nil {
				return nil, fmt.Errorf("range end %q is not a number", rangeText[1])
			}
			if lowValue < min || highValue > max || lowValue > highValue {
				return nil, fmt.Errorf("range %q is outside %d-%d", part, min, max)
			}
			low, high = lowValue, highValue
		default:
			value, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", part)
			}
			if value < min || value > max {
				return nil, fmt.Errorf("%d is outside %d-%d", value, min, max)
			}
			// A single value with a step is a range from the value to the
			// field maximum, as Vixie cron reads it.
			if step > 1 && value+step <= max {
				low = value
			} else {
				low, high = value, value
			}
		}
		for value := low; value <= high; value += step {
			values[value] = true
		}
	}
	return values, nil
}

// Next returns the first instant strictly after from that the expression
// matches, in from's location. It searches at most five years ahead and
// reports an error when no match exists in that window (for example the 31st
// of a month that never has one).
func (expression Expression) Next(from time.Time) (time.Time, error) {
	if len(expression.mins) == 0 {
		return time.Time{}, fmt.Errorf("cron expression is empty")
	}
	candidate := from.Truncate(time.Second).Add(time.Second)
	deadline := from.Add(5 * 366 * 24 * time.Hour)
	if expression.hasSeconds {
		for ; candidate.Before(deadline); candidate = candidate.Add(time.Second) {
			if expression.matches(candidate) {
				return candidate, nil
			}
		}
		return time.Time{}, fmt.Errorf("cron expression %q has no run in the next five years", expression.raw)
	}
	// A five-field expression fires on a whole minute, so the first candidate
	// strictly after from is the next minute boundary.
	candidate = from.Truncate(time.Minute).Add(time.Minute)
	for ; candidate.Before(deadline); candidate = candidate.Add(time.Minute) {
		if expression.matches(candidate) {
			return candidate, nil
		}
	}
	return time.Time{}, fmt.Errorf("cron expression %q has no run in the next five years", expression.raw)
}

// Matches reports whether the instant is exactly on the schedule. For a
// five-field expression only the minute matters; seconds are ignored.
func (expression Expression) Matches(at time.Time) bool {
	if !expression.hasSeconds && at.Second() != 0 {
		return false
	}
	return expression.matches(at)
}

func (expression Expression) matches(at time.Time) bool {
	if expression.hasSeconds && !expression.secs[at.Second()] {
		return false
	}
	if !expression.mins[at.Minute()] || !expression.hour[at.Hour()] || !expression.mon[int(at.Month())] {
		return false
	}
	dayOfMonth := expression.dom[at.Day()]
	dayOfWeek := expression.dow[int(at.Weekday())]
	switch {
	case expression.domRestricted && expression.dowRestricted:
		return dayOfMonth || dayOfWeek
	case expression.domRestricted:
		return dayOfMonth
	case expression.dowRestricted:
		return dayOfWeek
	default:
		return true
	}
}
