package secretsync

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/store"
)

// ── drift detection (roadmap 7.4) ────────────────────────────────────────────
//
// Sync is one-way: Janus pushes and forgets. Drift detection closes the loop by
// reading the destination's state back and comparing it with what Janus believes
// it pushed. Targets differ in what they can even reveal, so the read capability
// is an OPTIONAL provider interface with an explicit, honest capability level —
// a write-only destination must never be able to report "no drift" in a way an
// operator could mistake for "values verified".
//
// VALUE-FREE INVARIANT. Remote values exist only as local variables inside
// Verify, are compared via keyed HMAC (the same master-key-derived sync
// fingerprint subkey used for change detection) with a constant-time equality
// check, and are zeroized before Verify returns. Nothing outside this function —
// no audit event, notification, run row, API response, or log line — ever
// carries a value or a digest of one; only key NAMES, counts, and booleans.

// Capability declares how much of a destination's state a provider can read
// back. It is a property of the destination's API, not of Janus.
type Capability string

const (
	// CapValues — remote values are readable, so real value drift is detectable.
	CapValues Capability = "values"
	// CapNamesOnly — write-only by design: the API returns key names (and
	// timestamps) but NEVER values. Missing/extra keys are detectable; value
	// drift is not.
	CapNamesOnly Capability = "names_only"
	// CapNone — nothing is readable; no drift detection is possible. This is the
	// implicit level for a provider that does not implement Verifier at all.
	CapNone Capability = "none"
)

// Verify statuses, persisted on sync_verify_runs / sync_verify_state.
const (
	VerifyClean       = "clean"
	VerifyDrift       = "drift"
	VerifyError       = "error"
	VerifyUnsupported = "unsupported"
)

// ErrVerifyUnsupported is returned when the target's provider has no read
// capability (does not implement Verifier, or declares CapNone).
var ErrVerifyUnsupported = errors.New("sync: provider cannot be verified")

// verifyNameCap bounds how many key NAMES are persisted per drift class on a
// run row. The *Count fields stay exact even when the name list is truncated.
const verifyNameCap = 100

// defaultVerifyBatch is how many due targets one verify pass claims.
const defaultVerifyBatch = 50

// RemoteState is a destination's readable state, as seen by a Verifier.
//
// Names MUST list every key present at the destination (managed or not) so
// unmanaged extras are detectable. Values is populated ONLY by a CapValues
// provider and holds live plaintext — the engine zeroizes it immediately after
// digesting, and it must never be copied, logged, or returned upward.
type RemoteState struct {
	Names  []string
	Values map[string]string
}

// Verifier is the OPTIONAL read-back capability a Provider may additionally
// implement. Providers whose destination genuinely cannot reveal values declare
// CapNamesOnly rather than pretending to compare.
type Verifier interface {
	// Capability reports what Fetch can see. It must be constant per provider.
	Capability() Capability
	// Fetch reads the destination's current state. keys is the set of
	// Janus-managed key names the caller intends to compare; a provider that can
	// only read one key per API call uses it to bound its request fan-out. Every
	// key present at the destination must still be reported in Names.
	Fetch(ctx context.Context, creds Creds, addr Addr, keys []string) (RemoteState, error)
}

// Drift is the value-free result of one verification pass.
type Drift struct {
	Capability Capability
	// ValuesCompared is true only for a CapValues provider — i.e. remote values
	// were actually digest-compared. When false, Modified is meaningless and the
	// report must say so: a clean names-only result is NOT a verified one.
	ValuesCompared bool
	// Missing — Janus-managed keys absent at the destination.
	Missing []string
	// Modified — keys whose remote value differs from the desired value
	// (CapValues only).
	Modified []string
	// Extra — keys present at the destination that Janus does not manage.
	Extra []string
	// Unreadable — keys present at the destination whose VALUE it refused to
	// return (CapValues only; e.g. a variable an operator flipped to "secret" at
	// the provider). Not drift, but recorded so a partial comparison is never
	// presented as a full one.
	Unreadable []string
	// Checked is the number of Janus-managed keys examined.
	Checked int
	// ExtraIsDrift records whether Extra counts toward the drift verdict. With
	// prune enabled Janus owns the destination namespace, so an unmanaged key is
	// real drift; with prune disabled the destination is shared by design and
	// extras are reported informationally only.
	ExtraIsDrift bool
}

