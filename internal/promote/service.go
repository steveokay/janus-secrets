// Package promote moves selected secrets forward along a project's release
// pipeline. Source and target share one project KEK, so promotion decrypts each
// selected source value and re-encrypts it under the target via the secrets set
// path (never copies ciphertext). It never logs or returns a secret value.
package promote

import (
	"context"
	"errors"
	"fmt"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

var (
	ErrIllegalStep = errors.New("promote: not the next pipeline step")
	ErrLockedKey   = errors.New("promote: target key is locked")
	ErrNoPipeline  = errors.New("promote: project has no pipeline configured")
)

type Status string

const (
	StatusAdd    Status = "add"
	StatusChange Status = "change"
	StatusRemove Status = "remove"
	StatusSame   Status = "same"
)

type DiffEntry struct {
	Key         string
	Status      Status
	SourceValue string // raw stored value; "" when absent (remove)
	TargetValue string // raw stored value; "" when absent (add)
	Locked      bool   // key is locked on the target config
}

type Diff struct {
	SourceVersion int
	TargetExists  bool
	Entries       []DiffEntry
}

type Action string

const (
	ActionSet    Action = "set"
	ActionRemove Action = "remove"
)

type Selection struct {
	Key    string
	Action Action
}

type ApplyRequest struct {
	SourceConfigID string
	TargetConfigID string // empty when creating the target
	TargetEnvID    string // required when TargetConfigID == "" (create)
	TargetName     string // required when creating
	CreateTarget   bool
	SourceVersion  int // pin source to the previewed version
	Selections     []Selection
	Actor          string
}

type ApplyResult struct {
	TargetVersion int
	Applied       []Selection
	Skipped       []string // keys whose source vanished (drift)
}

type Service struct {
	secrets    *secrets.Service
	secretRepo *store.SecretRepo
	configs    *store.ConfigRepo
	envs       *store.EnvironmentRepo
	pipeline   *store.PipelineRepo
	locked     *store.LockedKeyRepo
	requests   *store.PromotionRequestRepo
}

func New(sec *secrets.Service, st *store.Store) *Service {
	return &Service{
		secrets:    sec,
		secretRepo: store.NewSecretRepo(st),
		configs:    store.NewConfigRepo(st),
		envs:       store.NewEnvironmentRepo(st),
		pipeline:   store.NewPipelineRepo(st),
		locked:     store.NewLockedKeyRepo(st),
		requests:   store.NewPromotionRequestRepo(st),
	}
}

// zeroizeSecrets best-effort wipes decrypted plaintext no longer needed. Not a
// guarantee (GC may have copied), but keeps the reveal-map lifetime short.
func zeroizeSecrets(m map[string]secrets.Secret) {
	for _, s := range m {
		for i := range s.Value {
			s.Value[i] = 0
		}
	}
}

// projectAndEnv returns (projectID, envID) for a config via config→env→project.
func (s *Service) projectAndEnv(ctx context.Context, configID string) (string, string, error) {
	c, err := s.configs.Get(ctx, configID)
	if err != nil {
		return "", "", err
	}
	e, err := s.envs.Get(ctx, c.EnvironmentID)
	if err != nil {
		return "", "", err
	}
	return e.ProjectID, c.EnvironmentID, nil
}

// validateStep confirms srcEnv→dstEnv is the pipeline's next step.
func (s *Service) validateStep(ctx context.Context, projectID, srcEnv, dstEnv string) error {
	next, ok, err := s.pipeline.NextEnv(ctx, projectID, srcEnv)
	if err != nil {
		return err
	}
	if !ok {
		steps, err := s.pipeline.Get(ctx, projectID)
		if err != nil {
			return err
		}
		if len(steps) == 0 {
			return ErrNoPipeline
		}
		return ErrIllegalStep
	}
	if next != dstEnv {
		return ErrIllegalStep
	}
	return nil
}

// Preview builds the per-key diff between source and target (raw values). The
// caller has already authorized secret:read on both and audited the reveal.
func (s *Service) Preview(ctx context.Context, sourceConfigID, targetConfigID, actor string) (Diff, error) {
	proj, srcEnv, err := s.projectAndEnv(ctx, sourceConfigID)
	if err != nil {
		return Diff{}, err
	}
	_, dstEnv, err := s.projectAndEnv(ctx, targetConfigID)
	if err != nil {
		return Diff{}, err
	}
	if err := s.validateStep(ctx, proj, srcEnv, dstEnv); err != nil {
		return Diff{}, err
	}
	srcVer, srcVals, err := s.secrets.RevealConfig(ctx, sourceConfigID)
	if err != nil {
		return Diff{}, err
	}
	defer zeroizeSecrets(srcVals)
	_, dstVals, err := s.secrets.RevealConfig(ctx, targetConfigID)
	if err != nil {
		// An existing target that has no version yet holds no values; treat it as
		// empty rather than failing the preview (everything in source is an add).
		if errors.Is(err, secrets.ErrNotFound) {
			dstVals = nil
		} else {
			return Diff{}, err
		}
	}
	if dstVals != nil {
		defer zeroizeSecrets(dstVals)
	}
	lockedKeys, err := s.locked.List(ctx, targetConfigID)
	if err != nil {
		return Diff{}, err
	}
	lockedSet := map[string]bool{}
	for _, k := range lockedKeys {
		lockedSet[k] = true
	}
	seen := map[string]bool{}
	entries := []DiffEntry{}
	add := func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		src, inSrc := srcVals[key]
		dst, inDst := dstVals[key]
		e := DiffEntry{Key: key, Locked: lockedSet[key]}
		switch {
		case inSrc && !inDst:
			e.Status, e.SourceValue = StatusAdd, string(src.Value)
		case !inSrc && inDst:
			e.Status, e.TargetValue = StatusRemove, string(dst.Value)
		case string(src.Value) == string(dst.Value):
			e.Status, e.SourceValue, e.TargetValue = StatusSame, string(src.Value), string(dst.Value)
		default:
			e.Status, e.SourceValue, e.TargetValue = StatusChange, string(src.Value), string(dst.Value)
		}
		entries = append(entries, e)
	}
	for k := range srcVals {
		add(k)
	}
	for k := range dstVals {
		add(k)
	}
	return Diff{SourceVersion: srcVer.Version, TargetExists: true, Entries: entries}, nil
}

