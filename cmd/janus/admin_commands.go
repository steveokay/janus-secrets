package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// The `admin` group is the LOCAL CONSOLE: recovery operations an operator runs
// while standing on the server host, talking straight to Postgres via
// JANUS_DATABASE_URL the way `janus migrate` does. Nothing here is reachable
// over HTTP, by design — there is no reset endpoint to attack, phish, or
// misconfigure. See docs/guides/disaster-recovery.md.

// Audit actions emitted by the admin group. Kept as constants so the docs, the
// tests, and the code cannot drift.
const (
	adminActionResetPassword = "admin.reset_password"
	adminActionClearMFA      = "admin.clear_mfa"

	// adminActorKind marks an event as originating from the local console
	// rather than from any authenticated Janus principal. It is deliberately
	// distinct from "user" / "service_token" / "anonymous": nobody logged in.
	adminActorKind = "local_console"
	adminActorName = "local-console"
	// adminActorIP stands in for the request IP the HTTP layer would record.
	// There is no request and no peer — the operator is on the box.
	adminActorIP = "local"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Local-console recovery operations (run on the server host)",
		Long: "Local-console recovery operations.\n\n" +
			"These commands run on the server host and talk directly to Postgres via\n" +
			"JANUS_DATABASE_URL. They are not exposed over HTTP. Each one requires the\n" +
			"instance's seal material (unseal shares, or the configured cloud KMS key)\n" +
			"and writes to the hash-chained audit log.",
	}
	cmd.AddCommand(newAdminResetPasswordCmd())
	return cmd
}

// adminResetOptions are the resolved flags for `janus admin reset-password`.
type adminResetOptions struct {
	email    string
	clearMFA bool
	yes      bool
}

func newAdminResetPasswordCmd() *cobra.Command {
	var opt adminResetOptions
	cmd := &cobra.Command{
		Use:   "reset-password",
		Short: "Reset a user's password from the server console (disaster recovery)",
		Long: "Reset a user's password from the server console.\n\n" +
			"For the case where the sole owner is locked out: the data is intact and\n" +
			"decryptable, so there is no reason to destroy the database. The ceremony\n" +
			"asks for the instance's seal material (unseal shares for a shamir seal, or\n" +
			"the ambient cloud credentials for a KMS seal), generates a new random\n" +
			"password, prints it exactly once, revokes every session the account holds,\n" +
			"and appends an " + adminActionResetPassword + " event to the audit chain.\n\n" +
			"TOTP enrolment is left alone unless --clear-mfa is passed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAdminResetPassword(cmd, opt)
		},
	}
	cmd.Flags().StringVar(&opt.email, "email", "", "email address of the account to reset (required)")
	cmd.Flags().BoolVar(&opt.clearMFA, "clear-mfa", false,
		"ALSO remove the account's TOTP enrolment and recovery codes (only when the second-factor device is lost too)")
	cmd.Flags().BoolVar(&opt.yes, "yes", false, "skip the confirmation prompt (required when stdin is not a terminal)")
	return cmd
}

