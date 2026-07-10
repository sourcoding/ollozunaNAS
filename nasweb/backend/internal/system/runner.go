// Package system fornisce un wrapper controllato per l'esecuzione di comandi
// di sistema. Tutta l'orchestrazione (mdadm, samba, mount, useradd, ...) passa
// di qui, così da centralizzare logging, timeout e — soprattutto — la
// validazione degli argomenti per prevenire command injection.
package system

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Runner esegue comandi esterni. È un'interfaccia per consentire il mocking nei
// test (così i moduli RAID/share possono essere testati senza root).
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout string, stderr string, err error)
	// RunStdin è come Run ma scrive `stdin` sullo standard input del comando.
	// Serve per passare dati sensibili (es. la password a chpasswd) senza
	// esporli negli argomenti, dove sarebbero visibili in `ps` o nei log.
	RunStdin(ctx context.Context, stdin string, name string, args ...string) (stdout string, stderr string, err error)
}

// ExecRunner è l'implementazione reale basata su os/exec.
type ExecRunner struct {
	DefaultTimeout time.Duration
}

func NewExecRunner() *ExecRunner {
	return &ExecRunner{DefaultTimeout: 60 * time.Second}
}

func (r *ExecRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return r.exec(ctx, nil, name, args...)
}

// RunStdin esegue il comando passandogli `stdin` sullo standard input. Il
// contenuto di stdin non viene mai loggato (può contenere segreti).
func (r *ExecRunner) RunStdin(ctx context.Context, stdin string, name string, args ...string) (string, string, error) {
	in := stdin
	return r.exec(ctx, &in, name, args...)
}

// exec è il punto unico di esecuzione: se `stdin` è non-nil viene scritto sullo
// standard input del comando. Centralizzare qui logging, timeout e gestione
// errori evita divergenze fra Run e RunStdin.
func (r *ExecRunner) exec(ctx context.Context, stdin *string, name string, args ...string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok && r.DefaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.DefaultTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, name, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if stdin != nil {
		cmd.Stdin = strings.NewReader(*stdin)
	}

	start := time.Now()
	err := cmd.Run()
	// Nota: non logghiamo né `stdin` né lo stdout, che possono contenere dati
	// sensibili; solo il nome comando, gli argomenti (già validati) e l'esito.
	slog.Debug("exec", "cmd", name, "args", args, "stdin", stdin != nil, "dur", time.Since(start), "err", err)
	if err != nil {
		return out.String(), errBuf.String(), fmt.Errorf("comando %s fallito: %w (stderr: %s)", name, err, errBuf.String())
	}
	return out.String(), errBuf.String(), nil
}

// --- Validatori condivisi per input provenienti dalla UI -------------------

var (
	reUsername  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	reShareName = regexp.MustCompile(`^[A-Za-z0-9 _.-]{1,80}$`)
	reDevice    = regexp.MustCompile(`^/dev/[a-zA-Z0-9/]+$`)
	reMDDevice  = regexp.MustCompile(`^/dev/md[0-9]+$`)
)

// ValidUsername verifica che un nome utente sia conforme alle regole POSIX.
func ValidUsername(s string) bool { return reUsername.MatchString(s) }

// ValidShareName verifica un nome share NFS/SMB.
func ValidShareName(s string) bool { return reShareName.MatchString(s) }

// ValidDevice verifica un percorso di device a blocchi (/dev/sda, /dev/nvme0n1...).
func ValidDevice(s string) bool { return reDevice.MatchString(s) }

// ValidMDDevice verifica un device md (/dev/md0...).
func ValidMDDevice(s string) bool { return reMDDevice.MatchString(s) }
