package ui

import (
	"os"

	"golang.org/x/term"
)

// StdinIsTerminal reports whether standard input is a terminal -- the
// "interactive" input to workflow.Gate (DR-0180). False in a pipe, a redirect
// or a cron job, where a destructive CLI form must print its plan and stop
// instead of waiting for input that will never come. Uses golang.org/x/term,
// the same package color.go already uses for standard output, so there is one
// way to ask the question in this repository.
func StdinIsTerminal() bool { return isTerminalFd(os.Stdin.Fd()) }

// isTerminalFd is StdinIsTerminal's testable core.
func isTerminalFd(fd uintptr) bool { return term.IsTerminal(int(fd)) }