func runAdminResetPassword(cmd *cobra.Command, opt adminResetOptions) error {
	email, err := validateAdminEmail(opt.email)
	if err != nil {
		return err
	}
	dsn := os.Getenv("JANUS_DATABASE_URL")
	if dsn == "" {
		return errors.New("JANUS_DATABASE_URL is not set")
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()

	// Refuse on an uninitialized instance. No seal config means `janus init` was
	// never run: there are no accounts, no project KEKs, and nothing to recover.
	seals := store.NewSealConfigStore(st)
	sealCfg, err := seals.Get(ctx)
	if errors.Is(err, crypto.ErrNoSealConfig) {
		return errors.New("this instance is not initialized (no seal configuration): there is nothing to recover — run `janus init`")
	}
	if err != nil {
		return fmt.Errorf("read seal configuration: %w", err)
	}

	// Resolve the account before demanding seal material, so a typo in --email
	// costs a retry rather than a whole share-collection ceremony. On the local
	// console the "does this address exist" answer is already available to
	// anyone who can run `psql`, so this is not an enumeration oracle.
	users := store.NewUserRepo(st)
	u, err := users.GetByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return err
	}

	if err := adminConfirm(cmd, opt, u.Email); err != nil {
		return err
	}

	// Obtaining the master key is a DELIBERATE CONTROL, not a technical need:
	// Argon2id password hashing uses no key material at all, and none of the
	// writes below touch an encrypted column. Requiring the seal material anyway
	//   (a) proves the operator holds the instance's root-of-trust authority
	//       rather than merely holding a database connection,
	//   (b) keeps the ceremony shaped exactly like `janus unseal`, so the same
	//       custodians and the same runbook apply, and
	//   (c) means incidental Postgres access — a stolen backup, a leaked DSN, a
	//       misconfigured replica — cannot be escalated into an owner takeover.
	// docs/threat-model.md row A7 already places root/DBA inside the trust
	// boundary, so this grants nobody new power; it converts a total-loss event
	// into an inconvenience. The key is verified (KCV) and wiped immediately —
	// it is never used to encrypt or decrypt anything here.
	master, err := adminObtainMasterKey(ctx, cmd, seals, sealCfg)
	if err != nil {
		return fmt.Errorf("could not obtain the master key: %w", err)
	}
	wipe(master)

	authSvc := auth.NewService(st, crypto.NewKeyring())
	rec := audit.New(store.NewAuditRepo(st))

	reset, err := authSvc.ResetPasswordByEmail(ctx, email)
	if err != nil {
		return err
	}

	// The audit log is append-only and hash-chained; a recovery that leaves no
	// trace is a hole. If the append fails, roll the credential back so no
	// unaudited credential change survives. (Revoked sessions are not restored
	// — that direction is fail-safe.)
	detail := fmt.Sprintf("seal=%s sessions_revoked=%d", sealCfg.Type, reset.SessionsRevoked)
	if err := rec.Record(ctx, audit.Event{
		Actor:    audit.Actor{Kind: adminActorKind, Name: adminActorName},
		Action:   adminActionResetPassword,
		Resource: "users/" + reset.UserID,
		Detail:   detail,
		Result:   "success",
		IP:       adminActorIP,
	}); err != nil {
		if undoErr := reset.Undo(ctx); undoErr != nil {
			return fmt.Errorf("audit append failed (%w) AND the previous password could not be restored (%v): "+
				"the account now has a password that was never shown — rerun this command", err, undoErr)
		}
		return fmt.Errorf("audit append failed, password reset rolled back: %w", err)
	}

	// The credential is durable and audited, so show it NOW. Anything that fails
	// after this point must not swallow the only copy of the password — the
	// operator would be locked out worse than when they started.
	adminPrintCredential(cmd, reset)

	// --clear-mfa is the louder, more dangerous step: it strips a whole
	// authentication factor. It is opt-in, audited under its own action, and
	// runs only after the password reset is durably recorded.
	if opt.clearMFA {
		switch err := authSvc.ClearTOTP(ctx, reset.UserID); {
		case err == nil:
			if err := rec.Record(ctx, audit.Event{
				Actor:    audit.Actor{Kind: adminActorKind, Name: adminActorName},
				Action:   adminActionClearMFA,
				Resource: "users/" + reset.UserID,
				Detail:   "seal=" + sealCfg.Type,
				Result:   "success",
				IP:       adminActorIP,
			}); err != nil {
				// The factor is already gone and cannot be restored (the wrapped
				// secret is destroyed), so there is nothing to roll back. Fail
				// loudly instead of pretending it was recorded.
				return fmt.Errorf("TOTP enrolment was removed but the audit append failed: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(),
				"TOTP enrolment and recovery codes were removed — re-enroll a second factor after signing in.")
		case errors.Is(err, auth.ErrNotFound):
			fmt.Fprintln(cmd.ErrOrStderr(), "note: --clear-mfa had no effect — the account has no TOTP enrolment")
		default:
			return fmt.Errorf("clear MFA: %w", err)
		}
	}
	return nil
}

// adminPrintCredential renders the one-time credential block, matching the
// style `janus init` uses for the initial-admin credential.
func adminPrintCredential(cmd *cobra.Command, reset *auth.PasswordReset) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "\nPassword reset — the new credential is shown once.")
	fmt.Fprintln(out, "It WILL NOT BE SHOWN AGAIN.")
	fmt.Fprintf(out, "  Email:    %s\n", reset.Email)
	fmt.Fprintf(out, "  Password: %s\n", reset.Password)
	fmt.Fprintf(out, "\n%d session(s) revoked; sign in and change this password immediately.\n", reset.SessionsRevoked)
}

