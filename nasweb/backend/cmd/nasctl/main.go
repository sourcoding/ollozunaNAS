// Command nasctl è una piccola CLI di amministrazione del database nasd.
// Uso principale: creare il primo utente amministratore dell'interfaccia.
//
//	nasctl -db /var/lib/nasd/nasd.db create-admin -u admin -p 'password'
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nasweb/nasd/internal/auth"
	"github.com/nasweb/nasd/internal/config"
)

func main() {
	dbPath := flag.String("db", "/var/lib/nasd/nasd.db", "percorso database")
	migrations := flag.String("migrations", "/usr/share/nasd/migrations", "dir migrazioni")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "uso: nasctl create-admin -u <user> -p <pass>")
		os.Exit(2)
	}

	db, err := config.OpenDB(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "errore DB:", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := config.Migrate(db, *migrations); err != nil {
		fmt.Fprintln(os.Stderr, "errore migrazioni:", err)
		os.Exit(1)
	}

	switch flag.Arg(0) {
	case "create-admin":
		fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
		u := fs.String("u", "", "username")
		p := fs.String("p", "", "password")
		_ = fs.Parse(flag.Args()[1:])
		if *u == "" || *p == "" {
			fmt.Fprintln(os.Stderr, "username e password obbligatori")
			os.Exit(2)
		}
		hash, err := auth.HashPassword(*p, 12)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, err = db.Exec(
			`INSERT INTO app_users(username, password_hash, is_admin) VALUES(?,?,1)
			 ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash, is_admin=1`,
			*u, hash,
		)
		if err != nil {
			fmt.Fprintln(os.Stderr, "errore creazione utente:", err)
			os.Exit(1)
		}
		fmt.Printf("Amministratore '%s' creato/aggiornato.\n", *u)
	default:
		fmt.Fprintln(os.Stderr, "comando sconosciuto:", flag.Arg(0))
		os.Exit(2)
	}
}
