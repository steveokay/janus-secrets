package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/auth"
)

// webauthnNicknameHeader carries the user's label for a passkey through the
// finish-registration call. The body of that request is the raw
// PublicKeyCredential JSON produced by the browser and is handed straight to the
// WebAuthn parser, so the nickname cannot ride along inside it.
const webauthnNicknameHeader = "X-Janus-Passkey-Name"

// writeWebAuthnError maps passkey sentinels to the error envelope without
// leaking which of "unknown / expired / replayed / wrong session" occurred.
func (s *Server) writeWebAuthnError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrWebAuthnNotConfigured):
		writeError(w, http.StatusConflict, "webauthn_not_configured",
			"passkeys are not configured on this server")
	case errors.Is(err, auth.ErrWebAuthnState):
		writeError(w, http.StatusConflict, "webauthn_state", "invalid passkey state")
	case errors.Is(err, auth.ErrWebAuthnVerification):
		writeError(w, http.StatusUnauthorized, "webauthn_verification", "the passkey ceremony could not be verified")
	case errors.Is(err, auth.ErrWebAuthnCloned):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	default:
		s.writeAuthError(w, err)
	}
}

// handleWebAuthnStatus is the PRE-AUTH probe the login screen uses to decide
// whether to offer a passkey button. It reveals only whether the feature is
// configured — no account information.
func (s *Server) handleWebAuthnStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": s.auth.WebAuthnEnabled(),
		"rp_id":   s.auth.WebAuthnRPID(),
	})
}

// handleWebAuthnList returns the caller's registered passkeys (value-free
// metadata only — no key material).
func (s *Server) handleWebAuthnList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if !s.auth.WebAuthnEnabled() {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false, "rp_id": "", "credentials": []any{},
		})
		return
	}
	creds, err := s.auth.ListWebAuthnCredentials(r.Context(), p.ID)
	if err != nil {
		s.writeWebAuthnError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "rp_id": s.auth.WebAuthnRPID(), "credentials": creds,
	})
}

// handleWebAuthnRegisterBegin issues a registration challenge bound to the
// caller's session.
func (s *Server) handleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	opts, err := s.auth.BeginWebAuthnRegistration(r.Context(), p.ID, p.Name)
	if err != nil {
		s.writeWebAuthnError(w, err)
		return
	}
	// The options payload is public ceremony data (challenge, RP, algorithms).
	writeJSON(w, http.StatusOK, json.RawMessage(opts))
}

// handleWebAuthnRegisterFinish verifies the attestation response and stores the
// credential. The request body is the browser's PublicKeyCredential JSON.
func (s *Server) handleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	nickname := r.Header.Get(webauthnNicknameHeader)
	info, err := s.auth.FinishWebAuthnRegistration(r.Context(), p.ID, p.Name, nickname, r.Body)
	if err != nil {
		// A failed enrollment is a denied, audited event. Best-effort: nothing
		// was mutated, so an audit failure must not change the response.
		_ = s.record(r, "webauthn.register", "users/"+p.ID, "denied", "webauthn_verification", "")
		s.writeWebAuthnError(w, err)
		return
	}
	// Value-free: the credential id and nickname are public handles.
	if err := s.record(r, "webauthn.register", "users/"+p.ID, "success", "", "credential="+info.CredentialID); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type webauthnRenameReq struct {
	Nickname string `json:"nickname"`
}

func (s *Server) handleWebAuthnRename(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	var req webauthnRenameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "a nickname is required")
		return
	}
	if err := s.auth.RenameWebAuthnCredential(r.Context(), p.ID, id, req.Nickname); err != nil {
		s.writeWebAuthnError(w, err)
		return
	}
	if err := s.record(r, "webauthn.credential.rename", "users/"+p.ID, "success", "", "credential="+id); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWebAuthnDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	nickname, err := s.auth.DeleteWebAuthnCredential(r.Context(), p.ID, id)
	if err != nil {
		s.writeWebAuthnError(w, err)
		return
	}
	if err := s.record(r, "webauthn.credential.delete", "users/"+p.ID, "success", "", "credential="+id+" name="+nickname); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type webauthnLoginBeginReq struct {
	Email string `json:"email"`
}

// handleWebAuthnLoginBegin issues an assertion challenge. It answers identically
// for known and unknown accounts (the service substitutes a decoy credential),
// so it is not an account-existence oracle.
func (s *Server) handleWebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	var req webauthnLoginBeginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "an email is required")
		return
	}
	opts, err := s.auth.BeginWebAuthnLogin(r.Context(), req.Email)
	if err != nil {
		s.writeWebAuthnError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(opts))
}

