package system

import (
	"context"
	"testing"
)

// TestExecRunnerRunStdin verifica che lo stdin venga effettivamente passato al
// comando: `cat` senza argomenti riproduce sullo stdout ciò che riceve in input.
func TestExecRunnerRunStdin(t *testing.T) {
	r := NewExecRunner()
	out, _, err := r.RunStdin(context.Background(), "ciao mondo\n", "cat")
	if err != nil {
		t.Fatalf("RunStdin ha restituito errore: %v", err)
	}
	if out != "ciao mondo\n" {
		t.Fatalf("stdout = %q, atteso %q", out, "ciao mondo\n")
	}
}

// TestExecRunnerRunNoStdin verifica che Run resti invariato: senza stdin, `cat`
// con un input vuoto (EOF immediato) non produce output.
func TestExecRunnerRunNoStdin(t *testing.T) {
	r := NewExecRunner()
	out, _, err := r.RunStdin(context.Background(), "", "cat")
	if err != nil {
		t.Fatalf("RunStdin con stdin vuoto ha restituito errore: %v", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q, atteso vuoto", out)
	}
}
