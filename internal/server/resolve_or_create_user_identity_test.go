package server

import (
	"context"
	"testing"

	identityv1 "github.com/agynio/users/.gen/go/agynio/api/identity/v1"
	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Records every registration so a resolve of an already-stored user can be
// told apart from a create.
type recordingIdentityClient struct {
	fakeIdentityClient
	registered []string
	err        error
}

func (c *recordingIdentityClient) RegisterIdentity(_ context.Context, req *identityv1.RegisterIdentityRequest, _ ...grpc.CallOption) (*identityv1.RegisterIdentityResponse, error) {
	c.registered = append(c.registered, req.GetIdentityId())
	if c.err != nil {
		return nil, c.err
	}
	return &identityv1.RegisterIdentityResponse{}, nil
}

func resolveUserServer(identity *recordingIdentityClient) (*Server, *fakeUserStore) {
	fakeStore := newFakeUserStore()
	return NewWithGroups(
		fakeStore,
		&fakeAuthorizationClient{},
		identity,
		&fakeZitiManagementClient{},
		&fakeGroupsClient{},
	), fakeStore
}

// A user seeded before RegisterIdentity existed resolves rather than creates,
// and used to keep no identity row at all -- which CreateOrganization reads as
// "not a person".
func TestResolveOrCreateUserRegistersIdentityForExistingUser(t *testing.T) {
	identity := &recordingIdentityClient{}
	server, _ := resolveUserServer(identity)
	req := &usersv1.ResolveOrCreateUserRequest{OidcSubject: "subject-1", Email: "user@agyn.dev", Name: "User"}

	first, err := server.ResolveOrCreateUser(context.Background(), req)
	require.NoError(t, err)
	require.True(t, first.GetCreated())

	second, err := server.ResolveOrCreateUser(context.Background(), req)
	require.NoError(t, err)
	require.False(t, second.GetCreated(), "the second resolve must find the stored user")

	require.Equal(t, first.GetUser().GetMeta().GetId(), second.GetUser().GetMeta().GetId())
	require.Len(t, identity.registered, 2, "the existing user is registered too, not only the created one")
}

// Registration already having happened is the ordinary case on every later
// sign-in, so it must not fail the request.
func TestResolveOrCreateUserToleratesAlreadyRegisteredIdentity(t *testing.T) {
	identity := &recordingIdentityClient{err: status.Error(codes.AlreadyExists, "identity")}
	server, _ := resolveUserServer(identity)

	response, err := server.ResolveOrCreateUser(context.Background(), &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: "subject-2",
		Email:       "other@agyn.dev",
	})
	require.NoError(t, err)
	require.NotEmpty(t, response.GetUser().GetMeta().GetId())
}
