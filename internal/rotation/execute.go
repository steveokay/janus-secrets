package rotation

import (
	"context"
	"errors"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	backoffBase = 1 * time.Minute
	backoffCap  = 1 * time.Hour
)

// backoff returns the retry delay after failureCount consecutive failures:
// base*2^(n-1), capped. n is 1-based (first failure → base).
func backoff(failureCount int) time.Duration {
	d := backoffBase
	for i := 1; i < failureCount; i++ {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}
	if d > backoffCap {
		return backoffCap
	}
	return d
}

// rotatorApplier is the per-type apply contract.
type rotatorApplier interface {
	apply(ctx context.Context, cfg PolicyConfig, policyID, secretKey, newValue string) error
}

func (s *Service) rotatorFor(typ string) (rotatorApplier, error) {
	switch typ {
	case TypePostgres:
		return postgresRotator{}, nil
	case TypeWebhook:
		return webhookRotator{hc: s.hc}, nil
	case TypeMySQL:
		return mysqlRotator{}, nil
	case TypeRedis:
		return redisRotator{}, nil
	default:
		return nil, ErrInvalidType
	}
}

// rotate performs one crash-safe rotation of p: persist-pending → idempotent
// apply → commit (write new config version, clear pending) → notify. A pending
// value from a prior crash/attempt is reused so the external apply is idempotent.
func (s *Service) rotate(ctx context.Context, p *store.RotationPolicy, startedAt time.Time) error {
	proj, err := s.projects.Get(ctx, p.ProjectID)
	if err != nil {
		return mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, p) // fails with ErrSealed while sealed
	if err != nil {
		return err
	}

	// Reuse an in-flight value (crash/retry recovery) or generate a new one.
	var newValue string
	if p.PendingState != nil {
		if newValue, err = s.openPending(proj, p); err != nil {
			return err
		}
	} else {
		n := cfg.PasswordLen
		if n <= 0 {
			n = defaultPasswdLen
		}
		if newValue, err = generatePassword(n); err != nil {
			return err
		}
		ct, nonce, wrapped, err := s.sealPending(proj, p.ID, newValue)
		if err != nil {
			return err
		}
		if err := s.repo.SetPending(ctx, p.ID, ct, nonce, wrapped); err != nil {
			return mapStoreErr(err)
		}
	}

	// Apply to the target system (idempotent for the same newValue).
	rot, err := s.rotatorFor(p.Type)
	if err != nil {
		return err
	}
	if err := rot.apply(ctx, cfg, p.ID, p.SecretKey, newValue); err != nil {
		return err
	}

	// Commit: write the new value as a config version, then clear pending.
	actor := "rotation:" + p.ID
	cv, err := s.secrets.SetSecrets(ctx, p.ConfigID,
		[]secrets.SecretChange{{Key: p.SecretKey, Value: []byte(newValue)}}, actor, actor)
	if err != nil {
		return err
	}
	next := s.now().Add(time.Duration(p.IntervalSeconds) * time.Second)
	if err := s.repo.MarkRotated(ctx, p.ID, cv.Version, next, startedAt, p.FailureCount); err != nil {
		return mapStoreErr(err)
	}
	s.notify(ctx, cfg, p, cv.Version)
	return nil
}

// attempt runs rotate and records the audit event + failure bookkeeping. It is
// the single entry point for both the scheduler and manual rotate-now.
func (s *Service) attempt(ctx context.Context, p *store.RotationPolicy) error {
	startedAt := s.now()
	err := s.rotate(ctx, p, startedAt)
	if err != nil {
		// A sealed server is expected operational state, not a rotation fault:
		// do NOT count it as a failure (no MarkFailure → no failure_count bump,
		// no backoff, and crucially no threshold flip to status='failed', which
		// would keep the policy out of ClaimDue even after unseal), and do NOT
		// emit a counting 'failure' audit event. Return the sentinel unchanged
		// so callers can match it with ==/errors.Is.
		if errors.Is(err, ErrSealed) {
			s.logger.Debug("rotation skipped: server sealed", "policy", p.ID)
			return err
		}
		next := s.now().Add(backoff(p.FailureCount + 1))
		if merr := s.repo.MarkFailure(ctx, p.ID, sanitize(err), next, failureThreshold, startedAt, p.FailureCount+1); merr != nil {
			s.logger.Warn("rotation mark-failure failed", "policy", p.ID, "err", merr)
		}
		s.recordRotate(ctx, p, "failure", sanitize(err))
		return err
	}
	s.recordRotate(ctx, p, "success", "")
	return nil
}

// sanitize maps an apply/store error to a fixed, value-free category string
// safe to persist in last_error and audit detail.
func sanitize(err error) string {
	switch {
	case errors.Is(err, ErrSealed):
		return "sealed"
	case errors.Is(err, ErrApplyFailed):
		return "apply failed"
	case errors.Is(err, ErrInvalidConfig):
		return "invalid config"
	default:
		return "rotation error"
	}
}

// recordRotate writes a rotation.rotate audit event for a system actor. Detail
// is a value-free category on failure, empty on success.
func (s *Service) recordRotate(ctx context.Context, p *store.RotationPolicy, result, detail string) {
	if s.audit == nil {
		return
	}
	err := s.audit.Record(ctx, audit.Event{
		Actor:      audit.Actor{Kind: "system", Name: "rotation:" + p.ID},
		Action:     "rotation.rotate",
		Resource:   "configs/" + p.ConfigID + "/secrets/" + p.SecretKey,
		Detail:     detail,
		Result:     result,
		ResultCode: "",
	})
	if err != nil {
		s.logger.Warn("rotation audit write failed", "policy", p.ID, "err", err)
	}
}
