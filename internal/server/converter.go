package server

import (
	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	"github.com/agynio/users/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func toProtoEntityMeta(meta store.EntityMeta) *usersv1.EntityMeta {
	return &usersv1.EntityMeta{
		Id:        meta.ID.String(),
		CreatedAt: timestamppb.New(meta.CreatedAt),
		UpdatedAt: timestamppb.New(meta.UpdatedAt),
	}
}

func toProtoUser(user store.User) *usersv1.User {
	return &usersv1.User{
		Meta:        toProtoEntityMeta(user.Meta),
		OidcSubject: user.OIDCSubject,
		Name:        user.Name,
		Email:       user.Email,
		PhotoUrl:    user.PhotoURL,
	}
}

func toProtoAPIToken(token store.APIToken) *usersv1.APIToken {
	var expiresAt *timestamppb.Timestamp
	if token.ExpiresAt != nil {
		expiresAt = timestamppb.New(*token.ExpiresAt)
	}
	var lastUsedAt *timestamppb.Timestamp
	if token.LastUsedAt != nil {
		lastUsedAt = timestamppb.New(*token.LastUsedAt)
	}
	return &usersv1.APIToken{
		Id:          token.ID.String(),
		IdentityId:  token.IdentityID.String(),
		Name:        token.Name,
		TokenPrefix: token.TokenPrefix,
		ExpiresAt:   expiresAt,
		CreatedAt:   timestamppb.New(token.CreatedAt),
		LastUsedAt:  lastUsedAt,
	}
}
