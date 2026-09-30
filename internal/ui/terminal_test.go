package ui

import (
	"os"
	"testing"
)

// A pipe is what a script's standard input looks like, and is never a
// terminal: the gate must treat it as non-interactive and never wait on it.
func TestIsTerminalFd_PipeIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminalFd(r.Fd()) {
		t.Error("a pipe reported as a terminal")
	}
	if isTerminalFd(w.Fd()) {
		t.Error("a pipe's write end reported as a terminal")
	}
}

func TestIsTerminalFd_RegularFileIsNotATerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminalFd(f.Fd()) {
		t.Error("a regular file reported as a terminal")
	}
}
