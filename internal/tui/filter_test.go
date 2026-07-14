package tui

import "testing"

func TestFilterableWindowHeight_DescriptionCostsTwoRowsLikeHeader(t *testing.T) {
	base := filterableWindowHeight(24, false, false)
	withHeader := filterableWindowHeight(24, true, false)
	withDescription := filterableWindowHeight(24, false, true)
	withBoth := filterableWindowHeight(24, true, true)

	if base-withHeader != headerChromeRows {
		t.Errorf("header cost = %d rows, want %d", base-withHeader, headerChromeRows)
	}
	if base-withDescription != descriptionChromeRows {
		t.Errorf("description cost = %d rows, want %d", base-withDescription, descriptionChromeRows)
	}
	if base-withBoth != headerChromeRows+descriptionChromeRows {
		t.Errorf("header+description cost = %d rows, want %d", base-withBoth, headerChromeRows+descriptionChromeRows)
	}
}

func TestFilterableWindowHeight_NeverBelowMinimum(t *testing.T) {
	got := filterableWindowHeight(1, true, true)
	if got != minListViewRows {
		t.Errorf("got %d, want the floor %d", got, minListViewRows)
	}
}
