// Package editreq implements the four-eyes approval flow for edits to a
// protected config (configs.require_approval = true). Instead of committing a
// secret save directly, it stores the proposed changes ENVELOPE-ENCRYPTED as a
// pending config_edit_requests row; a DIFFERENT user then approves it, which
// decrypts the proposal and commits it via the normal secrets save path (one
// config version). It never logs or returns a secret value; request metadata is
// value-free (changed key NAMES only). It reuses the promotion-approval
// patterns: request → approve (applies immediately) → reject/cancel, with a
// mark-on-success CAS so an "applied" row always maps to a real commit.
package editreq

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// ErrRequestConflict is returned when a request is no longer pending (already
// applied/rejected/cancelled, or a concurrent approver won the race).
var ErrRequestConflict = errors.New("editreq: request not pending")

// ErrSelfApproval is returned when the requester tries to approve/reject their
// own request (four-eyes).
var ErrSelfApproval = errors.New("editreq: cannot decide your own request")

// proposedChange is the JSON-serializable form of a secrets.SecretChange stored
// inside the envelope-encrypted proposal blob. Value carries the proposed
// plaintext, which is why the whole blob is encrypted at rest.
type proposedChange struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Delete bool   `json:"delete"`
	Type   string `json:"type"`
}

// Service orchestrates config-edit-request lifecycle over the secrets service
// (for envelope encryption + the eventual commit) and the store.
type Service struct {
	secrets  *secrets.Service
	requests *store.ConfigEditRequestRepo
	configs  *store.ConfigRepo
}

// New constructs a Service.
func New(sec *secrets.Service, st *store.Store) *Service {
	return &Service{
		secrets:  sec,
		requests: store.NewConfigEditRequestRepo(st),
		configs:  store.NewConfigRepo(st),
	}
}

// CreateInput describes a proposed edit to a protected config.
type CreateInput struct {
	ConfigID    string
	Changes     []secrets.SecretChange
	Message     string
	Reason      string
	RequestedBy string
}

