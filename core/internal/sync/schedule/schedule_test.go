package schedule

import (
	"testing"
	"time"
)

func at(t *testing.T, text string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestNextFiveField(t *testing.T) {
	expression, err := Parse("*/15 * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	from := at(t, "2026-10-10T08:07:30Z")
	next, err := expression.Next(from)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if want := at(t, "2026-10-10T08:15:00Z"); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestNextDaily(t *testing.T) {
	expression, err := Parse("30 4 * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	from := at(t, "2026-10-10T08:07:30Z")
	next, err := expression.Next(from)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if want := at(t, "2026-10-11T04:30:00Z"); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestNextSixFieldSeconds(t *testing.T) {
	expression, err := Parse("*/30 * * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !expression.HasSeconds() {
		t.Fatal("six-field expression should report seconds")
	}
	from := at(t, "2026-10-10T08:07:20Z")
	next, err := expression.Next(from)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if want := at(t, "2026-10-10T08:07:30Z"); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

// Without seconds the schedule fires on whole minutes, so a candidate inside
// the current minute rolls to the next one.
func TestNextSkipsCurrentMinute(t *testing.T) {
	expression, _ := Parse("* * * * *")
	next, err := expression.Next(at(t, "2026-10-10T08:07:30Z"))
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if want := at(t, "2026-10-10T08:08:00Z"); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

// When both day fields are restricted the schedule fires on a day matching
// either one, as Vixie cron does.
func TestDayOfMonthAndWeekdayUseEither(t *testing.T) {
	expression, err := Parse("0 0 1 * 1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 2026-09-21 is a Monday: the weekday matches, which is the earlier day.
	next, err := expression.Next(at(t, "2026-09-15T00:00:00Z"))
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if want := at(t, "2026-09-21T00:00:00Z"); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestSundayMatchesZeroAndSeven(t *testing.T) {
	// 2026-10-11 is a Sunday.
	for _, field := range []string{"0", "7"} {
		expression, err := Parse("0 3 * * " + field)
		if err != nil {
			t.Fatalf("parse %q: %v", field, err)
		}
		next, err := expression.Next(at(t, "2026-10-10T09:00:00Z"))
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		if want := at(t, "2026-10-11T03:00:00Z"); !next.Equal(want) {
			t.Fatalf("%q next = %v, want %v", field, next, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, text := range []string{
		"", "* * * *", "* * * * * * *", "60 * * * *", "* 24 * * *",
		"* * 0 * *", "* * * 13 *", "* * * * 8", "@daily", "*/0 * * * *",
		"a * * * *", "5-1 * * * *", "1,,2 * * * *",
	} {
		if _, err := Parse(text); err == nil {
			t.Fatalf("Parse(%q) succeeded, want error", text)
		}
	}
}

func TestNoRunReportsError(t *testing.T) {
	expression, err := Parse("0 0 31 2 *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := expression.Next(at(t, "2026-10-10T08:07:30Z")); err == nil {
		t.Fatal("February 31st should have no run")
	}
}

func TestMatchesWholeMinuteOnly(t *testing.T) {
	expression, _ := Parse("* * * * *")
	if !expression.Matches(at(t, "2026-10-10T08:07:00Z")) {
		t.Fatal("whole minute should match")
	}
	if expression.Matches(at(t, "2026-10-10T08:07:30Z")) {
		t.Fatal("mid-minute should not match a five-field expression")
	}
}
