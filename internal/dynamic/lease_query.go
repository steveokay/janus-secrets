package dynamic

import "context"

// GetLease returns the masked view of a single lease by id.
func (s *Service) GetLease(ctx context.Context, id string) (LeaseView, error) {
	l, err := s.leases.Get(ctx, id)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	return leaseView(l), nil
}

// ListLeasesByRole returns masked views of every lease for a role.
func (s *Service) ListLeasesByRole(ctx context.Context, roleID string) ([]LeaseView, error) {
	ls, err := s.leases.ListByRole(ctx, roleID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]LeaseView, 0, len(ls))
	for _, l := range ls {
		out = append(out, leaseView(l))
	}
	return out, nil
}
