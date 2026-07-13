package tui

import (
	"strings"
	"testing"
)

func TestPadOrTruncate_ANSIStyledRowMeasuresVisibleWidthOnly(t *testing.T) {
	plain := ">* logs/"
	styled := reverseVideo(plain)

	gotPlain := PadOrTruncate(plain, 20)
	gotStyled := PadOrTruncate(styled, 20)

	if RuneLen(gotPlain) != 20 {
		t.Fatalf("PadOrTruncate(plain) visible width = %d, want 20", RuneLen(gotPlain))
	}
	if RuneLen(gotStyled) != 20 {
		t.Fatalf("PadOrTruncate(styled) visible width = %d, want 20 (ANSI codes must not count toward width)", RuneLen(gotStyled))
	}
	if StripANSI(gotPlain) != StripANSI(gotStyled) {
		t.Errorf("stripped forms differ: %q vs %q", StripANSI(gotPlain), StripANSI(gotStyled))
	}
}

func TestPadOrTruncate_TruncatesOverlongPlainText(t *testing.T) {
	got := PadOrTruncate("this is way too long", 6)
	if RuneLen(got) != 6 {
		t.Fatalf("RuneLen(%q) = %d, want 6", got, RuneLen(got))
	}
	if got != "this i" {
		t.Errorf("got %q, want %q", got, "this i")
	}
}

func TestPadOrTruncate_NegativeWidthTreatedAsZero(t *testing.T) {
	if got := PadOrTruncate("anything", -5); got != "" {
		t.Errorf("PadOrTruncate with negative width = %q, want empty", got)
	}
}

func TestBoxRow2_BothCellsAlignRegardlessOfANSIStyling(t *testing.T) {
	leftW, rightW := 20, 15
	plainRow := BoxRow2("plain left", "plain right", leftW, rightW)
	styledRow := BoxRow2(reverseVideo("styled left"), bold("styled right"), leftW, rightW)

	if got, want := RuneLen(plainRow), RuneLen(styledRow); got != want {
		t.Fatalf("stripped rendered width differs: plain=%d styled=%d", got, want)
	}
	if !strings.HasSuffix(StripANSI(plainRow), "│\n") || !strings.HasSuffix(StripANSI(styledRow), "│\n") {
		t.Fatalf("expected both rows to end with the right border: plain=%q styled=%q", plainRow, styledRow)
	}
}

func TestBoxLine_PadsToInnerWidthAndAddsBorders(t *testing.T) {
	got := StripANSI(BoxLine("hello", 20))
	if !strings.HasPrefix(got, "│ ") || !strings.HasSuffix(got, " │\n") {
		t.Fatalf("BoxLine(%q) = %q, want it wrapped in │ ... │", "hello", got)
	}
	// inner=20 means the content region (between "│ " and " │") is 18
	// visible runes wide.
	inner := strings.TrimSuffix(strings.TrimPrefix(got, "│ "), " │\n")
	if RuneLen(inner) != 18 {
		t.Errorf("content region width = %d, want 18", RuneLen(inner))
	}
}

func TestTopBorder_TitleFitsWithinWidth(t *testing.T) {
	got := StripANSI(TopBorder(" my title ", 40))
	if !strings.HasPrefix(got, "┌ my title ") {
		t.Fatalf("TopBorder = %q, want it to start with the title", got)
	}
	if !strings.HasSuffix(got, "┐\n") {
		t.Fatalf("TopBorder = %q, want it to end with ┐", got)
	}
	if RuneLen(strings.TrimSuffix(got, "\n")) != 40+2 {
		t.Errorf("TopBorder total width = %d, want %d (inner + 2 corners)", RuneLen(strings.TrimSuffix(got, "\n")), 40+2)
	}
}

func TestTopBorder_TitleLongerThanInnerIsTruncated(t *testing.T) {
	got := TopBorder("a title far too long to fit", 10)
	if RuneLen(strings.TrimSuffix(got, "\n")) != 10+2 {
		t.Errorf("TopBorder total width = %d, want %d even when the title overflows", RuneLen(strings.TrimSuffix(got, "\n")), 10+2)
	}
}

func TestBottomBorder_MatchesInnerWidth(t *testing.T) {
	got := StripANSI(BottomBorder(10))
	if got != "└"+strings.Repeat("─", 10)+"┘\n" {
		t.Errorf("BottomBorder(10) = %q", got)
	}
}

func TestDivider_MatchesInnerWidth(t *testing.T) {
	got := StripANSI(Divider(10))
	if got != "├"+strings.Repeat("─", 10)+"┤\n" {
		t.Errorf("Divider(10) = %q", got)
	}
}

func TestSplitAndMergeDividers_JoinAtTheMiddleColumn(t *testing.T) {
	split := StripANSI(SplitDivider(10, 8))
	if !strings.Contains(split, "┬") {
		t.Errorf("SplitDivider = %q, want it to contain ┬", split)
	}
	merge := StripANSI(MergeDivider(10, 8))
	if !strings.Contains(merge, "┴") {
		t.Errorf("MergeDivider = %q, want it to contain ┴", merge)
	}
	if RuneLen(strings.TrimSuffix(split, "\n")) != RuneLen(strings.TrimSuffix(merge, "\n")) {
		t.Errorf("SplitDivider/MergeDivider widths differ: %d vs %d", RuneLen(split), RuneLen(merge))
	}
}
