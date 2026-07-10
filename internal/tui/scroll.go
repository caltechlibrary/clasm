package tui

// ScrollWindow returns the [start, end) slice bounds of total items to
// show in a windowHeight-tall viewport, keeping cursor visible and
// roughly centered when there's room to do so -- the same "cursor
// stays on screen, view scrolls around it" behavior every pageable
// list (mc, ranger, less) uses.
func ScrollWindow(cursor, total, windowHeight int) (start, end int) {
	if windowHeight <= 0 {
		windowHeight = 1
	}
	if total <= windowHeight {
		return 0, total
	}
	start = cursor - windowHeight/2
	start = max(start, 0)
	start = min(start, total-windowHeight)
	return start, start + windowHeight
}
