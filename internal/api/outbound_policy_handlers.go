package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/nethard"
	"github.com/steveokay/janus-secrets/internal/store"
)

// outboundPolicyResponse is the operator-facing view of the egress policy.
//
// `source` is load-bearing rather than decorative: once the policy can come
// from either the environment or the database, an operator reading a value has
// to know which, or a Helm chart that says one thing and an instance that does
// another look identical. `locked` reports the env pin, so the UI disables
// editing for a reason it can name instead of failing a save.
//
// allow_proxy is reported but never accepted on write — see handleOutboundPolicyPut.
type outboundPolicyResponse struct {
	BlockPrivate bool     `json:"block_private"`
	Allow        []string `json:"allow"`
	AllowProxy   bool     `json:"allow_proxy"`
	// Source is "environment" or "stored".
	Source    string  `json:"source"`
	Locked    bool    `json:"locked"`
	UpdatedAt *string `json:"updated_at,omitempty"`
	UpdatedBy *string `json:"updated_by,omitempty"`
	// AlwaysBlocked names the ranges no policy can ever exempt, so the screen can
	// state the guarantee rather than the operator having to trust it.
	AlwaysBlocked []string `json:"always_blocked"`
}

// alwaysBlockedLabels mirrors nethard's unconditional block for display only.
var alwaysBlockedLabels = []string{"169.254.0.0/16", "fe80::/10", "fd00:ec2::254", "224.0.0.0/4", "ff00::/8", "0.0.0.0/32", "::/128"}

// handleOutboundPolicyGet reports the effective egress policy. Owner-only
// (sys:egress). A plain read; not self-audited, mirroring the sibling sys reads.
func (s *Server) handleOutboundPolicyGet(w http.ResponseWriter, r *http.Request) {
	if err := s.can(r, authz.SysEgress, authz.Instance()); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	resp, err := s.outboundPolicyView(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "could not read the outbound policy")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// outboundPolicyView builds the response from the LIVE policy plus the stored
// row's provenance. It reports what the dialers are actually using — reading the
// environment here instead would misreport an instance that has an override.
func (s *Server) outboundPolicyView(r *http.Request) (outboundPolicyResponse, error) {
	live := nethard.Process().Policy()
	resp := outboundPolicyResponse{
		BlockPrivate:  live.BlockPrivate,
		Allow:         nethard.DescribeAllow(live.Allow),
		AllowProxy:    live.AllowProxy,
		Source:        "environment",
		Locked:        nethard.PolicyLocked(),
		AlwaysBlocked: alwaysBlockedLabels,
	}
	if resp.Allow == nil {
		resp.Allow = []string{} // an empty list, never null, so the UI can map it
	}
	if s.outboundPolicy == nil {
		return resp, nil
	}
	stored, err := s.outboundPolicy.Get(r.Context())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return resp, nil // no override: environment is authoritative
	case err != nil:
		return resp, err
	}
	resp.Source = "stored"
	ts := stored.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	resp.UpdatedAt = &ts
	resp.UpdatedBy = stored.UpdatedBy
	return resp, nil
}

type outboundPolicyPutRequest struct {
	BlockPrivate *bool     `json:"block_private"`
	Allow        *[]string `json:"allow"`
	// AllowProxy is rejected rather than ignored — see below.
	AllowProxy *bool `json:"allow_proxy"`
}

// handleOutboundPolicyPut stores the egress policy and applies it immediately.
// Owner-only (sys:egress), fail-closed audited.
//
// Three refusals are deliberate:
//
//   - JANUS_OUTBOUND_POLICY_LOCKED pins the policy to the environment; a write
//     is a 409, not a silent no-op, so a hardened deployment fails loudly.
//   - allow_proxy is a 400 rather than a silent drop. It stays environment-only
//     because it is the single setting that blinds the connect-time guard, and
//     accepting-then-ignoring it would let an operator believe they had changed
//     something they had not.
//   - Entries are validated with the SAME parser the guard uses, so an attempt
//     to allowlist a link-local / cloud-metadata range is a 400 here for exactly
//     the reason it is unreachable at dial time.
func (s *Server) handleOutboundPolicyPut(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysEgress, authz.Instance(), "sys.egress.update", "sys/outbound-policy") {
		return
	}
	if s.outboundPolicy == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "the outbound policy store is not configured")
		return
	}
	if nethard.PolicyLocked() {
		writeError(w, http.StatusConflict, CodeValidation,
			"the outbound policy is pinned to the environment by "+nethard.EnvPolicyLocked)
		return
	}

	var req outboundPolicyPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}
	if req.AllowProxy != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"allow_proxy cannot be set here: it is configured only by "+nethard.EnvAllowProxy+
				", because routing through a proxy prevents the guard from seeing the real destination")
		return
	}
	if req.BlockPrivate == nil || req.Allow == nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "block_private and allow are both required")
		return
	}

	// Validate with the guard's own parser. Joining and re-parsing (rather than
	// parsing each entry separately) means the endpoint accepts exactly what the
	// environment variable accepts, including its rejections.
	prefixes, err := nethard.ParseAllow(strings.Join(*req.Allow, ","))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	normalised := nethard.DescribeAllow(prefixes)
	if normalised == nil {
		normalised = []string{}
	}

	// Capture the outgoing policy so the audit event records what CHANGED, not
	// just that something did. For an egress control the previous value is the
	// interesting half of the record.
	before := nethard.Process().Policy()

	stored, err := s.outboundPolicy.Put(r.Context(), *req.BlockPrivate, normalised, actorOf(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "could not store the outbound policy")
		return
	}

	// Fail closed: no unaudited egress change. If the audit write fails the
	// stored row is put back to what it was, so the record and the policy cannot
	// disagree — an egress widening that left no trace is precisely the outcome
	// this endpoint must not produce.
	if aerr := s.record(r, "sys.egress.update", "sys/outbound-policy", "success", "",
		egressDetail(before, *req.BlockPrivate, normalised)); aerr != nil {
		s.restoreOutboundPolicy(r, before)
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}

	// Apply to the live process. AllowProxy is carried over from the environment
	// unchanged, since it is not storable.
	nethard.SetProcess(nethard.Policy{
		BlockPrivate: stored.BlockPrivate,
		AllowProxy:   nethard.PolicyFromEnv().AllowProxy,
		Allow:        prefixes,
	})

	resp, err := s.outboundPolicyView(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "could not read back the outbound policy")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleOutboundPolicyDelete drops the override, returning the instance to the
