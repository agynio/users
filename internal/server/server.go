package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	"github.com/agynio/users/internal/apitoken"
	"github.com/agynio/users/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Server struct {
	usersv1.UnimplementedUsersServiceServer
	store *store.Store
}

func New(store *store.Store) *Server {
	return &Server{store: store}
}

func (s *Server) ResolveOrCreateUser(ctx context.Context, req *usersv1.ResolveOrCreateUserRequest) (*usersv1.ResolveOrCreateUserResponse, error) {
	oidcSubject := req.GetOidcSubject()
	if oidcSubject == "" {
		return nil, status.Error(codes.InvalidArgument, "oidc_subject must be provided")
	}
	user, created, err := s.store.ResolveOrCreateUser(ctx, store.UserInput{
		OIDCSubject: oidcSubject,
		Name:        req.GetName(),
		Email:       req.GetEmail(),
		PhotoURL:    req.GetPhotoUrl(),
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &usersv1.ResolveOrCreateUserResponse{User: toProtoUser(user), Created: created}, nil
}

func (s *Server) GetUser(ctx context.Context, req *usersv1.GetUserRequest) (*usersv1.GetUserResponse, error) {
	id, err := parseUUID(req.GetIdentityId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "identity_id: %v", err)
	}
	user, err := s.store.GetUser(ctx, id)
	if err != nil {
		return nil, toStatusError(err)
	}
	return &usersv1.GetUserResponse{User: toProtoUser(user)}, nil
}

func (s *Server) GetUserByOIDCSubject(ctx context.Context, req *usersv1.GetUserByOIDCSubjectRequest) (*usersv1.GetUserByOIDCSubjectResponse, error) {
	oidcSubject := req.GetOidcSubject()
	if oidcSubject == "" {
		return nil, status.Error(codes.InvalidArgument, "oidc_subject must be provided")
	}
	user, err := s.store.GetUserByOIDCSubject(ctx, oidcSubject)
	if err != nil {
		return nil, toStatusError(err)
	}
	return &usersv1.GetUserByOIDCSubjectResponse{User: toProtoUser(user)}, nil
}

func (s *Server) BatchGetUsers(ctx context.Context, req *usersv1.BatchGetUsersRequest) (*usersv1.BatchGetUsersResponse, error) {
	identityIDs := req.GetIdentityIds()
	if len(identityIDs) == 0 {
		return &usersv1.BatchGetUsersResponse{Users: nil}, nil
	}
	if len(identityIDs) > 100 {
		return nil, status.Errorf(codes.InvalidArgument, "batch size %d exceeds maximum of 100", len(identityIDs))
	}

	ids := make([]uuid.UUID, 0, len(identityIDs))
	for i, identityID := range identityIDs {
		id, err := parseUUID(identityID)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "identity_ids[%d]: %v", i, err)
		}
		ids = append(ids, id)
	}

	users, err := s.store.BatchGetUsers(ctx, ids)
	if err != nil {
		return nil, toStatusError(err)
	}
	protoUsers := make([]*usersv1.User, 0, len(users))
	for _, user := range users {
		protoUsers = append(protoUsers, toProtoUser(user))
	}
	return &usersv1.BatchGetUsersResponse{Users: protoUsers}, nil
}

func (s *Server) UpdateUser(ctx context.Context, req *usersv1.UpdateUserRequest) (*usersv1.UpdateUserResponse, error) {
	id, err := parseUUID(req.GetIdentityId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "identity_id: %v", err)
	}
	if req.Name == nil && req.Email == nil && req.PhotoUrl == nil {
		return nil, status.Error(codes.InvalidArgument, "at least one field must be provided")
	}

	update := store.UserUpdate{}
	if req.Name != nil {
		value := req.GetName()
		update.Name = &value
	}
	if req.Email != nil {
		value := req.GetEmail()
		update.Email = &value
	}
	if req.PhotoUrl != nil {
		value := req.GetPhotoUrl()
		update.PhotoURL = &value
	}

	user, err := s.store.UpdateUser(ctx, id, update)
	if err != nil {
		return nil, toStatusError(err)
	}
	return &usersv1.UpdateUserResponse{User: toProtoUser(user)}, nil
}

