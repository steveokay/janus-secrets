package promote

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/store"
)

var ErrRequestConflict = errors.New("promotion request not pending")

type CreateRequestInput struct {
	SourceConfigID string
	TargetConfigID string
	TargetEnvID    string
	TargetName     string
	CreateTarget   bool
	SourceVersion  int
	Selections     []Selection
	Note           string
	RequestedBy    string
}

func (s *Service) CreateRequest(ctx context.Context, in CreateRequestInput) (string, error) {
	projectID, srcEnv, err := s.projectAndEnv(ctx, in.SourceConfigID)
	if err != nil {
		return "", err
	}
	dstEnv := in.TargetEnvID
	if in.TargetConfigID != "" {
		_, dstEnv, err = s.projectAndEnv(ctx, in.TargetConfigID)
		if err != nil {
			return "", err
		}
	}
	if err := s.validateStep(ctx, projectID, srcEnv, dstEnv); err != nil {
		return "", err
	}
	sels := make([]store.PromotionSelection, 0, len(in.Selections))
	for _, sel := range in.Selections {
		sels = append(sels, store.PromotionSelection{Key: sel.Key, Action: string(sel.Action)})
	}
	rec := &store.PromotionRequest{
		ProjectID:      projectID,
		SourceConfigID: in.SourceConfigID,
		SourceVersion:  in.SourceVersion,
		TargetEnvID:    dstEnv,
		TargetName:     in.TargetName,
		CreateTarget:   in.CreateTarget,
		Selections:     sels,
		Note:           in.Note,
		RequestedBy:    in.RequestedBy,
	}
	if in.TargetConfigID != "" {
		rec.TargetConfigID = &in.TargetConfigID
	}
	created, err := s.requests.Create(ctx, rec)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (s *Service) GetRequest(ctx context.Context, id string) (*store.PromotionRequest, error) {
	return s.requests.Get(ctx, id)
}

func (s *Service) ListRequestsByProject(ctx context.Context, projectID, status string) ([]*store.PromotionRequest, error) {
	return s.requests.ListByProject(ctx, projectID, status)
}

func (s *Service) ListRequestsByRequester(ctx context.Context, userID, status string) ([]*store.PromotionRequest, error) {
	return s.requests.ListByRequester(ctx, userID, status)
}

func (s *Service) ApproveRequest(ctx context.Context, id, approver string) (ApplyResult, error) {
	req, err := s.requests.Get(ctx, id)
	if err != nil {
		return ApplyResult{}, err
	}
	if req.Status != "pending" {
		return ApplyResult{}, ErrRequestConflict
	}
	target := ""
	if req.TargetConfigID != nil {
		target = *req.TargetConfigID
	}
	sels := make([]Selection, 0, len(req.Selections))
	for _, sel := range req.Selections {
		sels = append(sels, Selection{Key: sel.Key, Action: Action(sel.Action)})
	}
	// Apply FIRST; only mark the request applied once the promotion actually
	// succeeded. On an apply failure the request is left pending (no write), so
	// an "applied" row always corresponds to a real promotion — never a stranded
	// state. The mark is an atomic CAS (pending -> applied): if a concurrent
	// approver won the race in the meantime it returns ErrNotFound, which we
	// surface as a conflict (the promotion still happened and is returned).
	res, err := s.Apply(ctx, ApplyRequest{
		SourceConfigID: req.SourceConfigID,
		TargetConfigID: target,
		TargetEnvID:    req.TargetEnvID,
		TargetName:     req.TargetName,
		CreateTarget:   req.CreateTarget,
		SourceVersion:  req.SourceVersion,
		Selections:     sels,
		Actor:          approver,
	})
	if err != nil {
		return ApplyResult{}, err // request stays pending; can be retried or cancelled
	}
	if merr := s.requests.MarkApplied(ctx, id, approver, res.TargetVersion); merr != nil {
		return res, ErrRequestConflict
	}
	return res, nil
}

func (s *Service) RejectRequest(ctx context.Context, id, approver, note string) error {
	if err := s.requests.Decide(ctx, id, "rejected", approver, note); err != nil {
		return ErrRequestConflict
	}
	return nil
}

func (s *Service) CancelRequest(ctx context.Context, id, requester string) error {
	if err := s.requests.Decide(ctx, id, "cancelled", requester, ""); err != nil {
		return ErrRequestConflict
	}
	return nil
}
