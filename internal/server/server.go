package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	authorizationv1 "github.com/agynio/users/.gen/go/agynio/api/authorization/v1"
	identityv1 "github.com/agynio/users/.gen/go/agynio/api/identity/v1"
	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	"github.com/agynio/users/internal/apitoken"
	"github.com/agynio/users/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	identityObjectPrefix = "identity:"
	clusterObject        = "cluster:global"
	adminRelation        = "admin"
)

type Server struct {
	usersv1.UnimplementedUsersServiceServer
	store               *store.Store
	authorizationClient authorizationv1.AuthorizationServiceClient
	identityClient      identityv1.IdentityServiceClient
}

func New(store *store.Store, authorizationClient authorizationv1.AuthorizationServiceClient, identityClient identityv1.IdentityServiceClient) *Server {
	return &Server{store: store, authorizationClient: authorizationClient, identityClient: identityClient}
}

func identityIDFromContext(ctx context.Context) (uuid.UUID, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return uuid.Nil, fmt.Errorf("no metadata in context")
	}
	values := md.Get("x-identity-id")
	if len(values) == 0 || values[0] == "" {
		return uuid.Nil, fmt.Errorf("x-identity-id not found in metadata")
	}
	return uuid.Parse(values[0])
}

func (s *Server) checkClusterAdmin(ctx context.Context, identityID uuid.UUID) (bool, error) {
	response, err := s.authorizationClient.Check(ctx, &authorizationv1.CheckRequest{
		TupleKey: &authorizationv1.TupleKey{
			User:     fmt.Sprintf("%s%s", identityObjectPrefix, identityID.String()),
			Relation: adminRelation,
			Object:   clusterObject,
		},
	})
	if err != nil {
		return false, err
	}
	return response.GetAllowed(), nil
}

func (s *Server) requireClusterAdmin(ctx context.Context) (uuid.UUID, error) {
	identityID, err := identityIDFromContext(ctx)
	if err != nil {
		return uuid.Nil, status.Errorf(codes.Unauthenticated, "identity not available: %v", err)
	}
	allowed, err := s.checkClusterAdmin(ctx, identityID)
	if err != nil {
		return uuid.Nil, status.Errorf(codes.Internal, "authorization check: %v", err)
	}
	if !allowed {
		return uuid.Nil, status.Error(codes.PermissionDenied, "cluster admin required")
	}
	return identityID, nil
}

func (s *Server) resolveClusterRole(ctx context.Context, identityID uuid.UUID) (usersv1.ClusterRole, error) {
	allowed, err := s.checkClusterAdmin(ctx, identityID)
	if err != nil {
		return usersv1.ClusterRole_CLUSTER_ROLE_UNSPECIFIED, err
	}
	if allowed {
		return usersv1.ClusterRole_CLUSTER_ROLE_ADMIN, nil
	}
	return usersv1.ClusterRole_CLUSTER_ROLE_UNSPECIFIED, nil
}

func (s *Server) syncClusterRole(ctx context.Context, identityID uuid.UUID, role usersv1.ClusterRole) error {
	tuple := &authorizationv1.TupleKey{
		User:     fmt.Sprintf("%s%s", identityObjectPrefix, identityID.String()),
		Relation: adminRelation,
		Object:   clusterObject,
	}
	request := &authorizationv1.WriteRequest{}
	switch role {
	case usersv1.ClusterRole_CLUSTER_ROLE_ADMIN:
		request.Writes = []*authorizationv1.TupleKey{tuple}
	case usersv1.ClusterRole_CLUSTER_ROLE_UNSPECIFIED:
		request.Deletes = []*authorizationv1.TupleKey{tuple}
	default:
		return fmt.Errorf("unsupported cluster role: %s", role.String())
	}
	_, err := s.authorizationClient.Write(ctx, request)
	return err
}