// PreviewCreate builds the diff for promoting into a target ENV that has no
// config yet: every source key is an "add". Reveals the source (audited by the
// caller) and re-uses the same pipeline-step check as Preview. target env must
// be in the same project and the pipeline's next step from the source env.
func (s *Service) PreviewCreate(ctx context.Context, sourceConfigID, toEnvID, actor string) (Diff, error) {
	proj, srcEnv, err := s.projectAndEnv(ctx, sourceConfigID)
	if err != nil {
		return Diff{}, err
	}
	toEnv, err := s.envs.Get(ctx, toEnvID)
	if err != nil {
		return Diff{}, err
	}
	if toEnv.ProjectID != proj {
		return Diff{}, ErrIllegalStep // cross-project promotion is never legal
	}
	if err := s.validateStep(ctx, proj, srcEnv, toEnvID); err != nil {
		return Diff{}, err
	}
	srcVer, srcVals, err := s.secrets.RevealConfig(ctx, sourceConfigID)
	if err != nil {
		return Diff{}, err
	}
	defer zeroizeSecrets(srcVals)
	entries := make([]DiffEntry, 0, len(srcVals))
	for k, sec := range srcVals {
		entries = append(entries, DiffEntry{Key: k, Status: StatusAdd, SourceValue: string(sec.Value), Locked: false})
	}
	return Diff{SourceVersion: srcVer.Version, TargetExists: false, Entries: entries}, nil
}

// PlanResult is a computed promotion changeset that has NOT been committed. It
// lets a caller route a promotion into a PROTECTED target config through the
// four-eyes edit-request flow instead of committing directly. Changes carry
// plaintext values, so the caller must not log them and must pass them only to a
// path that zeroizes them (editreq.Create / SetSecrets).
type PlanResult struct {
	TargetConfigID string
	SourceEnv      string
	SourceVersion  int
	Message        string
	Changes        []secrets.SecretChange
	Applied        []Selection
	Skipped        []string // keys whose source vanished (drift)
}