func (s *Server) CreateAPIToken(ctx context.Context, req *usersv1.CreateAPITokenRequest) (*usersv1.CreateAPITokenResponse, error) {
	identityID, err := identityIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name must be provided")
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		if err := req.ExpiresAt.CheckValid(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "expires_at: %v", err)
		}
		value := req.ExpiresAt.AsTime()
		now := time.Now()
		if !value.After(now) {
			return nil, status.Error(codes.InvalidArgument, "expires_at must be in the future")
		}
		expiresAt = &value
	}

	generated, err := apitoken.Generate()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "generate token: %v", err)
	}

	token, err := s.store.CreateAPIToken(ctx, store.CreateAPITokenInput{
		IdentityID:  identityID,
		Name:        name,
		TokenHash:   generated.Hash,
		TokenPrefix: generated.TokenPrefix,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return nil, toStatusError(err)
	}

	return &usersv1.CreateAPITokenResponse{
		Token:          toProtoAPIToken(token),
		PlaintextToken: generated.Plaintext,
	}, nil
}

func (s *Server) ListAPITokens(ctx context.Context, _ *usersv1.ListAPITokensRequest) (*usersv1.ListAPITokensResponse, error) {
	identityID, err := identityIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	tokens, err := s.store.ListAPITokens(ctx, identityID)
	if err != nil {
		return nil, toStatusError(err)
	}

	protoTokens := make([]*usersv1.APIToken, 0, len(tokens))
	for _, token := range tokens {
		protoTokens = append(protoTokens, toProtoAPIToken(token))
	}

	return &usersv1.ListAPITokensResponse{Tokens: protoTokens}, nil
}

func (s *Server) RevokeAPIToken(ctx context.Context, req *usersv1.RevokeAPITokenRequest) (*usersv1.RevokeAPITokenResponse, error) {
	identityID, err := identityIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	tokenID, err := parseUUID(req.GetTokenId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "token_id: %v", err)
	}

	if err := s.store.RevokeAPIToken(ctx, tokenID, identityID); err != nil {
		return nil, toStatusError(err)
	}

	return &usersv1.RevokeAPITokenResponse{}, nil
}

func (s *Server) ResolveAPIToken(ctx context.Context, req *usersv1.ResolveAPITokenRequest) (*usersv1.ResolveAPITokenResponse, error) {
	tokenHash := req.GetTokenHash()
	if tokenHash == "" {
		return nil, status.Error(codes.InvalidArgument, "token_hash must be provided")
	}

	token, err := s.store.ResolveAPIToken(ctx, tokenHash)
	if err != nil {
		return nil, toStatusError(err)
	}

	if token.ExpiresAt != nil {
		now := time.Now()
		if !token.ExpiresAt.After(now) {
			return nil, status.Error(codes.Unauthenticated, "api token expired")
		}
	}

	return &usersv1.ResolveAPITokenResponse{
		IdentityId: token.IdentityID.String(),
		Token:      toProtoAPIToken(token),
	}, nil
}

func parseUUID(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.UUID{}, fmt.Errorf("value is empty")
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, err
	}
	return id, nil
}

func identityIDFromContext(ctx context.Context) (uuid.UUID, error) {
	metadataValues, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "x-identity-id metadata is required")
	}
	identityValues := metadataValues.Get("x-identity-id")
	if len(identityValues) == 0 || identityValues[0] == "" {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "x-identity-id metadata is required")
	}
	identityID, err := parseUUID(identityValues[0])
	if err != nil {
		return uuid.UUID{}, status.Errorf(codes.Unauthenticated, "x-identity-id: %v", err)
	}
	return identityID, nil
}

func toStatusError(err error) error {
	var notFound *store.NotFoundError
	if errors.As(err, &notFound) {
		return status.Error(codes.NotFound, notFound.Error())
	}
	var exists *store.AlreadyExistsError
	if errors.As(err, &exists) {
		return status.Error(codes.AlreadyExists, exists.Error())
	}
	return status.Errorf(codes.Internal, "internal error: %v", err)
}
