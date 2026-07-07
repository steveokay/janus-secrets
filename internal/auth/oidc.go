package auth

import (
	"context"
	"errors"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

const oidcAuthRequestTTL = 10 * time.Minute

type oidcVerifier struct {
	issuer   string
	clientID string
	verifier *oidc.IDTokenVerifier
	oauth2   *oauth2.Config
}

// OIDCClaims are the verified ID-token claims we consume.
type OIDCClaims struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
}

// OIDCProviderInput is the admin-supplied provider configuration.
type OIDCProviderInput struct {
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string // plaintext in; wrapped before storage
	Scopes       []string
	RedirectURL  string
	Enabled      bool
}

// OIDCProviderView is the non-secret provider config for admin display.
type OIDCProviderView struct {
	Name        string   `json:"name"`
	Issuer      string   `json:"issuer"`
	ClientID    string   `json:"client_id"`
	Scopes      []string `json:"scopes"`
	RedirectURL string   `json:"redirect_url"`
	Enabled     bool     `json:"enabled"`
	SecretSet   bool     `json:"secret_set"`
}

// SetOIDCProvider wraps the client secret under the master key and upserts the
// provider. Requires an unsealed keyring (surfaces crypto.ErrSealed otherwise).
func (s *Service) SetOIDCProvider(ctx context.Context, in OIDCProviderInput) error {
	if in.Name == "" {
		in.Name = "default"
	}
	if in.Issuer == "" || in.ClientID == "" || in.ClientSecret == "" || in.RedirectURL == "" {
		return ErrValidation
	}
	if len(in.Scopes) == 0 {
		in.Scopes = []string{"openid", "email", "profile"}
	}
	secret := []byte(in.ClientSecret)
	ct, err := s.keyring.WrapOIDCClientSecret(secret)
	zeroize(secret)
	if err != nil {
		return err
	}
	err = s.oidcProviders.Put(ctx, store.OIDCProvider{
		Name: in.Name, Issuer: in.Issuer, ClientID: in.ClientID,
		WrappedClientSecret: ct.Marshal(), Scopes: in.Scopes,
		RedirectURL: in.RedirectURL, Enabled: in.Enabled,
	})
	if err != nil {
		return err
	}
	s.invalidateOIDCVerifier()
	return nil
}

// GetOIDCProvider returns the non-secret provider view, or ErrNotFound.
func (s *Service) GetOIDCProvider(ctx context.Context) (*OIDCProviderView, error) {
	p, err := s.oidcProviders.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &OIDCProviderView{
		Name: p.Name, Issuer: p.Issuer, ClientID: p.ClientID, Scopes: p.Scopes,
		RedirectURL: p.RedirectURL, Enabled: p.Enabled,
		SecretSet: len(p.WrappedClientSecret) > 0,
	}, nil
}

// DeleteOIDCProvider removes the provider.
func (s *Service) DeleteOIDCProvider(ctx context.Context) error {
	if err := s.oidcProviders.Delete(ctx); err != nil {
		return err
	}
	s.invalidateOIDCVerifier()
	return nil
}

// unwrapClientSecret returns the plaintext secret for a stored provider. The
// caller must zeroize the result.
func (s *Service) unwrapClientSecret(p *store.OIDCProvider) ([]byte, error) {
	ct, err := crypto.ParseCiphertext(p.WrappedClientSecret)
	if err != nil {
		return nil, err
	}
	return s.keyring.UnwrapOIDCClientSecret(ct)
}

// invalidateOIDCVerifier drops the cached verifier (config changed).
func (s *Service) invalidateOIDCVerifier() {
	s.oidcMu.Lock()
	s.oidcCache = nil
	s.oidcMu.Unlock()
}

// oidcVerifierFor builds (or returns cached) the verifier for the enabled
// provider. ErrNotFound if none/disabled.
func (s *Service) oidcVerifierFor(ctx context.Context) (*oidcVerifier, error) {
	p, err := s.oidcProviders.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !p.Enabled {
		return nil, ErrNotFound
	}
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if s.oidcCache != nil && s.oidcCache.issuer == p.Issuer && s.oidcCache.clientID == p.ClientID {
		return s.oidcCache, nil
	}
	secret, err := s.unwrapClientSecret(p)
	if err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(ctx, p.Issuer)
	if err != nil {
		zeroize(secret)
		return nil, err
	}
	v := &oidcVerifier{
		issuer:   p.Issuer,
		clientID: p.ClientID,
		verifier: provider.Verifier(&oidc.Config{ClientID: p.ClientID}),
		oauth2: &oauth2.Config{
			ClientID:     p.ClientID,
			ClientSecret: string(secret),
			Endpoint:     provider.Endpoint(),
			RedirectURL:  p.RedirectURL,
			Scopes:       p.Scopes,
		},
	}
	zeroize(secret)
	s.oidcCache = v
	return v, nil
}

