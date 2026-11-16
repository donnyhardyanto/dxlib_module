package password

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"strconv"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
)

// temporaryAlphabet leaves out characters that are easy to misread (0 O o 1 l I).
const temporaryAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// TemporaryLength is the length of a reset-owner password when Config.MinLength is not longer: 24 characters of a
// 56-symbol alphabet, about 139 bits.
const TemporaryLength = 24

func temporaryPassword(n int) (string, error) {
	max := big.NewInt(int64(len(temporaryAlphabet)))
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("password: reading random bytes for the temporary password: %w", err)
		}
		b[i] = temporaryAlphabet[k.Int64()]
	}
	return string(b), nil
}

// ResetOwner gives the owner account a random temporary password, in this order: ends a lockout of the account,
// stores the password with must_change_password set, ends every session of the account, and writes a
// CredentialChanged audit record whose detail names the host command as the actor. It returns the temporary
// password (the caller shows it once, never logs it) and the number of sessions ended.
//
// The temporary password is random (TemporaryLength characters, longer when Config.MinLength is), so it is not
// checked against the breached list or the context words: a reset must work while the list cannot be reached.
func (m *Manager) ResetOwner(ctx context.Context, ownerUUID string) (temporary string, sessionsEnded int, err error) {
	d := map[string]string{"auth_method": AuthMethod, "step": "reset_owner", "actor": "host command reset-owner", "must_change_password": "true"}
	u, err := canonicalUUID(ownerUUID)
	if err != nil {
		return "", 0, m.changeFailed(ctx, "", "", fmt.Errorf("reset-owner: %w", err), d)
	}
	n := TemporaryLength
	if m.cfg.MinLength > n {
		n = m.cfg.MinLength
	}
	temporary, err = temporaryPassword(n)
	if err != nil {
		return "", 0, m.changeFailed(ctx, "", u, err, d)
	}
	if err := m.lockout.Unlock(ctx, u, "reset-owner host command"); err != nil {
		return "", 0, m.changeFailed(ctx, "", u, fmt.Errorf("reset-owner: ending the lockout of user %s: %w; nothing was changed", u, err), d)
	}
	rec, err := m.store1(ctx, u, normalize(temporary), true, nil)
	if err != nil {
		return "", 0, m.changeFailed(ctx, "", u, fmt.Errorf("reset-owner: %w", err), d)
	}
	d["key_version"] = strconv.Itoa(rec.KeyVersion)
	sessionsEnded, rerr := m.sessions.RevokeAll(ctx, u, "owner password reset on the host")
	d["sessions_ended"] = strconv.Itoa(sessionsEnded)
	e := auth_audit.Event{Kind: auth_audit.CredentialChanged, UserUUID: u, Outcome: auth_audit.OutcomeOK, Detail: d}
	if rerr != nil {
		e.Reason = "the temporary password was set, but ending the owner's sessions failed: " + rerr.Error()
	}
	aerr := m.write(ctx, e)
	if rerr != nil {
		return temporary, sessionsEnded, fmt.Errorf("password: reset-owner set the temporary password of user %s, but their sessions could not be ended: %w", u, rerr)
	}
	if aerr != nil {
		return temporary, sessionsEnded, fmt.Errorf("password: reset-owner set the temporary password of user %s, but its audit record could not be written: %w", u, aerr)
	}
	return temporary, sessionsEnded, nil
}

// RunResetOwner is the reset-owner host command, for a service to run from its own binary (for example
// "myservice reset-owner --confirm") on the server, after it has built the Manager from its secret store. owner
// returns the owner account's uuid; --user names another account instead.
//
// It refuses to run without --confirm. On success it writes the temporary password, and nothing else, as one line on
// stdout, so it can be read by the operator or piped to a password manager; every other message goes to stderr.
// Exit status: 0 done, 1 failed, 2 usage. When the password was set but a later step failed (ending the sessions,
// the audit record), the password is still printed, the error is named on stderr and the status is 1.
func RunResetOwner(ctx context.Context, m *Manager, owner func(ctx context.Context) (string, error), args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reset-owner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	confirm := fs.Bool("confirm", false, "really reset the password (without it nothing is changed)")
	user := fs.String("user", "", "uuid of the account to reset, instead of the owner account")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: reset-owner --confirm [--user <uuid>]")
		_, _ = fmt.Fprintln(stderr, "Sets a random temporary password for the owner account, ends its lockout and its sessions, and")
		_, _ = fmt.Fprintln(stderr, "forces a new password at the next login. The temporary password is printed once on stdout.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "reset-owner: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}
	if m == nil {
		_, _ = fmt.Fprintln(stderr, "reset-owner: the service passed no password Manager; check how it builds it")
		return 1
	}
	target := *user
	if target == "" {
		if owner == nil {
			_, _ = fmt.Fprintln(stderr, "reset-owner: the service names no owner account; give --user <uuid>")
			return 2
		}
		var err error
		if target, err = owner(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "reset-owner: finding the owner account: %v\n", err)
			return 1
		}
	}
	if !*confirm {
		_, _ = fmt.Fprintf(stderr, "reset-owner: would reset the password of user %s, end its lockout and its sessions; nothing changed. Run again with --confirm.\n", target)
		return 2
	}
	temporary, ended, err := m.ResetOwner(ctx, target)
	if temporary != "" {
		_, _ = fmt.Fprintln(stdout, temporary)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reset-owner: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "reset-owner: user %s has a temporary password (above); %d session(s) ended; a new password is required at the next login.\n", target, ended)
	return 0
}