type webauthnDiscoverableBeginReq struct {
	// Conditional requests browser autofill ("conditional mediation") instead of
	// a modal prompt. A client-side directive only: it changes nothing the server
	// verifies at finish time.
	Conditional bool `json:"conditional"`
}

// handleWebAuthnDiscoverableLoginBegin issues a PASSWORDLESS assertion
// challenge. It takes no email and is byte-for-byte the same for every caller,
// so — unlike the identified begin — there is nothing here to probe at all.
func (s *Server) handleWebAuthnDiscoverableLoginBegin(w http.ResponseWriter, r *http.Request) {
	var req webauthnDiscoverableBeginReq
	// An absent or unparseable body simply means "not conditional"; there is no
	// user input to validate on this route.
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	opts, err := s.auth.BeginWebAuthnDiscoverableLogin(r.Context(), req.Conditional)
	if err != nil {
		s.writeWebAuthnError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(opts))
}

// handleWebAuthnDiscoverableLoginFinish verifies a passwordless assertion and
// mints the session cookie. The account is resolved from the credential id in
// OUR store — never from the client's claimed user handle, which is only ever
// cross-checked against it. See auth.FinishWebAuthnDiscoverableLogin.
func (s *Server) handleWebAuthnDiscoverableLoginFinish(w http.ResponseWriter, r *http.Request) {
	s.finishPasskeyLogin(w, r, ceremonyDiscoverable, s.auth.FinishWebAuthnDiscoverableLogin)
}

// handleWebAuthnLoginFinish verifies an assertion and mints the session cookie.
func (s *Server) handleWebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	s.finishPasskeyLogin(w, r, ceremonyIdentified, s.auth.FinishWebAuthnLogin)
}

// passkeyFinisher is the shared shape of the identified and discoverable finish
// steps: consume the assertion body, return the session cookie plus the
// value-free identifiers the audit record needs.
type passkeyFinisher func(ctx context.Context, body io.Reader) (cookie, userID, email, credential string, err error)

// Which passkey ceremony an audit event came from. Recorded on every passkey
// login, success or denied, so an operator reading the ledger can tell a
// passwordless sign-in from an address-identified one. Both are value-free
// labels.
const (
	ceremonyIdentified   = "identified"
	ceremonyDiscoverable = "discoverable"
)

// finishPasskeyLogin is the common response/audit half of both passkey login
// ceremonies. Keeping it in one place is deliberate: the lockout disclosure
// rule, the clone-warning audit code, and the cookie must not drift apart
// between the two flows.
func (s *Server) finishPasskeyLogin(w http.ResponseWriter, r *http.Request, ceremony string, finish passkeyFinisher) {
	cookie, userID, email, credential, err := finish(withSessionMeta(r), r.Body)
	if err != nil {
		// A locked account is revealed only to a caller who just produced a valid
		// assertion — the same rule the password path applies to the
		// password-holder.
		if locked, ok := auth.AsAccountLocked(err); ok {
			secs := int(locked.RetryAfter.Seconds())
			if secs < 1 {
				secs = 1
			}
			_ = s.recordActor(r, audit.Actor{Kind: "anonymous"}, "auth.lockout", "", "denied", CodeAccountLocked, "webauthn "+ceremony)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeError(w, http.StatusTooManyRequests, CodeAccountLocked,
				"account temporarily locked due to repeated failed logins; try again later")
			return
		}
		code := "invalid_credentials"
		if errors.Is(err, auth.ErrWebAuthnCloned) {
			// Worth surfacing distinctly in the audit trail: a counter that did not
			// advance means a cloned authenticator or a replayed assertion.
			code = "webauthn_cloned"
		}
		_ = s.recordActor(r, audit.Actor{Kind: "anonymous"}, "webauthn.login", "", "denied", code, "ceremony="+ceremony)
		s.writeWebAuthnError(w, err)
		return
	}
	if err := s.recordActor(r, audit.Actor{Kind: string(auth.KindUser), ID: userID, Name: email},
		"webauthn.login", "", "success", "", "ceremony="+ceremony+" credential="+credential); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	http.SetCookie(w, sessionCookie(r, cookie, 24*time.Hour))
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{"id": userID, "email": email},
	})
}
