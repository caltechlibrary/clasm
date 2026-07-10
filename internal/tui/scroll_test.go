package tui

import "testing"

func TestScrollWindow_FitsEverythingWhenShorterThanWindow(t *testing.T) {
	start, end := ScrollWindow(2, 5, 10)
	if start != 0 || end != 5 {
		t.Errorf("ScrollWindow(2, 5, 10) = (%d, %d), want (0, 5)", start, end)
	}
}

func TestScrollWindow_FollowsCursorPastTheBottom(t *testing.T) {
	// 100 items, a 10-row window, cursor near the end -- the window
	// must include the cursor, not stay pinned to the top.
	start, end := ScrollWindow(95, 100, 10)
	if end <= 95 {
		t.Fatalf("ScrollWindow(95, 100, 10) = (%d, %d), cursor 95 falls outside the window", start, end)
	}
	if start < 0 || end > 100 {
		t.Fatalf("ScrollWindow(95, 100, 10) = (%d, %d), out of bounds", start, end)
	}
}

func TestScrollWindow_NeverExceedsWindowHeight(t *testing.T) {
	for _, cursor := range []int{0, 1, 50, 98, 99} {
		start, end := ScrollWindow(cursor, 100, 10)
		if end-start != 10 {
			t.Errorf("ScrollWindow(%d, 100, 10) window size = %d, want 10", cursor, end-start)
		}
		if cursor < start || cursor >= end {
			t.Errorf("ScrollWindow(%d, 100, 10) = (%d, %d), cursor not inside window", cursor, start, end)
		}
	}
}

func TestScrollWindow_ZeroOrNegativeHeightTreatedAsOne(t *testing.T) {
	start, end := ScrollWindow(0, 10, 0)
	if end-start != 1 {
		t.Errorf("ScrollWindow with height=0 window size = %d, want 1", end-start)
	}
}