// Plan computes the promotion changeset WITHOUT committing it. Authorization is
// the caller's responsibility; the pipeline-step, locked-key, and drift rules
// are identical to Apply. It creates the target config when req.CreateTarget is
// set (same as Apply) — but a brand-new config can never be protected, so the
// approval-routing caller always passes an EXISTING target and no config is
// created on that path. The returned Changes carry plaintext values.
func (s *Service) Plan(ctx context.Context, req ApplyRequest) (PlanResult, error) {
	proj, srcEnv, err := s.projectAndEnv(ctx, req.SourceConfigID)
	if err != nil {
		return PlanResult{}, err
	}

	target := req.TargetConfigID
	if target == "" && req.CreateTarget {
		c, err := s.configs.Create(ctx, req.TargetEnvID, req.TargetName, nil)
		if err != nil {
			return PlanResult{}, err
		}
		target = c.ID
	}
	_, dstEnv, err := s.projectAndEnv(ctx, target)
	if err != nil {
		return PlanResult{}, err
	}
	if err := s.validateStep(ctx, proj, srcEnv, dstEnv); err != nil {
		return PlanResult{}, err
	}

	// Reject locked keys among the selections (defense in depth beyond the UI).
	keys := make([]string, 0, len(req.Selections))
	for _, sel := range req.Selections {
		keys = append(keys, sel.Key)
	}
	lockedMap, err := s.locked.AreLocked(ctx, target, keys)
	if err != nil {
		return PlanResult{}, err
	}
	for _, sel := range req.Selections {
		if lockedMap[sel.Key] {
			return PlanResult{}, fmt.Errorf("%w: %s", ErrLockedKey, sel.Key)
		}
	}

	// Reveal the pinned source version (raw plaintext) to re-encrypt under target.
	srcVals, err := s.secrets.RevealConfigVersion(ctx, req.SourceConfigID, req.SourceVersion)
	if err != nil {
		return PlanResult{}, err
	}
	defer zeroizeSecrets(srcVals)

	changes := make([]secrets.SecretChange, 0, len(req.Selections))
	applied := make([]Selection, 0, len(req.Selections))
	skipped := []string{}
	for _, sel := range req.Selections {
		switch sel.Action {
		case ActionRemove:
			changes = append(changes, secrets.SecretChange{Key: sel.Key, Delete: true})
			applied = append(applied, sel)
		case ActionSet:
			sec, ok := srcVals[sel.Key]
			if !ok {
				skipped = append(skipped, sel.Key) // drift: vanished from the source
				continue
			}
			changes = append(changes, secrets.SecretChange{Key: sel.Key, Value: append([]byte(nil), sec.Value...), Type: sec.Type})
			applied = append(applied, sel)
		}
	}
	return PlanResult{
		TargetConfigID: target,
		SourceEnv:      srcEnv,
		SourceVersion:  req.SourceVersion,
		Message:        fmt.Sprintf("promote from env %s v%d", srcEnv, req.SourceVersion),
		Changes:        changes,
		Applied:        applied,
		Skipped:        skipped,
	}, nil
}

// Apply promotes the selected keys as one new target config version. The caller
// has authorized secret:promote on target + secret:read on source (+ config
// create if creating). Locked target keys are rejected. Drifted keys are skipped.
// Callers must gate a PROTECTED target through the edit-request flow BEFORE
// calling Apply (Apply commits directly, bypassing require-approval by design).
func (s *Service) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	plan, err := s.Plan(ctx, req)
	if err != nil {
		return ApplyResult{}, err
	}
	if len(plan.Changes) == 0 {
		return ApplyResult{Applied: plan.Applied, Skipped: plan.Skipped}, nil
	}
	cv, err := s.secrets.SetSecrets(ctx, plan.TargetConfigID, plan.Changes, plan.Message, req.Actor)
	if err != nil {
		return ApplyResult{}, err
	}
	// Record promotion provenance for the UI "promoted from <env> v<n>" indicator.
	// Best-effort / non-fatal: the version is already committed, so a failed
	// provenance UPDATE must not fail or roll back the promotion. Value-free
	// (only the source env id + version).
	_ = s.secretRepo.MarkPromoted(ctx, cv.ID, plan.SourceEnv, plan.SourceVersion)
	return ApplyResult{TargetVersion: cv.Version, Applied: plan.Applied, Skipped: plan.Skipped}, nil
}
