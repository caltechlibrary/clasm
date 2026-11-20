package recipe

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		code  int
		class string
	}{
		{"usage", &UsageError{Msg: "x"}, 2, "usage"},
		{"header", &HeaderError{File: "f", Msg: "x"}, 65, "data"},
		{"gate", &GateError{Step: "load", Changes: []string{"database"}}, 1, "negative"},
		{"host", &HostError{Step: "s", Err: errors.New("down")}, 69, "unavailable"},
		{"missing directory", fmt.Errorf("open: %w", fs.ErrNotExist), 66, "no_input"},
		{"step exit 65", &StepError{Result: StepResult{ExitCode: 65, ExitClass: "data"}}, 65, "data"},
		{"step exit 1", &StepError{Result: StepResult{ExitCode: 1, ExitClass: "negative"}}, 1, "negative"},
		{"step exit 7 is internal", &StepError{Result: StepResult{ExitCode: 7, ExitClass: "internal"}}, 70, "internal"},
		{"step timeout", &StepError{Result: StepResult{ExitCode: 69, ExitClass: "timeout"}}, 69, "unavailable"},
		{"wrapped", fmt.Errorf("run: %w", &GateError{Step: "x"}), 1, "negative"},
	}
	for _, c := range cases {
		code, class, ok := Classify(c.err)
		if !ok || code != c.code || class != c.class {
			t.Errorf("%s: got %d %s %v, want %d %s", c.name, code, class, ok, c.code, c.class)
		}
	}
	if _, _, ok := Classify(errors.New("something else")); ok {
		t.Error("an error recipe does not own is not classified here")
	}
	if _, _, ok := Classify(nil); ok {
		t.Error("nil is not an error")
	}
}
