package server

import (
  	"context"
  	"errors"
  	"fmt"

  	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
  	"github.com/agynio/users/internal/store"
  	"github.com/google/uuid"
  	"google.golang.org/grpc/codes"
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
  		Nickname:    req.GetNickname(),
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
  	if req.Name == nil && req.Nickname == nil && req.PhotoUrl == nil {
  		return nil, status.Error(codes.InvalidArgument, "at least one field must be provided")
  	}

  	update := store.UserUpdate{}
  	if req.Name != nil {
  		value := req.GetName()
  		update.Name = &value
  	}
  	if req.Nickname != nil {
  		value := req.GetNickname()
  		update.Nickname = &value
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
