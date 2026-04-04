//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"regexp"
	"testing"
	"time"

	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	"github.com/agynio/users/internal/apitoken"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAPITokensE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	conn, err := grpc.DialContext(ctx, usersAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
	})

	client := usersv1.NewUsersServiceClient(conn)
	plainTextFormat := regexp.MustCompile(`^agyn_[0-9A-Za-z]{44}$`)

	t.Run("CreateAPIToken", func(t *testing.T) {
		identityID, authedCtx := createUserContext(t, ctx, client)
		resp, err := client.CreateAPIToken(authedCtx, &usersv1.CreateAPITokenRequest{Name: "CLI"})
		require.NoError(t, err)
		require.NotNil(t, resp.Token)
		require.True(t, plainTextFormat.MatchString(resp.GetPlaintextToken()))
		require.Equal(t, resp.GetPlaintextToken()[:8], resp.Token.TokenPrefix)
		require.Equal(t, identityID, resp.Token.IdentityId)
		require.Equal(t, "CLI", resp.Token.Name)
		require.NotNil(t, resp.Token.CreatedAt)
	})

	t.Run("CreateAPITokenWithExpiration", func(t *testing.T) {
		identityID, authedCtx := createUserContext(t, ctx, client)
		expiresAt := time.Now().Add(2 * time.Hour).UTC()
		resp, err := client.CreateAPIToken(authedCtx, &usersv1.CreateAPITokenRequest{
			Name:      "expires",
			ExpiresAt: timestamppb.New(expiresAt),
		})
		require.NoError(t, err)
		require.Equal(t, identityID, resp.Token.IdentityId)
		require.WithinDuration(t, expiresAt, resp.Token.ExpiresAt.AsTime(), time.Second)
	})

	t.Run("CreateAPITokenValidation", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		_, err := client.CreateAPIToken(authedCtx, &usersv1.CreateAPITokenRequest{})
		requireStatusCode(t, err, codes.InvalidArgument)
	})

	t.Run("CreateAPITokenPastExpiration", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		past := time.Now().Add(-1 * time.Hour)
		_, err := client.CreateAPIToken(authedCtx, &usersv1.CreateAPITokenRequest{
			Name:      "expired",
			ExpiresAt: timestamppb.New(past),
		})
		requireStatusCode(t, err, codes.InvalidArgument)
	})

	t.Run("ListAPITokens", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		firstResp := createAPIToken(t, authedCtx, client, "first", nil)
		time.Sleep(10 * time.Millisecond)
		secondResp := createAPIToken(t, authedCtx, client, "second", nil)

		listResp, err := client.ListAPITokens(authedCtx, &usersv1.ListAPITokensRequest{})
		require.NoError(t, err)
		require.Len(t, listResp.Tokens, 2)
		require.Equal(t, secondResp.Token.Id, listResp.Tokens[0].Id)
		require.Equal(t, firstResp.Token.Id, listResp.Tokens[1].Id)
	})

	t.Run("ListAPITokensEmpty", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		listResp, err := client.ListAPITokens(authedCtx, &usersv1.ListAPITokensRequest{})
		require.NoError(t, err)
		require.Empty(t, listResp.Tokens)
	})

	t.Run("RevokeAPIToken", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		createResp := createAPIToken(t, authedCtx, client, "revoke", nil)

		_, err := client.RevokeAPIToken(authedCtx, &usersv1.RevokeAPITokenRequest{TokenId: createResp.Token.Id})
		require.NoError(t, err)

		listResp, err := client.ListAPITokens(authedCtx, &usersv1.ListAPITokensRequest{})
		require.NoError(t, err)
		require.Empty(t, listResp.Tokens)
	})

	t.Run("RevokeAPITokenNotFound", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		_, err := client.RevokeAPIToken(authedCtx, &usersv1.RevokeAPITokenRequest{TokenId: uuid.NewString()})
		requireStatusCode(t, err, codes.NotFound)
	})

	t.Run("RevokeAPITokenCrossUser", func(t *testing.T) {
		_, firstCtx := createUserContext(t, ctx, client)
		createResp := createAPIToken(t, firstCtx, client, "owner", nil)
		_, secondCtx := createUserContext(t, ctx, client)

		_, err := client.RevokeAPIToken(secondCtx, &usersv1.RevokeAPITokenRequest{TokenId: createResp.Token.Id})
		requireStatusCode(t, err, codes.NotFound)
	})

	t.Run("ResolveAPIToken", func(t *testing.T) {
		identityID, authedCtx := createUserContext(t, ctx, client)
		createResp := createAPIToken(t, authedCtx, client, "resolve", nil)
		resolveResp, err := client.ResolveAPIToken(ctx, &usersv1.ResolveAPITokenRequest{TokenHash: apitoken.Hash(createResp.PlaintextToken)})
		require.NoError(t, err)
		require.Equal(t, identityID, resolveResp.IdentityId)
		require.NotNil(t, resolveResp.Token)
		require.NotNil(t, resolveResp.Token.LastUsedAt)
	})

	t.Run("ResolveAPITokenNotFound", func(t *testing.T) {
		_, err := client.ResolveAPIToken(ctx, &usersv1.ResolveAPITokenRequest{TokenHash: apitoken.Hash("agyn_" + uuid.NewString())})
		requireStatusCode(t, err, codes.NotFound)
	})

	t.Run("ResolveAPITokenExpired", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		expiresAt := time.Now().Add(300 * time.Millisecond)
		createResp := createAPIToken(t, authedCtx, client, "soon", &expiresAt)
		if wait := time.Until(expiresAt.Add(50 * time.Millisecond)); wait > 0 {
			time.Sleep(wait)
		}
		_, err := client.ResolveAPIToken(ctx, &usersv1.ResolveAPITokenRequest{TokenHash: apitoken.Hash(createResp.PlaintextToken)})
		requireStatusCode(t, err, codes.Unauthenticated)
	})

	t.Run("ResolveAPITokenLastUsedAtUpdates", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		createResp := createAPIToken(t, authedCtx, client, "repeat", nil)
		tokenHash := apitoken.Hash(createResp.PlaintextToken)

		firstResp, err := client.ResolveAPIToken(ctx, &usersv1.ResolveAPITokenRequest{TokenHash: tokenHash})
		require.NoError(t, err)
		firstUsed := firstResp.Token.LastUsedAt.AsTime()

		time.Sleep(10 * time.Millisecond)
		secondResp, err := client.ResolveAPIToken(ctx, &usersv1.ResolveAPITokenRequest{TokenHash: tokenHash})
		require.NoError(t, err)
		secondUsed := secondResp.Token.LastUsedAt.AsTime()
		require.True(t, secondUsed.After(firstUsed))
	})
}

func createUserContext(t *testing.T, ctx context.Context, client usersv1.UsersServiceClient) (string, context.Context) {
	t.Helper()
	subject := "oidc-" + uuid.NewString()
	resp, err := client.ResolveOrCreateUser(ctx, &usersv1.ResolveOrCreateUserRequest{
		OidcSubject: subject,
		Name:        "Token User",
		Email:       "token-user",
		PhotoUrl:    "https://example.com/token-user.png",
	})
	require.NoError(t, err)
	identityID := resp.User.Meta.Id
	return identityID, metadata.AppendToOutgoingContext(ctx, "x-identity-id", identityID)
}

func createAPIToken(
	t *testing.T,
	ctx context.Context,
	client usersv1.UsersServiceClient,
	name string,
	expiresAt *time.Time,
) *usersv1.CreateAPITokenResponse {
	t.Helper()
	request := &usersv1.CreateAPITokenRequest{Name: name}
	if expiresAt != nil {
		request.ExpiresAt = timestamppb.New(*expiresAt)
	}
	resp, err := client.CreateAPIToken(ctx, request)
	require.NoError(t, err)
	return resp
}
