package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	// webauthnChallengeTTL bounds how long a begin→finish ceremony may take.
	// Generous enough for a user to find a security key, short enough that a
	// leaked challenge is useless. Enforced server-side in the store claim, not
	// just advertised to the browser.
	webauthnChallengeTTL = 5 * time.Minute
	// webauthnPurposeRegister / webauthnPurposeLogin separate the two challenge
	// pools so a registration challenge can never be finished as a login.
	webauthnPurposeRegister = "register"
	webauthnPurposeLogin    = "login"
	// webauthnPurposeLoginDiscoverable is a THIRD, separate pool for passwordless
	// (client-side discoverable) login. It must not share the identified-login
	// pool: an identified challenge is bound to exactly one account, a
	// discoverable one is bound to none, so letting either be finished as the
	// other would be a way to opt out of that binding.
	webauthnPurposeLoginDiscoverable = "login_discoverable"
	// webauthnMaxNickname bounds the user-supplied credential label.
	webauthnMaxNickname = 64
	// webauthnMaxCredentials caps passkeys per user (bounded storage; a user with
	// this many has long since covered their devices).
	webauthnMaxCredentials = 20
)

// webauthnUser adapts a Janus user to the go-webauthn User interface. The user
// handle is the raw 16 bytes of the account UUID: already opaque and random,
// stable for the lifetime of the account, and never displayed.
type webauthnUser struct {
	handle []byte
	email  string
	creds  []webauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte                         { return u.handle }
func (u *webauthnUser) WebAuthnName() string                       { return u.email }
func (u *webauthnUser) WebAuthnDisplayName() string                { return u.email }
func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// userHandle converts a user UUID string into the 16-byte opaque WebAuthn user
// handle. Any non-UUID id (never produced by the store) is rejected rather than
// silently truncated.
func userHandle(userID string) ([]byte, error) {
	h := strings.ReplaceAll(userID, "-", "")
	if len(h) != 32 {
		return nil, fmt.Errorf("%w: malformed user id", ErrValidation)
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed user id", ErrValidation)
	}
	return b, nil
}

// SetWebAuthnConfig validates and installs the Relying Party configuration,
// building the go-webauthn instance. A zero-value config disables passkeys. Call
// once during boot, before the server serves requests.
func (s *Service) SetWebAuthnConfig(cfg WebAuthnConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !cfg.Enabled() {
		s.waCfg = WebAuthnConfig{}
		s.wa = nil
		return nil
	}
	name := cfg.RPDisplayName
	if name == "" {
		name = "Janus"
	}
	w, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: name,
		RPOrigins:     cfg.Origins,
		// No FIDO Metadata Service is wired, so demanding an attestation
		// statement would give us a blob we cannot evaluate while de-anonymising
		// the user's authenticator model. Request none; go-webauthn still
		// verifies whatever statement arrives, and the credential's public key,
		// RP ID hash, origin, and flags are verified regardless.
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			// REQUIRED, not preferred. Janus offers passwordless sign-in, which
			// only works with a client-side DISCOVERABLE credential; "preferred"
			// lets an authenticator quietly store a non-discoverable one, leaving
			// the user with a passkey that inexplicably fails the passwordless
			// button. Requiring it makes the trade-off loud instead: an
			// authenticator that cannot store a resident key (a security key with
			// no free slots) refuses enrollment with a visible error, and the
			// user's password path is untouched either way.
			//
			// Credentials enrolled BEFORE this change ran under "preferred" and
			// may or may not be discoverable; their recorded discoverability is
			// UNKNOWN and the UI says so rather than guessing.
			ResidentKey: protocol.ResidentKeyRequirementRequired,
			// The WebAuthn L1 spelling of the same requirement, for clients that
			// predate `residentKey`. The library does not derive it from the
			// config, so set it explicitly and keep the two consistent.
			RequireResidentKey: protocol.ResidentKeyRequired(),
			// User verification is REQUIRED, not preferred: a Janus passkey login
			// is single-step (it does not additionally prompt for TOTP), so the
			// credential must itself be two factors — possession of the
			// authenticator plus the PIN/biometric that unlocks it.
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return fmt.Errorf("%w: %s", ErrValidation, err)
	}
	s.waCfg = cfg
	s.wa = w
	return nil
}

// WebAuthnEnabled reports whether passkeys are configured.
func (s *Service) WebAuthnEnabled() bool { return s.wa != nil }

