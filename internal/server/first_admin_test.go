package server

import (
	"context"
	"fmt"
	"sync"
	"testing"

	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestFirstSignInTakesClusterAdminWhenUnrestricted(t *testing.T) {
	authorization := &fakeAuthorizationClient{}
	server := newFirstAdminServer(newFakeUserStore(), authorization)

	first, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "auth0|first",
		Email:       "first@example.com",
	})
	require.NoError(t, err)
	require.True(t, first.GetCreated())
	require.Equal(t, []string{identityObject(first)}, authorization.clusterAdmins())

	second, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "auth0|second",
		Email:       "second@example.com",
	})
	require.NoError(t, err)
	require.True(t, second.GetCreated())

	returning, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "auth0|first",
		Email:       "first@example.com",
	})
	require.NoError(t, err)
	require.False(t, returning.GetCreated())

	require.Equal(t, []string{identityObject(first)}, authorization.clusterAdmins())
}

func TestNonMatchingEmailLeavesTheClaimOpen(t *testing.T) {
	authorization := &fakeAuthorizationClient{}
	server := newFirstAdminServer(newFakeUserStore(), authorization, WithFirstAdminEmail("Admin@Example.com"))

	other, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject:   "auth0|other",
		Email:         "other@example.com",
		EmailVerified: true,
	})
	require.NoError(t, err)
	require.True(t, other.GetCreated())
	require.Empty(t, authorization.clusterAdmins())

	admin, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject:   "auth0|admin",
		Email:         "admin@example.com",
		EmailVerified: true,
	})
	require.NoError(t, err)
	require.Equal(t, []string{identityObject(admin)}, authorization.clusterAdmins())
}

func TestUnverifiedEmailNeverTakesTheClaim(t *testing.T) {
	authorization := &fakeAuthorizationClient{}
	server := newFirstAdminServer(newFakeUserStore(), authorization, WithFirstAdminEmail("admin@example.com"))

	impostor, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "auth0|impostor",
		Email:       "admin@example.com",
	})
	require.NoError(t, err)
	require.True(t, impostor.GetCreated())
	require.Empty(t, authorization.clusterAdmins())

	admin, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject:   "auth0|admin",
		Email:         "admin@example.com",
		EmailVerified: true,
	})
	require.NoError(t, err)
	require.Equal(t, []string{identityObject(admin)}, authorization.clusterAdmins())
}

func TestConcurrentFirstSignInsProduceExactlyOneClusterAdmin(t *testing.T) {
	authorization := &fakeAuthorizationClient{}
	fakeStore := newFakeUserStore()
	server := newFirstAdminServer(fakeStore, authorization)

	const claimants = 8
	var start sync.WaitGroup
	var finished sync.WaitGroup
	start.Add(1)
	errs := make(chan error, claimants)
	for index := range claimants {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()
			_, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
				OidcSubject: fmt.Sprintf("auth0|%d", index),
				Email:       fmt.Sprintf("user-%d@example.com", index),
			})
			errs <- err
		}()
	}
	start.Done()
	finished.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, fakeStore.users, claimants)
	require.Len(t, authorization.clusterAdmins(), 1)
}

func TestDeletingEveryClusterAdminDoesNotReopenTheClaim(t *testing.T) {
	authorization := &fakeAuthorizationClient{}
	server := newFirstAdminServer(newFakeUserStore(), authorization)

	first, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "auth0|first",
		Email:       "first@example.com",
	})
	require.NoError(t, err)
	require.Equal(t, []string{identityObject(first)}, authorization.clusterAdmins())

	adminContext := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-identity-id", uuid.NewString()))
	_, err = server.DeleteUser(adminContext, &usersv1.DeleteUserRequest{
		IdentityId: first.GetUser().GetMeta().GetId(),
	})
	require.NoError(t, err)
	require.Empty(t, authorization.clusterAdmins())

	next, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "auth0|next",
		Email:       "next@example.com",
	})
	require.NoError(t, err)
	require.True(t, next.GetCreated())
	require.Empty(t, authorization.clusterAdmins())
}

func TestFailedGrantDoesNotHandTheClaimToTheNextSignIn(t *testing.T) {
	authorization := &fakeAuthorizationClient{writeErr: fmt.Errorf("openfga unavailable")}
	server := newFirstAdminServer(newFakeUserStore(), authorization)

	_, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "auth0|first",
		Email:       "first@example.com",
	})
	require.ErrorContains(t, err, "grant cluster admin")

	authorization.writeErr = nil
	next, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "auth0|next",
		Email:       "next@example.com",
	})
	require.NoError(t, err)
	require.True(t, next.GetCreated())
	require.Empty(t, authorization.clusterAdmins())
}

func newFirstAdminServer(store userStore, authorization *fakeAuthorizationClient, options ...Option) *Server {
	return NewWithGroups(store, authorization, &fakeIdentityClient{}, &fakeZitiManagementClient{}, nil, options...)
}

func identityObject(response *usersv1.ResolveOrCreateUserResponse) string {
	return identityObjectPrefix + response.GetUser().GetMeta().GetId()
}
