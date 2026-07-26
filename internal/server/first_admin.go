package server

import (
	"context"
	"fmt"
	"strings"

	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	"github.com/agynio/users/internal/store"
)

// WithFirstAdminEmail restricts the first-admin claim to the holder of this
// email address. Unset, the first sign-in wins — the right default for a local
// cluster nobody else can reach.
func WithFirstAdminEmail(email string) Option {
	return func(s *Server) {
		s.firstAdminEmail = strings.TrimSpace(email)
	}
}

// claimFirstAdmin grants cluster admin to a just-provisioned user when the
// claim is still open and this claimant is permitted to take it.
//
// The claim is marked before the tuple is written. The two cannot share a
// transaction — one is Postgres, the other OpenFGA — so one of them has to go
// first, and the orders fail differently. Marking first can lose the grant and
// leave a cluster with no admin, which an operator can see and repair. Writing
// first can leave a granted tuple behind a claim that is still open, and the
// next sign-in would then take the claim as well: two cluster admins, neither
// of them wrong, and nothing to indicate it happened.
func (s *Server) claimFirstAdmin(ctx context.Context, user store.User, emailVerified bool) error {
	if !s.mayClaimFirstAdmin(user.Email, emailVerified) {
		return nil
	}
	claimed, err := s.store.ClaimFirstAdmin(ctx, user.Meta.ID)
	if err != nil {
		return fmt.Errorf("claim first admin: %w", err)
	}
	if !claimed {
		return nil
	}
	if err := s.syncClusterRole(ctx, user.Meta.ID, usersv1.ClusterRole_CLUSTER_ROLE_ADMIN); err != nil {
		return fmt.Errorf("grant cluster admin: %w", err)
	}
	return nil
}

// mayClaimFirstAdmin reports whether these email claims satisfy the configured
// restriction. A configured address needs the IdP to vouch for it: without
// email_verified, anyone who can register with the IdP under the operator's
// address would take the cluster.
func (s *Server) mayClaimFirstAdmin(email string, emailVerified bool) bool {
	if s.firstAdminEmail == "" {
		return true
	}
	if !emailVerified {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(email), s.firstAdminEmail)
}
