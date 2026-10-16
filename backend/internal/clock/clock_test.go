package clock_test

import (
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
)

func TestFakeClockAdvance(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var c clock.Clock = clock.NewFake(start)
	f := c.(*clock.Fake)

	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	f.Advance(90 * time.Second)
	if got, want := c.Now(), start.Add(90*time.Second); !got.Equal(want) {
		t.Fatalf("after Advance Now() = %v, want %v", got, want)
	}
	f.Advance(time.Nanosecond)
	if got, want := c.Now(), start.Add(90*time.Second+time.Nanosecond); !got.Equal(want) {
		t.Fatalf("after 1ns Advance Now() = %v, want %v", got, want)
	}
	reset := time.Date(2030, 6, 7, 8, 9, 10, 0, time.UTC)
	f.Set(reset)
	if got := c.Now(); !got.Equal(reset) {
		t.Fatalf("after Set Now() = %v, want %v", got, reset)
	}
}

func TestRealClockIsUTCAndCurrent(t *testing.T) {
	before := time.Now()
	got := clock.Real{}.Now()
	if got.Location() != time.UTC {
		t.Fatalf("Real.Now() location = %v, want UTC", got.Location())
	}
	if got.Before(before) || got.After(time.Now()) {
		t.Fatalf("Real.Now() = %v not within test window", got)
	}
}