// Count totals every reported difference, whether or not it is drift-worthy.
func (d Drift) Count() int { return len(d.Missing) + len(d.Modified) + len(d.Extra) }

// Status maps a Drift to its persisted status word.
func (d Drift) Status() string {
	if len(d.Missing) > 0 || len(d.Modified) > 0 {
		return VerifyDrift
	}
	if d.ExtraIsDrift && len(d.Extra) > 0 {
		return VerifyDrift
	}
	return VerifyClean
}

// VerifyReport is the API/UI projection of one pass. Value-free.
type VerifyReport struct {
	TargetID        string
	Status          string
	Capability      Capability
	ValuesCompared  bool
	Missing         []string
	Modified        []string
	Extra           []string
	Unreadable      []string
	MissingCount    int
	ModifiedCount   int
	ExtraCount      int
	UnreadableCount int
	Checked         int
	ExtraIsDrift    bool
	StartedAt       time.Time
	EndedAt         time.Time
	Error           string // sanitized category; "" on success
}

// ProviderCapability reports the declared read capability of a provider by
// name, without touching the network. Unknown providers report CapNone.
func (s *Service) ProviderCapability(name string) Capability {
	prov, err := s.providerFor(name)
	if err != nil {
		return CapNone
	}
	v, ok := prov.(Verifier)
	if !ok {
		return CapNone
	}
	return v.Capability()
}

// keyDigest returns the keyed HMAC of one (key,value) pair using the keyring's
// master-derived sync-fingerprint subkey. Returns nil while sealed. The digest
// is used ONLY for an in-memory equality check and is never persisted, logged,
// or returned.
func (s *Service) keyDigest(key, value string) []byte {
	var buf []byte
	buf = appendField(buf, key)
	buf = appendField(buf, value)
	return s.kr.SyncFingerprint(buf)
}

// zeroValues wipes a remote value map's plaintext as best Go allows: each string
// is dropped from the map so the only remaining references are unreachable.
// (Go strings are immutable, so the bytes themselves cannot be scrubbed in
// place; deleting the entries removes the live references promptly.)
func zeroValues(m map[string]string) {
	for k := range m {
		delete(m, k)
	}
}

