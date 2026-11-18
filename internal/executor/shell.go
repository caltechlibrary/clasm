package executor

import "strings"

// ShellQuote wraps s in single quotes for safe use in a shell command line
// built for a remote host, escaping any embedded single quote.
//
// @param s {string} the text to quote
// @returns {string} s as one single-quoted shell word
// @example
//
//	cmd := "ls " + ShellQuote("my file") // ls 'my file'
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