func (s *Server) ResolveOrCreateUser(ctx context.Context, req *usersv1.ResolveOrCreateUserRequest) (*usersv1.ResolveOrCreateUserResponse, error) {
	oidcSubject := req.GetOidcSubject()
	if oidcSubject == "" {
		return nil, status.Error(codes.InvalidArgument, "oidc_subject must be provided")
	}
	user, created, count, err := s.store.ResolveOrCreateUser(ctx, store.UserInput{
		OIDCSubject: oidcSubject,
		Name:        req.GetName(),
		Email:       req.GetEmail(),
		PhotoURL:    req.GetPhotoUrl(),
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	if created && count == 1 {
		if err := s.syncClusterRole(ctx, user.Meta.ID, usersv1.ClusterRole_CLUSTER_ROLE_ADMIN); err != nil {
			return nil, status.Errorf(codes.Internal, "grant first user admin: %v", err)
		}
	}
	return &usersv1.ResolveOrCreateUserResponse{User: toProtoUser(user), Created: created}, nil
}

func (s *Server) GetUser(ctx context.Context, req *usersv1.GetUserRequest) (*usersv1.GetUserResponse, error) {
	if _, err := s.requireClusterAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := parseUUID(req.GetIdentityId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "identity_id: %v", err)
	}
	user, err := s.store.GetUser(ctx, id)
	if err != nil {
		return nil, toStatusError(err)
	}
	clusterRole, err := s.resolveClusterRole(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "authorization check: %v", err)
	}
	return &usersv1.GetUserResponse{User: toProtoUser(user), ClusterRole: clusterRole}, nil
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
	if _, err := s.requireClusterAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := parseUUID(req.GetIdentityId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "identity_id: %v", err)
	}
	if req.ClusterRole != nil {
		if err := validateClusterRole(req.GetClusterRole()); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "cluster_role: %v", err)
		}
	}
	if req.Name == nil && req.Email == nil && req.Nickname == nil && req.PhotoUrl == nil && req.ClusterRole == nil {
		return nil, status.Error(codes.InvalidArgument, "at least one field must be provided")
	}

	update := store.UserUpdate{}
	updateUser := false
	if req.Name != nil {
		value := req.GetName()
		update.Name = &value
		updateUser = true
	}
	if req.Email != nil {
		value := req.GetEmail()
		update.Email = &value
		updateUser = true
	}
	if req.Nickname != nil {
		value := req.GetNickname()
		update.Nickname = &value
		updateUser = true
	}
	if req.PhotoUrl != nil {
		value := req.GetPhotoUrl()
		update.PhotoURL = &value
		updateUser = true
	}

	var user store.User
	if updateUser {
		user, err = s.store.UpdateUser(ctx, id, update)
	} else {
		user, err = s.store.GetUser(ctx, id)
	}
	if err != nil {
		return nil, toStatusError(err)
	}

	if req.ClusterRole != nil {
		if err := s.syncClusterRole(ctx, id, req.GetClusterRole()); err != nil {
			return nil, status.Errorf(codes.Internal, "sync cluster role: %v", err)
		}
	}
	return &usersv1.UpdateUserResponse{User: toProtoUser(user)}, nil
}

func (s *Server) CreateAPIToken(ctx context.Context, req *usersv1.CreateAPITokenRequest) (*usersv1.CreateAPITokenResponse, error) {
	identityID, err := identityIDFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "identity not available: %v", err)
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
		return nil, status.Errorf(codes.Unauthenticated, "identity not available: %v", err)
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
		return nil, status.Errorf(codes.Unauthenticated, "identity not available: %v", err)
	}

	tokenID, err := parseUUID(req.GetTokenId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "token_id: %v", err)
	}

	if err := s.store.RevokeAPIToken(ctx, identityID, tokenID); err != nil {
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
	if token.ExpiresAt != nil && !token.ExpiresAt.After(time.Now()) {
		return nil, status.Error(codes.Unauthenticated, "api token expired")
	}

	return &usersv1.ResolveAPITokenResponse{
		IdentityId: token.IdentityID.String(),
		Token:      toProtoAPIToken(token),
	}, nil
}

func (s *Server) GetMe(ctx context.Context, _ *usersv1.GetMeRequest) (*usersv1.GetMeResponse, error) {
	identityID, err := identityIDFromContext(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "identity not available: %v", err)
	}
	user, err := s.store.GetUser(ctx, identityID)
	if err != nil {
		return nil, toStatusError(err)
	}
	clusterRole, err := s.resolveClusterRole(ctx, identityID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "authorization check: %v", err)
	}
	return &usersv1.GetMeResponse{User: toProtoUser(user), ClusterRole: clusterRole}, nil
}

func (s *Server) ListUsers(ctx context.Context, req *usersv1.ListUsersRequest) (*usersv1.ListUsersResponse, error) {
	if _, err := s.requireClusterAdmin(ctx); err != nil {
		return nil, err
	}
	cursor, err := decodePageCursor(req.GetPageToken())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}
	result, err := s.store.ListUsers(ctx, req.GetPageSize(), cursor)
	if err != nil {
		return nil, toStatusError(err)
	}
	users, nextToken := mapListResult(result.Users, result.NextCursor, toProtoUser)
	return &usersv1.ListUsersResponse{Users: users, NextPageToken: nextToken}, nil
}