// computeDrift compares desired against remote. It never returns values.
func (s *Service) computeDrift(desired map[string]string, remote RemoteState, capa Capability, extraIsDrift bool) (Drift, error) {
	d := Drift{
		Capability: capa, ExtraIsDrift: extraIsDrift, Checked: len(desired),
		ValuesCompared: capa == CapValues,
	}

	present := make(map[string]bool, len(remote.Names))
	for _, n := range remote.Names {
		present[n] = true
	}

	keys := sortedKeys(desired)
	for _, k := range keys {
		if !present[k] {
			d.Missing = append(d.Missing, k)
			continue
		}
		if capa != CapValues {
			continue
		}
		rv, ok := remote.Values[k]
		if !ok {
			// Present by name but the destination would not return its value.
			// Not drift — but never silently counted as verified either.
			d.Unreadable = append(d.Unreadable, k)
			continue
		}
		want := s.keyDigest(k, desired[k])
		got := s.keyDigest(k, rv)
		if want == nil || got == nil {
			return Drift{}, ErrSealed
		}
		if !hmac.Equal(want, got) {
			d.Modified = append(d.Modified, k)
		}
	}

	names := append([]string(nil), remote.Names...)
	sort.Strings(names)
	for _, n := range names {
		if _, want := desired[n]; !want {
			d.Extra = append(d.Extra, n)
		}
	}
	return d, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// capNames truncates a key-name list for persistence/response. Counts are
// carried separately and stay exact.
func capNames(in []string) []string {
	if len(in) <= verifyNameCap {
		return in
	}
	return in[:verifyNameCap]
}

// verifierFor is the capability gate: a provider is verifiable only if it
// implements the optional Verifier interface AND declares a capability above
// CapNone. Deliberately a standalone function so the gate is directly testable
// with fake providers.
func verifierFor(p Provider) (Verifier, error) {
	v, ok := p.(Verifier)
	if !ok || v.Capability() == CapNone {
		return nil, ErrVerifyUnsupported
	}
	return v, nil
}

// verify runs one verification pass against a target's destination. It performs
// no persistence — Verify records the result.
func (s *Service) verify(ctx context.Context, t *store.SyncTarget) (Drift, error) {
	prov, err := s.providerFor(t.Provider)
	if err != nil {
		return Drift{Capability: CapNone}, err
	}
	v, err := verifierFor(prov)
	if err != nil {
		return Drift{Capability: CapNone}, err
	}
	capa := v.Capability()

	proj, err := s.projects.Get(ctx, t.ProjectID)
	if err != nil {
		return Drift{Capability: capa}, mapStoreErr(err)
	}
	creds, err := s.openCreds(proj, t) // ErrSealed while sealed
	if err != nil {
		return Drift{Capability: capa}, err
	}
	desired, err := s.resolveDesired(ctx, t)
	if err != nil {
		return Drift{Capability: capa}, err
	}
	var addr Addr
	if err := json.Unmarshal(t.Addr, &addr); err != nil {
		return Drift{Capability: capa}, ErrInvalidConfig
	}

	remote, err := v.Fetch(ctx, creds, addr, sortedKeys(desired))
	if remote.Values != nil {
		defer zeroValues(remote.Values)
	}
	if err != nil {
		return Drift{Capability: capa}, err
	}
	return s.computeDrift(desired, remote, capa, t.Prune)
}

// sanitizeVerify maps an error to a fixed, value-free category.
func sanitizeVerify(err error) string {
	if errors.Is(err, ErrVerifyUnsupported) {
		return "verify unsupported"
	}
	return sanitize(err)
}

// Verify runs a verification pass for one target and persists the result. It is
// the single entry point for both the scheduler and the manual endpoint.
//
// A sealed server is not a failure: nothing is recorded and ErrSealed is
// returned. An unsupported provider IS recorded (status "unsupported") so the
// operator sees, in the history, that this destination cannot be verified.
func (s *Service) Verify(ctx context.Context, targetID string) (VerifyReport, error) {
	t, err := s.repo.Get(ctx, targetID)
	if err != nil {
		return VerifyReport{}, mapStoreErr(err)
	}
	if s.kr.Sealed() {
		return VerifyReport{}, ErrSealed
	}
	startedAt := s.now()
	d, verr := s.verify(ctx, t)
	endedAt := s.now()

	rep := VerifyReport{
		TargetID: t.ID, Capability: d.Capability, ValuesCompared: d.ValuesCompared,
		Checked: d.Checked, ExtraIsDrift: d.ExtraIsDrift,
		StartedAt: startedAt, EndedAt: endedAt,
	}
	switch {
	case verr == nil:
		rep.Status = d.Status()
		rep.Missing, rep.Modified = capNames(d.Missing), capNames(d.Modified)
		rep.Extra, rep.Unreadable = capNames(d.Extra), capNames(d.Unreadable)
		rep.MissingCount, rep.ModifiedCount = len(d.Missing), len(d.Modified)
		rep.ExtraCount, rep.UnreadableCount = len(d.Extra), len(d.Unreadable)
	case errors.Is(verr, ErrSealed):
		return VerifyReport{}, ErrSealed
	case errors.Is(verr, ErrVerifyUnsupported):
		rep.Status = VerifyUnsupported
		rep.Error = sanitizeVerify(verr)
	default:
		rep.Status = VerifyError
		rep.Error = sanitizeVerify(verr)
	}

	st, err := s.repo.GetVerifyState(ctx, t.ID)
	if err != nil {
		return VerifyReport{}, mapStoreErr(err)
	}
	interval := st.IntervalSeconds
	if interval <= 0 {
		interval = store.DefaultVerifyIntervalSeconds
	}
	next := endedAt.Add(time.Duration(interval) * time.Second)

	in := store.SyncVerifyRunInput{
		TargetID: t.ID, StartedAt: startedAt, EndedAt: endedAt,
		Status: rep.Status, Capability: string(rep.Capability), ValuesCompared: rep.ValuesCompared,
		MissingKeys: rep.Missing, ModifiedKeys: rep.Modified, ExtraKeys: rep.Extra,
		UnreadableKeys: rep.Unreadable,
		MissingCount:   rep.MissingCount, ModifiedCount: rep.ModifiedCount,
		ExtraCount: rep.ExtraCount, UnreadableCount: rep.UnreadableCount,
		CheckedCount: rep.Checked,
	}
	if rep.Error != "" {
		e := rep.Error
		in.Error = &e
	}
	if err := s.repo.RecordVerifyRun(ctx, in, next); err != nil {
		return VerifyReport{}, mapStoreErr(err)
	}
	s.recordVerify(ctx, t, rep)
	if verr != nil && !errors.Is(verr, ErrVerifyUnsupported) {
		return rep, verr
	}
	return rep, nil
}

// recordVerify writes the sync.verify audit event. Detail carries only counts
// and a capability word — never a key value.
//
// Result is "failure" when drift was found OR the pass errored, which is what
// the notification dispatcher classifies into the sync.drift event kind.
func (s *Service) recordVerify(ctx context.Context, t *store.SyncTarget, rep VerifyReport) {
	if s.audit == nil {
		return
	}
	result := "success"
	if rep.Status == VerifyDrift || rep.Status == VerifyError {
		result = "failure"
	}
	detail := fmt.Sprintf("status=%s capability=%s values_compared=%t checked=%d missing=%d modified=%d extra=%d unreadable=%d",
		rep.Status, rep.Capability, rep.ValuesCompared, rep.Checked,
		rep.MissingCount, rep.ModifiedCount, rep.ExtraCount, rep.UnreadableCount)
	if rep.Error != "" {
		detail += " error=" + rep.Error
	}
	if err := s.audit.Record(ctx, audit.Event{
		Actor:    audit.Actor{Kind: "system", Name: "sync:" + t.ID},
		Action:   "sync.verify",
		Resource: "configs/" + t.ConfigID + " -> " + t.Provider,
		Detail:   detail,
		Result:   result,
	}); err != nil {
		s.logger.Warn("sync verify audit write failed", "target", t.ID, "err", err)
	}
}

// ListVerifyRuns returns recorded verification history for a target.
func (s *Service) ListVerifyRuns(ctx context.Context, targetID string, cursor int64, limit int) ([]store.SyncVerifyRun, error) {
	return s.repo.ListVerifyRuns(ctx, targetID, cursor, limit)
}

// GetVerifyState returns a target's verify schedule + last-result summary.
func (s *Service) GetVerifyState(ctx context.Context, targetID string) (store.SyncVerifyState, error) {
	return s.repo.GetVerifyState(ctx, targetID)
}

// VerifyStatesByProject batch-loads verify state for a project's targets.
func (s *Service) VerifyStatesByProject(ctx context.Context, projectID string) (map[string]store.SyncVerifyState, error) {
	return s.repo.VerifyStatesByProject(ctx, projectID)
}

// SetVerifySchedule updates the per-target verify knobs. nil leaves unchanged.
func (s *Service) SetVerifySchedule(ctx context.Context, targetID string, enabled *bool, intervalSeconds *int64) error {
	if intervalSeconds != nil && *intervalSeconds <= 0 {
		return ErrInvalidConfig
	}
	if _, err := s.repo.Get(ctx, targetID); err != nil {
		return mapStoreErr(err)
	}
	return mapStoreErr(s.repo.SetVerifySchedule(ctx, targetID, enabled, intervalSeconds, s.now()))
}

// RunVerifyDue verifies every currently-due target once. No-op while sealed.
// Per-target errors are recorded and never abort the pass.
func (s *Service) RunVerifyDue(ctx context.Context) {
	if s.verifyTickHook != nil {
		s.verifyTickHook()
	}
	if s.kr.Sealed() {
		return
	}
	ids, err := s.repo.ClaimVerifyDueIDs(ctx, s.now(), defaultVerifyBatch)
	if err != nil {
		s.logger.Warn("sync verify claim-due failed", "err", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.Verify(ctx, id); err != nil && !errors.Is(err, ErrSealed) {
			s.logger.Warn("sync verify failed", "target", id, "err", sanitizeVerify(err))
		}
	}
}

// RunVerifyScheduler ticks every `tick` and verifies due targets until ctx is
// done. tick <= 0 disables the verify scheduler (the shipped default).
func (s *Service) RunVerifyScheduler(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	s.logger.Info("sync drift verifier started", "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("sync drift verifier stopping")
			return
		case <-t.C:
			s.RunVerifyDue(ctx)
		}
	}
}

// SetVerifyTickHook installs a callback invoked at the top of every
// RunVerifyDue pass (metrics / health). nil is a no-op.
func (s *Service) SetVerifyTickHook(h func()) { s.verifyTickHook = h }