// adminConfirm gates the reset behind an explicit acknowledgement. On a
// terminal it prompts; otherwise --yes is mandatory, because a scripted run
// that silently invalidates every session is not something to do by accident.
func adminConfirm(cmd *cobra.Command, opt adminResetOptions, email string) error {
	if opt.yes {
		return nil
	}
	if !isTerminalCmd(cmd) {
		return errors.New("refusing to reset without confirmation: pass --yes when stdin is not a terminal")
	}
	what := "Reset the password for " + email + " and revoke all of their sessions"
	if opt.clearMFA {
		what += ", AND remove their TOTP enrolment"
	}
	ans, err := promptLine(cmd, what+"? [y/N]: ")
	if err != nil {
		return err
	}
	if ans != "y" && ans != "Y" {
		return errors.New("aborted")
	}
	return nil
}

// validateAdminEmail applies the same shape check the API boundary would: a
// single non-empty address with no whitespace and no control characters. The
// lookup itself is a parameterized query, so this is a usability guard rather
// than an injection one.
func validateAdminEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)
	if email == "" {
		return "", errors.New("--email is required")
	}
	if len(email) > 320 {
		return "", errors.New("--email is too long")
	}
	if strings.ContainsAny(email, " \t\r\n") || strings.Count(email, "@") != 1 ||
		strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return "", errors.New("--email is not a valid email address")
	}
	for _, r := range email {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("--email is not a valid email address")
		}
	}
	return email, nil
}

// adminObtainMasterKey recovers and verifies the master key for the stored seal
// type. Shamir prompts for a quorum of shares on stdin with echo off; the cloud
// KMS seals unwrap through the same ambient credentials the server itself uses.
// The returned key is the caller's to wipe.
func adminObtainMasterKey(ctx context.Context, cmd *cobra.Command, seals crypto.SealConfigStore, cfg *crypto.SealConfig) ([]byte, error) {
	switch cfg.Type {
	case crypto.SealTypeShamir:
		u := crypto.NewShamirUnsealer(seals, cfg.Shares, cfg.Threshold)
		defer u.Reset() // wipe any accepted shares even on the failure paths
		shares, err := adminReadShares(cmd, cfg.Threshold)
		if err != nil {
			return nil, err
		}
		for _, sh := range shares {
			_, err := u.SubmitShare(ctx, sh)
			wipe(sh)
			if err != nil {
				// crypto's share errors are share-free by construction, so this
				// never echoes key material back at the operator.
				return nil, err
			}
		}
		return u.Unseal(ctx)

	case crypto.SealTypeAWSKMS, crypto.SealTypeGCPKMS, crypto.SealTypeAzureKV:
		fmt.Fprintf(cmd.ErrOrStderr(), "unwrapping the master key via %s...\n", cfg.Type)
		client, err := newKMSClient(ctx, cfg.Type)
		if err != nil {
			return nil, err
		}
		return crypto.NewKMSUnsealerFor(seals, client, cfg.Type).Unseal(ctx)

	default:
		return nil, fmt.Errorf("unknown seal type %q", cfg.Type)
	}
}

// adminReadShares collects n unseal shares from stdin.
//
// On a terminal each share goes through readShare — the same echo-off prompt
// `janus unseal` uses — so nothing lands in scrollback. When stdin is piped
// (one share per line) a SINGLE bufio.Reader is shared across all n reads: a
// fresh reader per share would buffer ahead and swallow the remaining lines.
func adminReadShares(cmd *cobra.Command, n int) ([][]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("stored seal configuration has a non-positive threshold (%d)", n)
	}
	tty := isTerminalCmd(cmd)
	var piped *bufio.Reader
	out := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(cmd.ErrOrStderr(), "Unseal share %d of %d\n", i+1, n)
		var line string
		if tty {
			s, err := readShare(cmd)
			if err != nil {
				return nil, err
			}
			line = s
		} else {
			if piped == nil {
				piped = bufio.NewReader(cmd.InOrStdin())
			}
			l, err := piped.ReadString('\n')
			if err != nil && l == "" {
				return nil, fmt.Errorf("reading unseal share %d: %w", i+1, err)
			}
			line = strings.TrimSpace(l)
		}
		raw, err := hex.DecodeString(line)
		if err != nil {
			// Never echo the offending input — it is key material.
			return nil, fmt.Errorf("unseal share %d is not valid hex", i+1)
		}
		out = append(out, raw)
	}
	return out, nil
}

// wipe zeroes key material in place.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