// SweepExpiredOIDCRequests removes stale login-state rows (called at boot),
// mirroring SweepExpiredSessions.
func (s *Service) SweepExpiredOIDCRequests(ctx context.Context) error {
	return s.oidcAuthReqs.DeleteExpired(ctx)
}

// StartOIDCLogin persists a login-state row and returns the provider authorize
// URL (state + nonce + PKCE S256). ErrNotFound if OIDC not configured/enabled.
func (s *Service) StartOIDCLogin(ctx context.Context) (string, error) {
	v, err := s.oidcVerifierFor(ctx)
	if err != nil {
		return "", err
	}
	p, err := s.oidcProviders.Get(ctx)
	if err != nil {
		return "", err
	}
	state, err := randToken(32)
	if err != nil {
		return "", err
	}
	nonce, err := randToken(32)
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	if err := s.oidcAuthReqs.Create(ctx, state, nonce, verifier, p.ID, time.Now().Add(oidcAuthRequestTTL)); err != nil {
		return "", err
	}
	return v.oauth2.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	), nil
}

// verifyOIDCCallback consumes the state row, exchanges the code, verifies the
// ID token (sig, iss, aud, exp, nonce), and returns claims. No user/session.
func (s *Service) verifyOIDCCallback(ctx context.Context, state, code string) (*OIDCClaims, error) {
	req, err := s.oidcAuthReqs.Consume(ctx, state)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidOIDCState
		}
		return nil, err
	}
	v, err := s.oidcVerifierFor(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := v.oauth2.Exchange(ctx, code, oauth2.VerifierOption(req.PKCEVerifier))
	if err != nil {
		return nil, ErrOIDCExchange
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, ErrOIDCExchange
	}
	idt, err := v.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, ErrOIDCExchange
	}
	if idt.Nonce != req.Nonce {
		return nil, ErrOIDCExchange
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idt.Claims(&c); err != nil {
		return nil, ErrOIDCExchange
	}
	return &OIDCClaims{Issuer: idt.Issuer, Subject: idt.Subject, Email: c.Email, EmailVerified: c.EmailVerified}, nil
}

// resolveOIDCLogin maps verified claims to a pre-provisioned user and issues a
// session cookie. Policy: link by (issuer, subject) if present; else match an
// existing user by verified email (no auto-provision); deny disabled users and
// unverified/unknown emails. All denials return ErrOIDCDenied (no enumeration).
func (s *Service) resolveOIDCLogin(ctx context.Context, c *OIDCClaims) (string, Principal, error) {
	link, err := s.oidcIdentities.GetBySubject(ctx, c.Issuer, c.Subject)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", Principal{}, err
	}
	var userID, uEmail string
	if err == nil {
		u, uErr := s.users.Get(ctx, link.UserID)
		if uErr != nil || u.DisabledAt != nil {
			return "", Principal{}, ErrOIDCDenied
		}
		userID = u.ID
		uEmail = u.Email
		_ = s.oidcIdentities.TouchLastLogin(ctx, link.ID)
	} else {
		if !c.EmailVerified {
			return "", Principal{}, ErrOIDCDenied
		}
		u, uErr := s.users.GetByEmail(ctx, c.Email)
		if uErr != nil || u.DisabledAt != nil {
			return "", Principal{}, ErrOIDCDenied // unknown or disabled — indistinguishable
		}
		if _, cErr := s.oidcIdentities.Create(ctx, u.ID, c.Issuer, c.Subject); cErr != nil {
			return "", Principal{}, cErr
		}
		userID = u.ID
		uEmail = u.Email
	}
	cookie, err := s.createSession(ctx, userID)
	if err != nil {
		return "", Principal{}, err
	}
	return cookie, Principal{Kind: KindUser, ID: userID, Name: uEmail}, nil
}

// CompleteOIDCLogin is the public entry the API calls: verify the callback then
// resolve + issue a session. Returns the session cookie and the resolved
// principal (for audit).
func (s *Service) CompleteOIDCLogin(ctx context.Context, state, code string) (string, Principal, error) {
	claims, err := s.verifyOIDCCallback(ctx, state, code)
	if err != nil {
		return "", Principal{}, err
	}
	return s.resolveOIDCLogin(ctx, claims)
}