func (s *Server) CreateUser(ctx context.Context, req *usersv1.CreateUserRequest) (*usersv1.CreateUserResponse, error) {
	if _, err := s.requireClusterAdmin(ctx); err != nil {
		return nil, err
	}
	oidcSubject := req.GetOidcSubject()
	if oidcSubject == "" {
		return nil, status.Error(codes.InvalidArgument, "oidc_subject must be provided")
	}
	if err := validateClusterRole(req.GetClusterRole()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "cluster_role: %v", err)
	}

	user, err := s.store.CreateUser(ctx, store.UserInput{
		OIDCSubject: oidcSubject,
		Name:        req.GetName(),
		Nickname:    req.GetNickname(),
		PhotoURL:    req.GetPhotoUrl(),
	})
	if err != nil {
		return nil, toStatusError(err)
	}

	_, err = s.identityClient.RegisterIdentity(ctx, &identityv1.RegisterIdentityRequest{
		IdentityId:   user.Meta.ID.String(),
		IdentityType: identityv1.IdentityType_IDENTITY_TYPE_USER,
	})
	if err != nil {
		_ = s.store.DeleteUser(ctx, user.Meta.ID)
		return nil, status.Errorf(codes.Internal, "register identity: %v", err)
	}

	if req.GetClusterRole() == usersv1.ClusterRole_CLUSTER_ROLE_ADMIN {
		if err := s.syncClusterRole(ctx, user.Meta.ID, req.GetClusterRole()); err != nil {
			_ = s.store.DeleteUser(ctx, user.Meta.ID)
			// TODO: Identity deletion is not supported yet; this leaves an orphaned identity.
			return nil, status.Errorf(codes.Internal, "sync cluster role: %v", err)
		}
	}
	return &usersv1.CreateUserResponse{User: toProtoUser(user)}, nil
}

func (s *Server) DeleteUser(ctx context.Context, req *usersv1.DeleteUserRequest) (*usersv1.DeleteUserResponse, error) {
	if _, err := s.requireClusterAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := parseUUID(req.GetIdentityId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "identity_id: %v", err)
	}
	identityObject := fmt.Sprintf("%s%s", identityObjectPrefix, id.String())
	if err := s.syncClusterRole(ctx, id, usersv1.ClusterRole_CLUSTER_ROLE_UNSPECIFIED); err != nil {
		return nil, status.Errorf(codes.Internal, "sync cluster role: %v", err)
	}

	authResponse, err := s.authorizationClient.ListObjects(ctx, &authorizationv1.ListObjectsRequest{
		Type:     "organization",
		Relation: "member",
		User:     identityObject,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list organization memberships: %v", err)
	}
	if len(authResponse.Objects) > 0 {
		deletes := make([]*authorizationv1.TupleKey, 0, len(authResponse.Objects))
		for _, object := range authResponse.Objects {
			deletes = append(deletes, &authorizationv1.TupleKey{
				User:     identityObject,
				Relation: "member",
				Object:   object,
			})
		}
		if len(deletes) > 0 {
			if _, err := s.authorizationClient.Write(ctx, &authorizationv1.WriteRequest{Deletes: deletes}); err != nil {
				return nil, status.Errorf(codes.Internal, "remove organization memberships: %v", err)
			}
		}
	}

	if err := s.store.DeleteUser(ctx, id); err != nil {
		return nil, toStatusError(err)
	}

	// TODO: Delete identity records once Identity supports removal.
	return &usersv1.DeleteUserResponse{}, nil
}

func decodePageCursor(token string) (*store.PageCursor, error) {
	if token == "" {
		return nil, nil
	}
	id, err := store.DecodePageToken(token)
	if err != nil {
		return nil, err
	}
	return &store.PageCursor{AfterID: id}, nil
}

func mapListResult[T any, P any](items []T, nextCursor *store.PageCursor, convert func(T) P) ([]P, string) {
	results := make([]P, len(items))
	for i, item := range items {
		results[i] = convert(item)
	}
	if nextCursor == nil {
		return results, ""
	}
	return results, store.EncodePageToken(nextCursor.AfterID)
}

func validateClusterRole(role usersv1.ClusterRole) error {
	switch role {
	case usersv1.ClusterRole_CLUSTER_ROLE_ADMIN, usersv1.ClusterRole_CLUSTER_ROLE_UNSPECIFIED:
		return nil
	default:
		return fmt.Errorf("unsupported role %s", role.String())
	}
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
