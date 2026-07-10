package users

import (
	"context"
	"strings"
	"testing"
)

// mockRunner cattura le invocazioni per verificare cosa ricevono chpasswd/smbpasswd.
type mockCall struct {
	cmd       string
	args      []string
	stdin     string
	stdinUsed bool
}

type mockRunner struct {
	lastCmd   string
	lastArgs  []string
	lastStdin string
	stdinUsed bool
	calls     []mockCall
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	m.lastCmd, m.lastArgs, m.stdinUsed = name, args, false
	m.calls = append(m.calls, mockCall{cmd: name, args: args})
	return "", "", nil
}

func (m *mockRunner) RunStdin(ctx context.Context, stdin string, name string, args ...string) (string, string, error) {
	m.lastCmd, m.lastArgs, m.lastStdin, m.stdinUsed = name, args, stdin, true
	m.calls = append(m.calls, mockCall{cmd: name, args: args, stdin: stdin, stdinUsed: true})
	return "", "", nil
}

// TestSetPasswordUsesStdin verifica che la password sia passata su stdin a
// chpasswd nel formato "utente:password\n", e MAI come argomento.
func TestSetPasswordUsesStdin(t *testing.T) {
	mr := &mockRunner{}
	m := NewManager(mr)
	if err := m.SetPassword(context.Background(), "alice", "s3gr3ta!"); err != nil {
		t.Fatalf("SetPassword ha restituito errore: %v", err)
	}
	// Deve impostare sia la password di sistema (chpasswd) sia quella Samba
	// (smbpasswd), entrambe via stdin e mai con la password negli argomenti.
	var chpasswd, smbpasswd *mockCall
	for i := range mr.calls {
		switch mr.calls[i].cmd {
		case "chpasswd":
			chpasswd = &mr.calls[i]
		case "smbpasswd":
			smbpasswd = &mr.calls[i]
		}
	}
	if chpasswd == nil {
		t.Fatal("chpasswd non è stato invocato")
	}
	if !chpasswd.stdinUsed || chpasswd.stdin != "alice:s3gr3ta!\n" {
		t.Fatalf("chpasswd stdin = %q, atteso %q", chpasswd.stdin, "alice:s3gr3ta!\n")
	}
	if smbpasswd == nil {
		t.Fatal("smbpasswd non è stato invocato (accesso CIFS)")
	}
	if !smbpasswd.stdinUsed || smbpasswd.stdin != "s3gr3ta!\ns3gr3ta!\n" {
		t.Fatalf("smbpasswd stdin = %q, atteso password ripetuta", smbpasswd.stdin)
	}
	for _, c := range mr.calls {
		for _, a := range c.args {
			if strings.Contains(a, "s3gr3ta!") {
				t.Fatalf("la password non deve comparire negli argomenti di %s: %v", c.cmd, c.args)
			}
		}
	}
}

// TestSetPasswordRejectsNewline verifica il guard anti line-injection in chpasswd.
func TestSetPasswordRejectsNewline(t *testing.T) {
	mr := &mockRunner{}
	m := NewManager(mr)
	cases := []string{"pass\nroot:pwned", "pass\rinjected", ""}
	for _, pw := range cases {
		if err := m.SetPassword(context.Background(), "alice", pw); err == nil {
			t.Fatalf("password %q dovrebbe essere rifiutata", pw)
		}
	}
	if mr.stdinUsed {
		t.Fatal("nessun comando deve essere eseguito per password non valide")
	}
}

// TestSetPasswordRejectsBadUsername verifica la validazione del nome utente.
func TestSetPasswordRejectsBadUsername(t *testing.T) {
	mr := &mockRunner{}
	m := NewManager(mr)
	if err := m.SetPassword(context.Background(), "bad:name", "x"); err == nil {
		t.Fatal("nome utente non valido dovrebbe essere rifiutato")
	}
}
