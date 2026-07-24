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

// rotatorApplier is the per-type "apply a Janus-generated value" contract: the
// engine generates a fresh random value and the rotator pushes it to the target
// system (postgres/mysql/redis/webhook).
type rotatorApplier interface {
	apply(ctx context.Context, cfg PolicyConfig, policyID, secretKey, newValue string) error
}

// rotatorGenerator is the OPTIONAL "external system generates the value" contract
// for GENERATING rotators (oauth/aws_iam): the external side effect (an OAuth
// token grant, an IAM CreateAccessKey) IS the value source, and generate returns
// the new secret value that Janus persists. A rotator implements EITHER
// rotatorApplier (generate-locally→apply) OR rotatorGenerator, never both.
type rotatorGenerator interface {
	generate(ctx context.Context, cfg PolicyConfig, policyID, secretKey string) (newValue string, err error)
}

// rotatorFor returns the rotator for typ as an `any` so the caller can type-assert
// it to rotatorApplier or rotatorGenerator. Generating rotators (oauth/aws_iam)
// implement rotatorGenerator; the rest implement rotatorApplier.
func (s *Service) rotatorFor(typ string) (any, error) {
	switch typ {
	case TypePostgres:
		return postgresRotator{}, nil
	case TypeWebhook:
		return webhookRotator{hc: s.hc}, nil
	case TypeMySQL:
		return mysqlRotator{}, nil
	case TypeRedis:
		return redisRotator{}, nil
	case TypeOAuth:
		return oauthRotator{hc: s.hc}, nil
	case TypeAWSIAM:
		return awsiamRotator{}, nil
	default:
		return nil, ErrInvalidType
	}
}

// rotate performs one crash-safe rotation of p, then commits (write new config
// version, clear pending) → notify.
//
// Two rotator families:
//   - apply-a-value (postgres/mysql/redis/webhook, rotatorApplier): the engine
//     generates a fresh random value, persists it as pending BEFORE the external
//     apply (crash recovery reuses it so the apply is idempotent), then pushes it
//     to the target system.
//   - generating (oauth/aws_iam, rotatorGenerator): the EXTERNAL system mints the
//     new value — generate() itself is the side effect. There is no pre-persist:
//     the value does not exist until the external call returns. The trade-off is
//     that a crash between the external mint and the persist below can orphan a
//     credential (an OAuth token that goes unused until it expires; an IAM key
//     the next rotation prunes). A retry is safe: generating rotators re-mint
//     idempotently-enough (OAuth issues a fresh token; aws_iam converges the user
//     to a single Janus-recorded key because AWS caps a user at 2 keys and old
//     keys are deleted). See the per-rotator docs for the exact reasoning.
func (s *Service) rotate(ctx context.Context, p *store.RotationPolicy, startedAt time.Time) error {
	proj, err := s.projects.Get(ctx, p.ProjectID)
	if err != nil {
		return mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, p) // fails with ErrSealed while sealed
	if err != nil {
		return err
	}

	rot, err := s.rotatorFor(p.Type)
	if err != nil {
		return err
	}

	var newValue string
	switch r := rot.(type) {
	case rotatorGenerator:
		// The external system generates the value; the generate call IS the
		// side effect. A pending value from a prior crash cannot be reused (it
		// was minted externally and, if persisted, was already committed on that
		// attempt or is stale); we always mint a fresh one here.
		if newValue, err = r.generate(ctx, cfg, p.ID, p.SecretKey); err != nil {
			return err
		}
	case rotatorApplier:
		// Reuse an in-flight value (crash/retry recovery) or generate a new one,
		// persisting it as pending BEFORE the external apply so a crash mid-apply
		// is recovered with the SAME value (idempotent apply).
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
		if err := r.apply(ctx, cfg, p.ID, p.SecretKey, newValue); err != nil {
			return err
		}
	default:
		return ErrInvalidType
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
