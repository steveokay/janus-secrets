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
	secrets  *secrets.Service
	configs  *store.ConfigRepo
	envs     *store.EnvironmentRepo
	pipeline *store.PipelineRepo
	locked   *store.LockedKeyRepo
}

func New(sec *secrets.Service, st *store.Store) *Service {
	return &Service{
		secrets:  sec,
		configs:  store.NewConfigRepo(st),
		envs:     store.NewEnvironmentRepo(st),
		pipeline: store.NewPipelineRepo(st),
		locked:   store.NewLockedKeyRepo(st),
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

// Apply promotes the selected keys as one new target config version. The caller
// has authorized secret:promote on target + secret:read on source (+ config
// create if creating). Locked target keys are rejected. Drifted keys are skipped.
func (s *Service) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	proj, srcEnv, err := s.projectAndEnv(ctx, req.SourceConfigID)
	if err != nil {
		return ApplyResult{}, err
	}

	target := req.TargetConfigID
	if target == "" && req.CreateTarget {
		c, err := s.configs.Create(ctx, req.TargetEnvID, req.TargetName, nil)
		if err != nil {
			return ApplyResult{}, err
		}
		target = c.ID
	}
	_, dstEnv, err := s.projectAndEnv(ctx, target)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := s.validateStep(ctx, proj, srcEnv, dstEnv); err != nil {
		return ApplyResult{}, err
	}

	// Reject locked keys among the selections (defense in depth beyond the UI).
	keys := make([]string, 0, len(req.Selections))
	for _, sel := range req.Selections {
		keys = append(keys, sel.Key)
	}
	lockedMap, err := s.locked.AreLocked(ctx, target, keys)
	if err != nil {
		return ApplyResult{}, err
	}
	for _, sel := range req.Selections {
		if lockedMap[sel.Key] {
			return ApplyResult{}, fmt.Errorf("%w: %s", ErrLockedKey, sel.Key)
		}
	}

	// Reveal the pinned source version (raw plaintext) to re-encrypt under target.
	srcVals, err := s.secrets.RevealConfigVersion(ctx, req.SourceConfigID, req.SourceVersion)
	if err != nil {
		return ApplyResult{}, err
	}

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
			changes = append(changes, secrets.SecretChange{Key: sel.Key, Value: append([]byte(nil), sec.Value...)})
			applied = append(applied, sel)
		}
	}
	if len(changes) == 0 {
		return ApplyResult{Applied: applied, Skipped: skipped}, nil
	}
	msg := fmt.Sprintf("promote from env %s v%d", srcEnv, req.SourceVersion)
	cv, err := s.secrets.SetSecrets(ctx, target, changes, msg, req.Actor)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{TargetVersion: cv.Version, Applied: applied, Skipped: skipped}, nil
}