// environment's policy. Owner-only (sys:egress), fail-closed audited.
func (s *Server) handleOutboundPolicyDelete(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysEgress, authz.Instance(), "sys.egress.reset", "sys/outbound-policy") {
		return
	}
	if s.outboundPolicy == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "the outbound policy store is not configured")
		return
	}
	if nethard.PolicyLocked() {
		writeError(w, http.StatusConflict, CodeValidation,
			"the outbound policy is pinned to the environment by "+nethard.EnvPolicyLocked)
		return
	}
	before := nethard.Process().Policy()
	env := nethard.PolicyFromEnv()
	if err := s.outboundPolicy.Delete(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "could not reset the outbound policy")
		return
	}
	// Fail closed, as for update: restore the row if the event cannot be written.
	if aerr := s.record(r, "sys.egress.reset", "sys/outbound-policy", "success", "",
		egressDetail(before, env.BlockPrivate, nethard.DescribeAllow(env.Allow))); aerr != nil {
		s.restoreOutboundPolicy(r, before)
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	nethard.SetProcess(env)
	resp, err := s.outboundPolicyView(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "could not read back the outbound policy")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// egressDetail renders a value-free before → after summary for the audit event.
// Ranges are configuration, not secrets, and recording them is the point: an
// egress widening must be reconstructable from the chain alone.
func egressDetail(before nethard.Policy, blockPrivate bool, allow []string) string {
	was := nethard.DescribeAllow(before.Allow)
	if len(was) == 0 {
		was = []string{"none"}
	}
	now := allow
	if len(now) == 0 {
		now = []string{"none"}
	}
	return fmt.Sprintf("block_private %t→%t; allow %s→%s",
		before.BlockPrivate, blockPrivate, strings.Join(was, " "), strings.Join(now, " "))
}

// restoreOutboundPolicy puts the stored row back after a failed audit write, so
// a mutation that could not be recorded does not survive. A failure here is
// logged by the caller's 500 and cannot be retried usefully; the live process
// policy is deliberately left untouched, because it was never updated.
func (s *Server) restoreOutboundPolicy(r *http.Request, before nethard.Policy) {
	if s.outboundPolicy == nil {
		return
	}
	// Restoring uses a background-free context deliberately: r.Context() may
	// already be cancelled by the client that triggered the failure.
	ctx := context.WithoutCancel(r.Context())
	_, _ = s.outboundPolicy.Put(ctx, before.BlockPrivate, nethard.DescribeAllow(before.Allow), actorOf(r))
}

// resolveOutboundPolicy applies the stored override (if any) over the
// environment at boot, so a restart does not silently revert an operator's
// change. A store error is returned rather than swallowed: starting with the
// environment's policy when an override exists would be a SILENT policy change,
// which is the one outcome an egress control must never produce.
func resolveOutboundPolicy(ctx context.Context, repo *store.OutboundPolicyRepo) error {
	env := nethard.PolicyFromEnv()
	if repo == nil || nethard.PolicyLocked() {
		nethard.SetProcess(env)
		return nil
	}
	stored, err := repo.Get(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		nethard.SetProcess(env)
		return nil
	case err != nil:
		return err
	}
	prefixes, perr := nethard.ParseAllow(strings.Join(stored.Allow, ","))
	if perr != nil {
		// Stored rows are written through ParseAllow, so this means the row was
		// edited outside the application. Fail closed: keep the tightening, drop
		// the exemptions we cannot understand.
		prefixes = []netip.Prefix{}
	}
	nethard.SetProcess(nethard.Policy{
		BlockPrivate: stored.BlockPrivate,
		AllowProxy:   env.AllowProxy, // env-only, never stored
		Allow:        prefixes,
	})
	return perr
}
