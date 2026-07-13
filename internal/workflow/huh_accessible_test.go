package workflow

import (
	"bytes"
	"io"
)

// newHuhAccessibleInput returns an io.Reader that yields at most one
// newline-terminated line per Read call, no matter how large a buffer
// the caller offers. huh's accessible-mode fields (Select.RunAccessible,
// Confirm.RunAccessible, ...) each build a brand-new bufio.Scanner per
// call and discard whatever it buffered past the first newline once that
// call returns -- a reader that eagerly returns everything it has in one
// Read (e.g. strings.NewReader) silently starves every field after the
// first, both across separate Form.Run calls (as RunS3Menu's loop makes)
// and within a single multi-field Form (as object_browser.go's
// pre-flight makes). A real terminal in canonical mode naturally
// delivers input this way, one Read per Enter keypress; this reproduces
// that. See DECISIONS.md, "huh fields are pipe-testable via
// WithAccessible(true).WithInput/WithOutput."
func newHuhAccessibleInput(s string) io.Reader {
	return &lineAtATimeReader{remaining: []byte(s)}
}

// newPipeEditor returns a writer/reader/buffer trio for driving a
// workflow function's output (w, same value as buf) and accessible-mode
// prompt/confirm input (input) through a single pipe -- replaces the
// termlib.LineEditor-backed helper of the same name from before the
// termlib removal (DECISIONS.md, "Remove termlib entirely: input via
// huh, output via io.Writer"). w and buf are deliberately the same
// value: callers pass w as the function's output writer and inspect
// buf.String() afterward.
func newPipeEditor(input string) (w io.Writer, pipeInput io.Reader, buf *bytes.Buffer) {
	var b bytes.Buffer
	return &b, newHuhAccessibleInput(input), &b
}

type lineAtATimeReader struct {
	remaining []byte
}

func (r *lineAtATimeReader) Read(p []byte) (int, error) {
	if len(r.remaining) == 0 {
		return 0, io.EOF
	}
	idx := bytes.IndexByte(r.remaining, '\n')
	var line []byte
	if idx == -1 {
		line = r.remaining
		r.remaining = nil
	} else {
		line = r.remaining[:idx+1]
		r.remaining = r.remaining[idx+1:]
	}
	return copy(p, line), nil
}