// WebAuthnRPID returns the configured Relying Party ID ("" when disabled).
func (s *Service) WebAuthnRPID() string { return s.waCfg.RPID }

// WebAuthnCredentialInfo is a value-free summary of one registered passkey. It
// carries no key material — only the credential id (an opaque public handle,
// base64url-encoded for display), the nickname, and usage timestamps.
type WebAuthnCredentialInfo struct {
	ID           string `json:"id"`
	Nickname     string `json:"nickname"`
	CredentialID string `json:"credential_id"`
	// Discoverable reports whether this passkey can sign in with no email typed
	// first. null means UNKNOWN (enrolled before Janus asked, and never yet used
	// passwordlessly) — the UI must say "unknown" rather than promise either way.
	Discoverable *bool      `json:"discoverable"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

func credentialInfo(c store.WebAuthnCredential) WebAuthnCredentialInfo {
	return WebAuthnCredentialInfo{
		ID:           c.ID,
		Nickname:     c.Nickname,
		CredentialID: base64.RawURLEncoding.EncodeToString(c.CredentialID),
		Discoverable: c.Discoverable,
		CreatedAt:    c.CreatedAt,
		LastUsedAt:   c.LastUsedAt,
	}
}

// ListWebAuthnCredentials returns a user's passkeys registered under the active
// RP ID.
func (s *Service) ListWebAuthnCredentials(ctx context.Context, userID string) ([]WebAuthnCredentialInfo, error) {
	if s.wa == nil {
		return nil, ErrWebAuthnNotConfigured
	}
	rows, err := s.webauthn.ListCredentials(ctx, userID, s.waCfg.RPID)
	if err != nil {
		return nil, err
	}
	out := make([]WebAuthnCredentialInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, credentialInfo(r))
	}
	return out, nil
}

// loadWebAuthnUser builds the go-webauthn User for userID with the credentials
// registered under the active RP ID. The stored signature counter (the
// authoritative one, advanced by a database CAS) overrides whatever the
// serialized credential record carries.
func (s *Service) loadWebAuthnUser(ctx context.Context, userID, email string) (*webauthnUser, []store.WebAuthnCredential, error) {
	handle, err := userHandle(userID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.webauthn.ListCredentials(ctx, userID, s.waCfg.RPID)
	if err != nil {
		return nil, nil, err
	}
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(r.Credential, &c); err != nil {
			return nil, nil, fmt.Errorf("webauthn: stored credential is unreadable: %w", err)
		}
		if r.SignCount >= 0 && r.SignCount <= math.MaxUint32 {
			c.Authenticator.SignCount = uint32(r.SignCount)
		}
		creds = append(creds, c)
	}
	return &webauthnUser{handle: handle, email: email, creds: creds}, rows, nil
}

// BeginWebAuthnRegistration issues a registration challenge for an authenticated
// user and returns the PublicKeyCredentialCreationOptions payload for
// navigator.credentials.create(). The challenge is stored server-side, bound to
// this user, single-use, and expires.
func (s *Service) BeginWebAuthnRegistration(ctx context.Context, userID, email string) (json.RawMessage, error) {
	if s.wa == nil {
		return nil, ErrWebAuthnNotConfigured
	}
	u, rows, err := s.loadWebAuthnUser(ctx, userID, email)
	if err != nil {
		return nil, err
	}
	if len(rows) >= webauthnMaxCredentials {
		return nil, fmt.Errorf("%w: too many passkeys registered", ErrWebAuthnState)
	}
	// Exclude what is already registered so an authenticator refuses to enroll
	// twice rather than producing a duplicate the store would reject.
	exclude := webauthn.Credentials(u.creds).CredentialDescriptors()
	creation, session, err := s.wa.BeginRegistration(u,
		webauthn.WithExclusions(exclude),
		// credProps asks the client to report whether it actually stored a
		// DISCOVERABLE credential. Purely informational (it is a client-supplied
		// hint, not attested), and used only to tell the user whether this passkey
		// will work for passwordless sign-in.
		webauthn.WithExtensions(protocol.AuthenticationExtensions{"credProps": true}))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrWebAuthnState, err)
	}
	if err := s.storeChallenge(ctx, webauthnPurposeRegister, &userID, session); err != nil {
		return nil, err
	}
	return json.Marshal(creation.Response)
}

// storeChallenge persists ceremony session data keyed by its challenge. It also
// opportunistically sweeps expired rows (bounded, single statement).
func (s *Service) storeChallenge(ctx context.Context, purpose string, userID *string, session *webauthn.SessionData) error {
	_ = s.webauthn.DeleteExpiredChallenges(ctx) // best-effort housekeeping
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return s.webauthn.InsertChallenge(ctx, session.Challenge, purpose, userID, raw, time.Now().Add(webauthnChallengeTTL))
}

// FinishWebAuthnRegistration verifies the attestation response, stores the
// credential, and returns its summary. nickname is the user's label; an empty or
// already-used label is replaced with a generated unique one rather than
// discarding a completed ceremony.
func (s *Service) FinishWebAuthnRegistration(ctx context.Context, userID, email, nickname string, body io.Reader) (*WebAuthnCredentialInfo, error) {
	if s.wa == nil {
		return nil, ErrWebAuthnNotConfigured
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(body)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed attestation response", ErrWebAuthnVerification)
	}
	_, session, err := s.claimChallenge(ctx, webauthnPurposeRegister, parsed.Response.CollectedClientData.Challenge, &userID)
	if err != nil {
		return nil, err
	}
	u, rows, err := s.loadWebAuthnUser(ctx, userID, email)
	if err != nil {
		return nil, err
	}
	// CreateCredential verifies the client data (type, challenge, ORIGIN), the
	// RP ID hash, the user-presence/user-verification flags against the session's
	// requirement, and the attestation statement.
	cred, err := s.wa.CreateCredential(u, *session, parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrWebAuthnVerification, err)
	}
	// Defence in depth: the session demanded user verification, so a credential
	// that reports otherwise must not be enrolled.
	if !cred.Flags.UserVerified {
		return nil, fmt.Errorf("%w: authenticator did not perform user verification", ErrWebAuthnVerification)
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	name := sanitizeNickname(nickname)
	if name == "" {
		name = "Passkey"
	}
	name = uniqueNickname(name, rows)
	saved, err := s.webauthn.InsertCredential(ctx, userID, s.waCfg.RPID, cred.ID, raw,
		int64(cred.Authenticator.SignCount), name, credPropsResidentKey(parsed.ClientExtensionResults))
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, fmt.Errorf("%w: this authenticator is already registered", ErrWebAuthnState)
		}
		return nil, err
	}
	info := credentialInfo(*saved)
	return &info, nil
}

// claimChallenge consumes a stored ceremony challenge exactly once. wantUser, if
// non-nil, additionally requires the challenge to have been issued to that user
// — this is what binds a registration ceremony to the session that started it.
// Replay, expiry, an unknown challenge, and a cross-user challenge are all
// reported identically.
func (s *Service) claimChallenge(ctx context.Context, purpose, challenge string, wantUser *string) (*store.WebAuthnChallenge, *webauthn.SessionData, error) {
	if challenge == "" {
		return nil, nil, fmt.Errorf("%w: missing challenge", ErrWebAuthnVerification)
	}
	row, err := s.webauthn.ClaimChallenge(ctx, challenge, purpose)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, fmt.Errorf("%w: unknown, expired, or already-used challenge", ErrWebAuthnVerification)
		}
		return nil, nil, err
	}
	if wantUser != nil && (row.UserID == nil || *row.UserID != *wantUser) {
		return nil, nil, fmt.Errorf("%w: challenge was not issued to this session", ErrWebAuthnVerification)
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(row.SessionData, &session); err != nil {
		return nil, nil, fmt.Errorf("%w: unreadable challenge", ErrWebAuthnVerification)
	}
	return row, &session, nil
}

// sanitizeNickname trims and bounds a user-supplied credential label, dropping
// control characters.
func sanitizeNickname(n string) string {
	n = strings.TrimSpace(n)
	var b strings.Builder
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= webauthnMaxNickname {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// uniqueNickname appends a counter when the label collides with an existing one
// (case-insensitively, matching the unique index).
func uniqueNickname(name string, existing []store.WebAuthnCredential) string {
	taken := make(map[string]bool, len(existing))
	for _, e := range existing {
		taken[strings.ToLower(e.Nickname)] = true
	}
	if !taken[strings.ToLower(name)] {
		return name
	}
	for i := 2; i < webauthnMaxCredentials+3; i++ {
		cand := fmt.Sprintf("%s (%d)", name, i)
		if len(cand) > webauthnMaxNickname {
			cand = cand[:webauthnMaxNickname]
		}
		if !taken[strings.ToLower(cand)] {
			return cand
		}
	}
	return name + " (new)"
}

// RenameWebAuthnCredential relabels one of the caller's own passkeys.
func (s *Service) RenameWebAuthnCredential(ctx context.Context, userID, id, nickname string) error {
	if s.wa == nil {
		return ErrWebAuthnNotConfigured
	}
	name := sanitizeNickname(nickname)
	if name == "" {
		return fmt.Errorf("%w: a nickname is required", ErrValidation)
	}
	err := s.webauthn.RenameCredential(ctx, id, userID, name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrAlreadyExists):
		return fmt.Errorf("%w: that nickname is already in use", ErrValidation)
	}
	return err
}

// DeleteWebAuthnCredential removes one of the caller's own passkeys and returns
// its nickname for the audit record.
//
// Deleting the LAST passkey is deliberately allowed and cannot lock anyone out:
// a passkey is an additional way in, never a replacement for the password, and
// the password (plus TOTP where enabled) keeps working throughout.
func (s *Service) DeleteWebAuthnCredential(ctx context.Context, userID, id string) (string, error) {
	if s.wa == nil {
		return "", ErrWebAuthnNotConfigured
	}
	rows, err := s.webauthn.ListCredentials(ctx, userID, s.waCfg.RPID)
	if err != nil {
		return "", err
	}
	nickname := ""
	for _, r := range rows {
		if r.ID == id {
			nickname = r.Nickname
		}
	}
	if err := s.webauthn.DeleteCredential(ctx, id, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	return nickname, nil
}

// BeginWebAuthnLogin issues an assertion challenge for the account identified by
// email and returns the PublicKeyCredentialRequestOptions payload for
// navigator.credentials.get().
//
// It answers IDENTICALLY for an account that has no passkeys, is disabled, or
// does not exist: a decoy credential id derived from HMAC(token-key, email) is
// offered instead, so the response is not an account-existence oracle. The decoy
// challenge is stored with a NULL user, so the ceremony can never be finished —
// the browser simply reports that no matching authenticator was found, exactly
// as it would for a real account whose key is not present.
//
// Residual: the decoy is a single credential, so an account that has registered
// two or more passkeys is distinguishable by the LENGTH of allowCredentials
// (never by existence, and never for the common one-passkey case). Closing that
// would mean padding to a fixed count, which leaks in the other direction for
// heavy users; the per-IP rate limit bounds the sampling either way.
func (s *Service) BeginWebAuthnLogin(ctx context.Context, email string) (json.RawMessage, error) {
	if s.wa == nil {
		return nil, ErrWebAuthnNotConfigured
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, fmt.Errorf("%w: an email is required", ErrValidation)
	}

	var (
		u       *webauthnUser
		ownerID *string
	)
	if user, err := s.users.GetByEmail(ctx, email); err == nil && user.DisabledAt == nil {
		cand, rows, lErr := s.loadWebAuthnUser(ctx, user.ID, user.Email)
		if lErr != nil {
			return nil, lErr
		}
		if len(rows) > 0 {
			id := user.ID
			u, ownerID = cand, &id
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	if u == nil {
		decoy, err := s.decoyCredential(ctx, email)
		if err != nil {
			return nil, err
		}
		u = &webauthnUser{handle: decoy.handle, email: email, creds: decoy.creds}
	}

	assertion, session, err := s.wa.BeginLogin(u)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrWebAuthnState, err)
	}
	if err := s.storeChallenge(ctx, webauthnPurposeLogin, ownerID, session); err != nil {
		return nil, err
	}
	return json.Marshal(assertion.Response)
}

// credPropsResidentKey extracts the client's `credProps.rk` hint — whether the
// authenticator stored a client-side DISCOVERABLE credential. It returns nil
// when the client did not report the extension (older browsers, and every
// credential enrolled before Janus started asking), which the UI renders as
// "unknown".
//
// This is a CLIENT-SUPPLIED hint and is deliberately not trusted for anything
// but display: a lying client would only mislabel its own passkey. Actual
// discoverability is proved later, by a passwordless assertion succeeding.
func credPropsResidentKey(ext protocol.AuthenticationExtensionsClientOutputs) *bool {
	raw, ok := ext["credProps"]
	if !ok {
		return nil
	}
	props, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	rk, ok := props["rk"].(bool)
	if !ok {
		return nil
	}
	return &rk
}

// BeginWebAuthnDiscoverableLogin issues a PASSWORDLESS assertion challenge: one
// bound to the ceremony, not to any account. No email is supplied and none is
// implied, so — unlike the identified flow — there is nothing here to probe and
// no decoy is needed. This endpoint is the same for every caller.
//
// conditional selects "conditional mediation" (browser autofill): the browser
// may silently offer a passkey inside the sign-in field instead of showing a
// modal. The mediation requirement is a CLIENT-side directive and changes
// nothing the server verifies — the challenge, the RP ID, the user-verification
// requirement, and every check in the finish step are identical either way.
func (s *Service) BeginWebAuthnDiscoverableLogin(ctx context.Context, conditional bool) (json.RawMessage, error) {
	if s.wa == nil {
		return nil, ErrWebAuthnNotConfigured
	}
	mediation := protocol.MediationDefault
	if conditional {
		mediation = protocol.MediationConditional
	}
	assertion, session, err := s.wa.BeginDiscoverableMediatedLogin(mediation)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrWebAuthnState, err)
	}
	// Stored with a NULL user, which is exactly what a discoverable ceremony is:
	// the account is not known until the assertion arrives.
	if err := s.storeChallenge(ctx, webauthnPurposeLoginDiscoverable, nil, session); err != nil {
		return nil, err
	}
	return json.Marshal(assertion.Response)
}

// FinishWebAuthnDiscoverableLogin verifies a passwordless assertion and, on
// success, mints a session cookie.
//
// # How identity is established, and why it is safe
//
// The identified flow (FinishWebAuthnLogin) takes the account from the
// CHALLENGE and never from the client. A discoverable ceremony cannot do that:
// at begin time there IS no account, so identity necessarily comes from the
// assertion. That is safe only because the assertion is never believed on its
// own — every step below is a server-side lookup or a signature check:
//
//  1. The credential is looked up in OUR store by its raw credential id
//     (globally unique, enforced by a UNIQUE index). An id we never issued
//     resolves to nothing and the ceremony ends.
//  2. The account is whatever THAT ROW says owns the credential. The client's
//     userHandle is never used to select the account.
//  3. The presented userHandle must equal the handle derived from that owner's
//     id (constant-time). So a credential belonging to A, presented with B's
//     userHandle, is rejected — credential substitution cannot authenticate.
//     The library re-checks this independently in validateLogin step 2.
//  4. The signature is verified against THAT STORED CREDENTIAL's public key,
//     because the User handed to the library carries exactly the owner's own
//     credential set, and we additionally require the asserted id to be present
//     in it.
//
// Everything the identified path enforces still holds: the challenge is
// single-use and expiring (claimed by DELETE ... RETURNING), user verification
// is required and re-asserted from the flags, a signature counter that fails to
// advance is fatal, the origin and RP ID are verified by the library, disabled
// accounts are refused, and lockout is honoured — but revealed only after a
// valid assertion.
//
// Every failure — unknown credential, wrong user handle, bad signature, replayed
// challenge, disabled account — returns the SAME ErrInvalidCredentials, so this
// endpoint is not an oracle for which passkeys or accounts exist.
func (s *Service) FinishWebAuthnDiscoverableLogin(ctx context.Context, body io.Reader) (cookie, userID, email, credential string, err error) {
	if s.wa == nil {
		return "", "", "", "", ErrWebAuthnNotConfigured
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(body)
	if err != nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	chal, session, err := s.claimChallenge(ctx, webauthnPurposeLoginDiscoverable,
		parsed.Response.CollectedClientData.Challenge, nil)
	if err != nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	// A discoverable challenge is minted with no user. One carrying a user would
	// mean the pools had been crossed; refuse rather than reason about it.
	if chal.UserID != nil {
		return "", "", "", "", ErrInvalidCredentials
	}

	// Resolved by the handler below and read back after validation.
	var (
		owner *store.User
		rows  []store.WebAuthnCredential
	)
	handler := func(rawID, handle []byte) (webauthn.User, error) {
		// (1) The credential must be one WE issued.
		row, lErr := s.webauthn.GetCredentialByCredentialID(ctx, rawID)
		if lErr != nil {
			return nil, ErrInvalidCredentials
		}
		// A credential registered under a different Relying Party ID is not usable
		// here — the same rule ListCredentials applies.
		if row.RPID != s.waCfg.RPID {
			return nil, ErrInvalidCredentials
		}
		// (2) The account is the credential's STORED owner.
		u, uErr := s.users.Get(ctx, row.UserID)
		if uErr != nil || u.DisabledAt != nil {
			return nil, ErrInvalidCredentials
		}
		// (3) The presented user handle must be that owner's handle. Compared in
		// constant time; both sides are public, but the habit is cheap.
		want, hErr := userHandle(u.ID)
		if hErr != nil {
			return nil, ErrInvalidCredentials
		}
		if subtle.ConstantTimeCompare(want, handle) != 1 {
			return nil, ErrInvalidCredentials
		}
		// (4) Hand the library the owner's own credential set, and require the
		// asserted id to actually be in it — so the signature can only ever be
		// checked against a public key we hold FOR THIS ACCOUNT.
		wu, ownerRows, wErr := s.loadWebAuthnUser(ctx, u.ID, u.Email)
		if wErr != nil {
			return nil, ErrInvalidCredentials
		}
		found := false
		for i := range ownerRows {
			if bytes.Equal(ownerRows[i].CredentialID, rawID) {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrInvalidCredentials
		}
		owner, rows = u, ownerRows
		return wu, nil
	}

	// ValidateDiscoverableLogin re-verifies, independently of the handler: that
	// the session really was discoverable (no session user id), that the
	// userHandle equals the returned User's WebAuthnID, that the asserted
	// credential is in that User's credential list, and then the signature over
	// authenticatorData||clientDataHash, the challenge, the ORIGIN, the RP ID
	// hash, and the user-presence / user-verification flags.
	cred, err := s.wa.ValidateDiscoverableLogin(handler, *session, parsed)
	if err != nil || owner == nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	// Signature-counter regression: a counter that did not advance means a cloned
	// authenticator or a replay. Fatal, not advisory.
	if cred.Authenticator.CloneWarning {
		return "", "", "", "", ErrWebAuthnCloned
	}
	if !cred.Flags.UserVerified {
		return "", "", "", "", ErrInvalidCredentials
	}
	var row *store.WebAuthnCredential
	for i := range rows {
		if bytes.Equal(rows[i].CredentialID, cred.ID) {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	updated, err := json.Marshal(cred)
	if err != nil {
		return "", "", "", "", err
	}
	// Authoritative counter check: a strictly-increasing compare-and-swap in the
	// database, which closes the concurrent-replay race an in-memory comparison
	// cannot.
	advanced, err := s.webauthn.RecordAssertion(ctx, row.ID, updated, int64(cred.Authenticator.SignCount))
	if err != nil {
		return "", "", "", "", err
	}
	if !advanced {
		return "", "", "", "", ErrWebAuthnCloned
	}
	// This assertion IS proof the credential is client-side discoverable — the
	// browser found it with no allowCredentials list. Record that, so a passkey
	// enrolled before Janus asked stops showing as "unknown". Best-effort: the
	// login has already succeeded and this is display metadata.
	_ = s.webauthn.MarkDiscoverable(ctx, row.ID)

	if s.lockout.Enabled && owner.LockedUntil != nil {
		if remaining := time.Until(*owner.LockedUntil); remaining > 0 {
			return "", "", "", "", &AccountLockedError{RetryAfter: remaining}
		}
	}
	if s.lockout.Enabled {
		_ = s.users.ResetLoginFailures(ctx, owner.ID) // best-effort
	}
	c, err := s.createSession(ctx, owner.ID)
	if err != nil {
		return "", "", "", "", err
	}
	return c, owner.ID, owner.Email, row.Nickname, nil
}

// decoy is a synthetic user handle + credential list used to make an
// unknown/passkey-less account indistinguishable from a real one.
type decoy struct {
	handle []byte
	creds  []webauthn.Credential
}

// decoyCredential derives a stable, unguessable fake credential id for email
// under the token-HMAC key. Stable so repeated probes for the same email return
// the same id (a changing id would itself be a tell); unguessable so an attacker
// cannot recognise it as synthetic without the key.
func (s *Service) decoyCredential(ctx context.Context, email string) (*decoy, error) {
	key, err := s.hmacKey(ctx)
	if err != nil {
		return nil, err
	}
	defer zeroize(key)
	credID := mac(key, "webauthn-decoy-credential:"+strings.ToLower(email))
	handle := mac(key, "webauthn-decoy-handle:"+strings.ToLower(email))[:16]
	return &decoy{
		handle: handle,
		creds:  []webauthn.Credential{{ID: credID}},
	}, nil
}

// FinishWebAuthnLogin verifies an assertion and, on success, mints a session
// cookie. It returns the cookie plus the authenticated user id/email and the
// nickname of the passkey used (for the audit record).
//
// Interaction with TOTP: a passkey login is complete on its own and does NOT
// additionally require a TOTP code. Every Janus passkey ceremony demands user
// verification, so the assertion already proves possession of the authenticator
// AND the PIN/biometric that unlocks it. TOTP continues to gate password logins.
//
// Interaction with lockout: an account locked by repeated password failures is
// locked for passkey login too, but the lock is revealed only AFTER a valid
// assertion — exactly as the password path reveals it only to the
// password-holder. A failed assertion never increments the lockout counter:
// there is no guessable secret to brute-force, and counting failures would let
// anyone lock an account out by spamming this endpoint.
func (s *Service) FinishWebAuthnLogin(ctx context.Context, body io.Reader) (cookie, userID, email, credential string, err error) {
	if s.wa == nil {
		return "", "", "", "", ErrWebAuthnNotConfigured
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(body)
	if err != nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	chal, session, err := s.claimChallenge(ctx, webauthnPurposeLogin, parsed.Response.CollectedClientData.Challenge, nil)
	if err != nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	// The account is taken from the CHALLENGE, never from the client's response:
	// a challenge minted for one account can therefore never be spent on
	// another. A challenge issued for an unknown/passkey-less account carries no
	// user (the decoy path) and can never complete.
	if chal.UserID == nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	u, err := s.users.Get(ctx, *chal.UserID)
	if err != nil || u.DisabledAt != nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	wu, rows, err := s.loadWebAuthnUser(ctx, u.ID, u.Email)
	if err != nil {
		return "", "", "", "", err
	}
	// ValidateLogin verifies the signature over authenticatorData||clientDataHash,
	// the challenge, the ORIGIN, the RP ID hash, the user-presence and
	// user-verification flags, and that session.UserID matches this user — which
	// is what stops a challenge minted for one account being spent on another.
	cred, err := s.wa.ValidateLogin(wu, *session, parsed)
	if err != nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	// Signature-counter regression: the library raises CloneWarning when the
	// asserted counter did not advance. Treat it as fatal, not advisory.
	if cred.Authenticator.CloneWarning {
		return "", "", "", "", ErrWebAuthnCloned
	}
	if !cred.Flags.UserVerified {
		return "", "", "", "", ErrInvalidCredentials
	}
	// Resolve the stored row for the asserted credential, scoped to the account
	// the challenge was issued to.
	var row *store.WebAuthnCredential
	for i := range rows {
		if bytes.Equal(rows[i].CredentialID, cred.ID) {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		return "", "", "", "", ErrInvalidCredentials
	}
	updated, err := json.Marshal(cred)
	if err != nil {
		return "", "", "", "", err
	}
	// Second, authoritative counter check: a strictly-increasing compare-and-swap
	// in the database. This closes the concurrent-replay race that an in-memory
	// comparison cannot — two assertions carrying the same counter both pass
	// ValidateLogin's read-modify-write, but only one UPDATE can win here.
	advanced, err := s.webauthn.RecordAssertion(ctx, row.ID, updated, int64(cred.Authenticator.SignCount))
	if err != nil {
		return "", "", "", "", err
	}
	if !advanced {
		return "", "", "", "", ErrWebAuthnCloned
	}
	// Lockout is honoured, but only revealed to a caller who has just proven
	// possession of the credential.
	if s.lockout.Enabled && u.LockedUntil != nil {
		if remaining := time.Until(*u.LockedUntil); remaining > 0 {
			return "", "", "", "", &AccountLockedError{RetryAfter: remaining}
		}
	}
	if s.lockout.Enabled {
		_ = s.users.ResetLoginFailures(ctx, u.ID) // best-effort
	}
	c, err := s.createSession(ctx, u.ID)
	if err != nil {
		return "", "", "", "", err
	}
	return c, u.ID, u.Email, row.Nickname, nil
}
