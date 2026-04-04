//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestUsersServiceE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	conn, err := grpc.DialContext(ctx, usersAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
	})

	client := usersv1.NewUsersServiceClient(conn)

	t.Run("ResolveOrCreateUser", func(t *testing.T) {
		subject := "oidc-" + uuid.NewString()
		createResp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
			OidcSubject: subject,
			Name:        "Alice",
			Email:       "alice@example.com",
			PhotoUrl:    "https://example.com/photo.png",
		})
		require.NoError(t, err)
		require.True(t, createResp.Created)
		require.Equal(t, subject, createResp.User.OidcSubject)

		resolveResp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
			OidcSubject: subject,
			Name:        "Alice Updated",
			Email:       "alice-updated@example.com",
			PhotoUrl:    "https://example.com/updated.png",
		})
		require.NoError(t, err)
		require.False(t, resolveResp.Created)
		require.Equal(t, createResp.User.Meta.Id, resolveResp.User.Meta.Id)
	})

	t.Run("GetUser", func(t *testing.T) {
		subject := "oidc-" + uuid.NewString()
		createResp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
			OidcSubject: subject,
			Name:        "Bob",
			Email:       "bob@example.com",
			PhotoUrl:    "https://example.com/bob.png",
		})
		require.NoError(t, err)

		getResp, err := client.GetUser(ctx, &usersv1.GetUserRequest{IdentityId: createResp.User.Meta.Id})
		require.NoError(t, err)
		require.Equal(t, subject, getResp.User.OidcSubject)
		require.Equal(t, "Bob", getResp.User.Name)
	})

	t.Run("GetUserByOIDCSubject", func(t *testing.T) {
		subject := "oidc-" + uuid.NewString()
		createResp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
			OidcSubject: subject,
			Name:        "Charlie",
			Email:       "charlie@example.com",
			PhotoUrl:    "https://example.com/charlie.png",
		})
		require.NoError(t, err)

		getResp, err := client.GetUserByOIDCSubject(ctx, &usersv1.GetUserByOIDCSubjectRequest{OidcSubject: subject})
		require.NoError(t, err)
		require.Equal(t, createResp.User.Meta.Id, getResp.User.Meta.Id)
	})

	t.Run("BatchGetUsers", func(t *testing.T) {
		firstSubject := "oidc-" + uuid.NewString()
		secondSubject := "oidc-" + uuid.NewString()
		firstResp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
			OidcSubject: firstSubject,
			Name:        "Dana",
			Email:       "dana@example.com",
			PhotoUrl:    "https://example.com/dana.png",
		})
		require.NoError(t, err)
		secondResp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
			OidcSubject: secondSubject,
			Name:        "Elliot",
			Email:       "elliot@example.com",
			PhotoUrl:    "https://example.com/elliot.png",
		})
		require.NoError(t, err)

		batchResp, err := client.BatchGetUsers(ctx, &usersv1.BatchGetUsersRequest{
			IdentityIds: []string{firstResp.User.Meta.Id, secondResp.User.Meta.Id, uuid.NewString()},
		})
		require.NoError(t, err)
		require.Len(t, batchResp.Users, 2)
		require.True(t, hasUserID(batchResp.Users, firstResp.User.Meta.Id))
		require.True(t, hasUserID(batchResp.Users, secondResp.User.Meta.Id))
	})

	t.Run("UpdateUser", func(t *testing.T) {
		subject := "oidc-" + uuid.NewString()
		createResp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
			OidcSubject: subject,
			Name:        "Frank",
			Email:       "frank@example.com",
			PhotoUrl:    "https://example.com/frank.png",
		})
		require.NoError(t, err)

		updateResp, err := client.UpdateUser(ctx, &usersv1.UpdateUserRequest{
			IdentityId: createResp.User.Meta.Id,
			Name:       proto.String("Frank Updated"),
			PhotoUrl:   proto.String("https://example.com/frank-updated.png"),
		})
		require.NoError(t, err)
		require.Equal(t, "Frank Updated", updateResp.User.Name)
		require.Equal(t, "https://example.com/frank-updated.png", updateResp.User.PhotoUrl)
	})

	t.Run("ListUsers", func(t *testing.T) {
		userInputs := []struct {
			name  string
			email string
		}{
			{name: "List User One", email: "list-one@example.com"},
			{name: "List User Two", email: "list-two@example.com"},
			{name: "List User Three", email: "list-three@example.com"},
		}
		createdIDs := make([]string, 0, len(userInputs))
		for _, input := range userInputs {
			subject := "oidc-" + uuid.NewString()
			createResp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
				OidcSubject: subject,
				Name:        input.name,
				Email:       input.email,
				PhotoUrl:    "https://example.com/list.png",
			})
			require.NoError(t, err)
			createdIDs = append(createdIDs, createResp.User.Meta.Id)
		}

		pageToken := ""
		drained := false
		var listed []*usersv1.User
		for i := 0; i < 20; i++ {
			resp, err := client.ListUsers(ctx, &usersv1.ListUsersRequest{PageSize: 1, PageToken: pageToken})
			require.NoError(t, err)
			if i == 0 {
				require.NotEmpty(t, resp.Users)
				require.NotEmpty(t, resp.NextPageToken)
			}
			listed = append(listed, resp.Users...)
			pageToken = resp.NextPageToken
			if pageToken == "" {
				drained = true
				break
			}
		}
		require.True(t, drained)
		for _, id := range createdIDs {
			require.True(t, hasUserID(listed, id))
		}

		firstPageResp, err := client.ListUsers(ctx, &usersv1.ListUsersRequest{PageSize: 1})
		require.NoError(t, err)
		require.NotEmpty(t, firstPageResp.Users)

		defaultPageResp, err := client.ListUsers(ctx, &usersv1.ListUsersRequest{PageToken: ""})
		require.NoError(t, err)
		require.NotEmpty(t, defaultPageResp.Users)
		require.Equal(t, firstPageResp.Users[0].GetMeta().GetId(), defaultPageResp.Users[0].GetMeta().GetId())

		_, err = client.ListUsers(ctx, &usersv1.ListUsersRequest{PageToken: "invalid-token"})
		requireStatusCode(t, err, codes.InvalidArgument)
	})

	t.Run("NegativePaths", func(t *testing.T) {
		_, err := client.GetUser(ctx, &usersv1.GetUserRequest{IdentityId: uuid.NewString()})
		requireStatusCode(t, err, codes.NotFound)

		_, err = client.GetUser(ctx, &usersv1.GetUserRequest{IdentityId: "not-a-uuid"})
		requireStatusCode(t, err, codes.InvalidArgument)

		_, err = client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{})
		requireStatusCode(t, err, codes.InvalidArgument)

		_, err = client.GetUserByOIDCSubject(ctx, &usersv1.GetUserByOIDCSubjectRequest{OidcSubject: ""})
		requireStatusCode(t, err, codes.InvalidArgument)

		_, err = client.BatchGetUsers(ctx, &usersv1.BatchGetUsersRequest{IdentityIds: []string{"bad"}})
		requireStatusCode(t, err, codes.InvalidArgument)

		ids := make([]string, 101)
		for i := range ids {
			ids[i] = uuid.NewString()
		}
		_, err = client.BatchGetUsers(ctx, &usersv1.BatchGetUsersRequest{IdentityIds: ids})
		requireStatusCode(t, err, codes.InvalidArgument)

		_, err = client.UpdateUser(ctx, &usersv1.UpdateUserRequest{IdentityId: uuid.NewString()})
		requireStatusCode(t, err, codes.InvalidArgument)
	})
}

func hasUserID(users []*usersv1.User, id string) bool {
	for _, user := range users {
		if user.GetMeta().GetId() == id {
			return true
		}
	}
	return false
}

func requireStatusCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	statusErr, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, code, statusErr.Code())
}
