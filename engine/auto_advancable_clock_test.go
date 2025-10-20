package engine_test

import (
	"testing"
	"time"

	"twimulator/engine"
)

func TestAutoAdvancableClock(t *testing.T) {
	clock := engine.NewAutoAdvancableClock()

	// Get initial time
	start := clock.Now()

	// Wait a bit (real time passes)
	time.Sleep(10 * time.Millisecond)

	// Time should have progressed naturally
	afterSleep := clock.Now()
	if !afterSleep.After(start) {
		t.Errorf("Expected time to progress naturally, got start=%v, afterSleep=%v", start, afterSleep)
	}

	// Now advance manually
	clock.Advance(5 * time.Second)

	// Time should reflect both real time and the advance
	afterAdvance := clock.Now()
	elapsed := afterAdvance.Sub(start)

	// Should be at least 5 seconds (from advance) plus the sleep time
	if elapsed < 5*time.Second {
		t.Errorf("Expected at least 5 seconds elapsed, got %v", elapsed)
	}

	// Advance again
	clock.Advance(10 * time.Second)
	afterSecondAdvance := clock.Now()

	// Should be at least 15 seconds from start
	totalElapsed := afterSecondAdvance.Sub(start)
	if totalElapsed < 15*time.Second {
		t.Errorf("Expected at least 15 seconds elapsed after two advances, got %v", totalElapsed)
	}
}

func TestAutoAdvancableClockWithEngine(t *testing.T) {
	// Create engine with auto-advancable clock
	e := engine.NewEngine(engine.WithAutoAdvancableClock())
	defer e.Close()

	// Get time at creation
	start := e.Clock().Now()

	// Wait a bit for real time to pass
	time.Sleep(10 * time.Millisecond)

	// Time should have progressed
	afterSleep := e.Clock().Now()
	if !afterSleep.After(start) {
		t.Errorf("Expected time to progress, got start=%v, after=%v", start, afterSleep)
	}

	// Advance the engine's clock manually
	e.Advance(1 * time.Hour)

	// Time should now be about 1 hour ahead
	afterAdvance := e.Clock().Now()
	elapsed := afterAdvance.Sub(start)

	if elapsed < 1*time.Hour {
		t.Errorf("Expected at least 1 hour elapsed, got %v", elapsed)
	}
}
