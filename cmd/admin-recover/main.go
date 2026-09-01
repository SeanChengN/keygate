package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	keycrypto "github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/store"
	"golang.org/x/term"
)

const recoveryCodeCount = 10

type passwordReader func(prompt string) ([]byte, error)

func main() {
	if err := run(os.Args[1:], os.Geteuid(), os.Getenv, terminalPasswordReader, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "admin recovery failed: %v\n", err)
		os.Exit(1)
	}
}

func run(
	args []string,
	uid int,
	getenv func(string) string,
	readPassword passwordReader,
	stdout, stderr io.Writer,
) error {
	flags := flag.NewFlagSet("keygate-admin-recover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	email := flags.String("email", "", "existing owner/admin email")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if uid != 0 {
		return errors.New("must run as root in a one-off container; do not run inside the live Keygate container")
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(*email))
	if normalizedEmail == "" || !strings.Contains(normalizedEmail, "@") {
		return errors.New("--email must identify an existing owner/admin")
	}
	databaseURL := strings.TrimSpace(getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	otpPepper := getenv("OTP_PEPPER")
	if len(otpPepper) < 32 {
		return errors.New("OTP_PEPPER must contain at least 32 characters")
	}

	password, err := readMatchingPassword(readPassword)
	if err != nil {
		return err
	}
	defer clear(password)
	passwordHash, err := keycrypto.HashPassword(string(password))
	if err != nil {
		return err
	}
	codes, err := keycrypto.GenerateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return err
	}
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = keycrypto.HashRecoveryCode(otpPepper, code)
	}

	db, err := store.New(databaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer db.Close() //nolint:errcheck
	user, err := db.RecoverAdminByOperator(context.Background(), normalizedEmail, passwordHash, hashes)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Administrator %s recovered. Existing sessions and recovery codes were revoked.\n", user.Email)
	fmt.Fprintln(stdout, "Store these one-time recovery codes offline; they will not be shown again:")
	for _, code := range codes {
		fmt.Fprintln(stdout, code)
	}
	return nil
}

func terminalPasswordReader(prompt string) ([]byte, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("interactive terminal required; passwords are never accepted through arguments or pipes")
	}
	fmt.Fprint(os.Stderr, prompt)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return password, err
}

func readMatchingPassword(readPassword passwordReader) ([]byte, error) {
	password, err := readPassword("New administrator password: ")
	if err != nil {
		return nil, err
	}
	confirmation, err := readPassword("Confirm administrator password: ")
	if err != nil {
		clear(password)
		return nil, err
	}
	defer clear(confirmation)
	if !bytes.Equal(password, confirmation) {
		clear(password)
		return nil, errors.New("passwords do not match")
	}
	return password, nil
}