// Create stores the proposed changes envelope-encrypted as a pending request
// and returns its id. The proposal is serialized to JSON and encrypted under a
// fresh DEK wrapped by the config's project KEK — the proposed VALUES never hit
// disk in plaintext. changed key NAMES are stored separately (value-free) for
// list views. Best-effort zeroizes each change's Value after serialization.
func (s *Service) Create(ctx context.Context, in CreateInput) (string, error) {
	pcs := make([]proposedChange, 0, len(in.Changes))
	keys := make([]string, 0, len(in.Changes))
	for _, ch := range in.Changes {
		pcs = append(pcs, proposedChange{
			Key:    ch.Key,
			Value:  string(ch.Value),
			Delete: ch.Delete,
			Type:   ch.Type,
		})
		keys = append(keys, ch.Key)
	}
	blobJSON, err := json.Marshal(pcs)
	if err != nil {
		return "", err
	}
	// Best-effort wipe the caller's plaintext copies now that they're serialized.
	for i := range in.Changes {
		for j := range in.Changes[i].Value {
			in.Changes[i].Value[j] = 0
		}
	}
	blob, err := s.secrets.EncryptConfigBlob(ctx, in.ConfigID, blobJSON)
	// EncryptConfigBlob best-effort zeroizes blobJSON internally.
	if err != nil {
		return "", err
	}
	rec := &store.ConfigEditRequest{
		ConfigID:           in.ConfigID,
		RequestedBy:        in.RequestedBy,
		Reason:             in.Reason,
		Message:            in.Message,
		ProposedCiphertext: blob.Ciphertext,
		WrappedDEK:         blob.WrappedDEK,
		Nonce:              blob.Nonce,
		DEKKeyVersion:      blob.DEKKeyVersion,
		ChangedKeys:        keys,
	}
	created, err := s.requests.Create(ctx, rec)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// Get fetches one edit request.
func (s *Service) Get(ctx context.Context, id string) (*store.ConfigEditRequest, error) {
	return s.requests.Get(ctx, id)
}

// ListByConfig lists a config's edit requests, optionally filtered by status.
func (s *Service) ListByConfig(ctx context.Context, configID, status string) ([]*store.ConfigEditRequest, error) {
	return s.requests.ListByConfig(ctx, configID, status)
}

// ListByRequester lists a user's edit requests, optionally filtered by status.
func (s *Service) ListByRequester(ctx context.Context, userID, status string) ([]*store.ConfigEditRequest, error) {
	return s.requests.ListByRequester(ctx, userID, status)
}

// ApplyResult reports the outcome of approving a request.
type ApplyResult struct {
	Version int
	Keys    []string
}

// Approve applies a pending request: it decrypts the proposed changes and
// commits them via SetSecrets (one config version), then marks the request
// applied with a mark-on-success CAS. Four-eyes is enforced by the CALLER
// (approver != requester) before calling; this method also refuses when
// approver == requester as defense in depth. The commit happens FIRST; the mark
// only lands if the commit succeeded, so an applied row always maps to a real
// save. If a concurrent approver won the CAS, the commit still happened and is
// returned with ErrRequestConflict.
func (s *Service) Approve(ctx context.Context, id, approver string) (ApplyResult, error) {
	req, err := s.requests.Get(ctx, id)
	if err != nil {
		return ApplyResult{}, err
	}
	if req.Status != "pending" {
		return ApplyResult{}, ErrRequestConflict
	}
	if approver == req.RequestedBy {
		return ApplyResult{}, ErrSelfApproval
	}
	pt, err := s.secrets.DecryptConfigBlob(ctx, req.ConfigID, secrets.EditRequestBlob{
		Ciphertext:    req.ProposedCiphertext,
		WrappedDEK:    req.WrappedDEK,
		Nonce:         req.Nonce,
		DEKKeyVersion: req.DEKKeyVersion,
	})
	if err != nil {
		return ApplyResult{}, err
	}
	defer zeroize(pt)
	var pcs []proposedChange
	if err := json.Unmarshal(pt, &pcs); err != nil {
		return ApplyResult{}, err
	}
	changes := make([]secrets.SecretChange, 0, len(pcs))
	keys := make([]string, 0, len(pcs))
	for _, pc := range pcs {
		changes = append(changes, secrets.SecretChange{
			Key:    pc.Key,
			Value:  []byte(pc.Value),
			Delete: pc.Delete,
			Type:   pc.Type,
		})
		keys = append(keys, pc.Key)
	}
	cv, err := s.secrets.SetSecrets(ctx, req.ConfigID, changes, req.Message, approver)
	if err != nil {
		return ApplyResult{}, err // request stays pending; retriable
	}
	if merr := s.requests.MarkApplied(ctx, id, approver, cv.Version); merr != nil {
		return ApplyResult{Version: cv.Version, Keys: keys}, ErrRequestConflict
	}
	return ApplyResult{Version: cv.Version, Keys: keys}, nil
}

// Reject declines a pending request. Four-eyes: rejecter != requester (enforced
// here and by the caller).
func (s *Service) Reject(ctx context.Context, id, rejecter string) error {
	req, err := s.requests.Get(ctx, id)
	if err != nil {
		return err
	}
	if rejecter == req.RequestedBy {
		return ErrSelfApproval
	}
	if err := s.requests.Decide(ctx, id, "rejected", rejecter); err != nil {
		return ErrRequestConflict
	}
	return nil
}

// Cancel withdraws a pending request. Only the requester may cancel (caller
// enforces; this returns ErrRequestConflict if it is not pending).
func (s *Service) Cancel(ctx context.Context, id, requester string) error {
	if err := s.requests.Decide(ctx, id, "cancelled", requester); err != nil {
		return ErrRequestConflict
	}
	return nil
}

// zeroize best-effort wipes a plaintext slice.
func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
