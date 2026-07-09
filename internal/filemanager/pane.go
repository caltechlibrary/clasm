package filemanager

import (
	"path/filepath"
	"strings"
)

// side identifies which pane a value belongs to.
type side int

const (
	sideRemote side = iota
	sideLocal
)

// findState holds an in-progress or completed Find (DESIGN.md 21.7):
// results replace the pane's normal listing until Enter/Esc restores
// normal browsing.
type findState struct {
	pattern string
	base    string // prefix/dir the search started from, for Enter's jump-back
	results []entry
	scanned int
	done    bool
	cancel  func()
	err     error
}

// pane holds one side's browsing state -- current position, its
// current-level listing, tag set, and an optional active Find. DESIGN.md
// 21.5: panes navigate independently, so each side is a fully
// self-contained pane value.
type pane struct {
	side side
	// root is the bucket name (remote) or the linked local directory's
	// absolute path (local); constant for the pane's lifetime.
	root string
	// prefix is the current S3 key prefix ("" = bucket root) or the
	// current local directory, both slash-separated relative to root.
	prefix string

	entries []entry // current level's full listing, pre-filter
	cursor  int     // index into visible()

	filter string // current-level substring filter, "" = none (21.5)

	// tagged is keyed by entry.key so tags survive navigating away and
	// back, filtering, and Find (DESIGN.md 21.6 doesn't scope tagging to
	// "this directory only" -- mc/WinSCP-style dual-pane managers don't
	// either).
	tagged map[string]entry

	find *findState // non-nil while showing Find results
}

func newPane(s side, root string) *pane {
	return &pane{side: s, root: root, tagged: make(map[string]entry)}
}

// visible returns the rows currently shown: Find's flat results while a
// Find is active, otherwise the current level filtered by the active
// filter (or the unfiltered listing). A filter starting with "^" (or
// "/", accepted as an alias) is matched via globMatch's anchored form --
// an exact/glob match of the current level's basenames, not a
// substring -- reusing the same anchor convention Find uses (DESIGN.md
// 21.7), rather than a separate rule an operator has to remember is
// different between the two. Without an anchor, filter stays a plain
// case-insensitive substring match (DESIGN.md 21.5's original
// behavior).
func (p *pane) visible() []entry {
	if p.find != nil {
		return p.find.results
	}
	if p.filter == "" {
		return p.entries
	}
	if _, anchored := stripAnchor(p.filter); anchored {
		out := make([]entry, 0, len(p.entries))
		for _, e := range p.entries {
			if globMatch(p.filter, e.name) {
				out = append(out, e)
			}
		}
		return out
	}
	q := strings.ToLower(p.filter)
	out := make([]entry, 0, len(p.entries))
	for _, e := range p.entries {
		if strings.Contains(strings.ToLower(e.name), q) {
			out = append(out, e)
		}
	}
	return out
}

// current returns the row under the cursor, or false if the pane's
// visible list is empty.
func (p *pane) current() (entry, bool) {
	v := p.visible()
	if p.cursor < 0 || p.cursor >= len(v) {
		return entry{}, false
	}
	return v[p.cursor], true
}

func (p *pane) clampCursor() {
	n := len(p.visible())
	if n == 0 {
		p.cursor = 0
		return
	}
	if p.cursor >= n {
		p.cursor = n - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// toggleTag tags/untags the row under the cursor (Space, DESIGN.md 21.6).
func (p *pane) toggleTag() {
	e, ok := p.current()
	if !ok {
		return
	}
	if _, tagged := p.tagged[e.key]; tagged {
		delete(p.tagged, e.key)
		return
	}
	p.tagged[e.key] = e
}

// tagAllVisible tags every row currently visible (post-filter), the `*`
// hotkey (DESIGN.md 21.6).
func (p *pane) tagAllVisible() {
	for _, e := range p.visible() {
		p.tagged[e.key] = e
	}
}

// taggedOrCurrent returns the pane's tagged set, or the row under the
// cursor if nothing is tagged -- the selection rule every action key
// uses (DESIGN.md 21.6).
func (p *pane) taggedOrCurrent() []entry {
	if len(p.tagged) > 0 {
		out := make([]entry, 0, len(p.tagged))
		for _, e := range p.tagged {
			out = append(out, e)
		}
		sortEntries(out)
		return out
	}
	if e, ok := p.current(); ok {
		return []entry{e}
	}
	return nil
}

// taggedSize sums the size of every tagged row -- shown in the status
// line so an operator can see a bulk action's blast radius before
// confirming (DESIGN.md 21.4).
func (p *pane) taggedSize() int64 {
	var total int64
	for _, e := range p.tagged {
		total += e.size
	}
	return total
}

// clearTags empties the tag set, e.g. after a completed action.
func (p *pane) clearTags() {
	p.tagged = make(map[string]entry)
}

// enter navigates into the directory row under the cursor (or does
// nothing for a file row), clearing any active filter/Find so the new
// level starts from a clean view.
func (p *pane) enter(childPrefix string) {
	p.prefix = childPrefix
	p.entries = nil
	p.cursor = 0
	p.filter = ""
	p.find = nil
}

// up navigates to the parent directory of the current prefix.
func (p *pane) up() {
	if p.side == sideRemote {
		p.enter(parentOfS3Prefix(p.prefix))
		return
	}
	p.enter(parentOfLocal(p.prefix))
}

// toPrefix converts key -- an entry's identity, which is a full local
// filesystem path for the local pane or an already bucket-relative
// key/prefix for the remote pane (entry.key's doc comment) -- into this
// pane's own prefix representation. The remote side's key already *is*
// its prefix format, so this is a no-op there; the local side needs an
// absolute path turned back into a root-relative one, since pane.prefix
// is always root-relative (see the struct's doc comment) -- assigning
// an absolute path to p.prefix directly was the bug behind "drilling
// into a subdirectory breaks navigating back to the linked root":
// loadLocalCmd builds the directory to list as
// joinKey(p.root, p.prefix), which doubles into a malformed path
// (root + "/" + absolute-path) once prefix stops being root-relative.
func (p *pane) toPrefix(key string) string {
	if p.side == sideRemote {
		return key
	}
	if key == p.root {
		return ""
	}
	rel, err := filepath.Rel(p.root, key)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// parentPrefixOf returns the prefix (in this pane's own representation)
// of the directory containing key -- key being an entry's identity, the
// same shape toPrefix expects. Used by Find's Enter-to-jump (DESIGN.md
// 21.7), which jumps to a match's *parent* directory, not the match's
// own directory-as-if-it-were-a-prefix.
func (p *pane) parentPrefixOf(key string) string {
	if p.side == sideRemote {
		return parentOfS3Prefix(key)
	}
	return p.toPrefix(filepath.Dir(key))
}

// label returns the pane's header text, e.g. "LOCAL: /path/on/disk" or
// "S3: bucket-name/prefix/" (DESIGN.md 21.4).
func (p *pane) label() string {
	if p.side == sideLocal {
		return "LOCAL: " + joinKey(p.root, p.prefix)
	}
	loc := p.root + "/"
	if p.prefix != "" {
		loc += p.prefix
	}
	return "S3: " + loc
}
