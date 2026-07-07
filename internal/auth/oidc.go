package auth

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

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

// invalidateOIDCVerifier drops any cached verifier. TEMPORARY no-op; Task 10
// replaces the body with real cache invalidation.
func (s *Service) invalidateOIDCVerifier() {}
