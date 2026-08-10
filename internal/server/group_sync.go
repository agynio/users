package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	authorizationv1 "github.com/agynio/users/.gen/go/agynio/api/authorization/v1"
	groupsv1 "github.com/agynio/users/.gen/go/agynio/api/groups/v1"
	zitimanagementv1 "github.com/agynio/users/.gen/go/agynio/api/ziti_management/v1"
	"github.com/agynio/users/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// identityMetadataKey names the caller on a request. gRPC does not carry
// incoming metadata onto outgoing calls, so a service that dials another on
// behalf of someone has to say who that is.
const identityMetadataKey = "x-identity-id"

const (
	groupMembershipAddedSubject   = "agyn.groups.membership.added"
	groupMembershipRemovedSubject = "agyn.groups.membership.removed"
	groupRoleAttributePrefix      = "group-"
	organizationObjectPrefix      = "organization:"
	organizationType              = "organization"
	organizationMemberRelation    = "member"
)

func (s *Server) userGroupRoleAttributes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if s.groupsClient == nil {
		return []string{}, nil
	}
	groups, err := s.listUserGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	roleAttributes := make([]string, 0, len(groups))
	seen := map[string]struct{}{}
	for _, group := range groups {
		groupID := group.GetMeta().GetId()
		if groupID == "" {
			return nil, fmt.Errorf("groups list returned group without id")
		}
		roleAttribute := groupRoleAttribute(groupID)
		if _, ok := seen[roleAttribute]; ok {
			continue
		}
		seen[roleAttribute] = struct{}{}
		roleAttributes = append(roleAttributes, roleAttribute)
	}
	sort.Strings(roleAttributes)
	return roleAttributes, nil
}

func (s *Server) listUserGroups(ctx context.Context, userID uuid.UUID) ([]*groupsv1.Group, error) {
	organizationIDs, err := s.listUserOrganizationIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Named as the user whose groups these are. Groups lets a caller read its
	// own memberships outright and asks for organization membership otherwise,
	// so this is both what the request means and the only identity the caller
	// always has: reconciliation runs on a timer with no request behind it.
	ctx = metadata.AppendToOutgoingContext(ctx, identityMetadataKey, userID.String())

	groups := []*groupsv1.Group{}
	for _, organizationID := range organizationIDs {
		pageToken := ""
		for {
			response, err := s.groupsClient.ListMemberGroups(ctx, &groupsv1.ListMemberGroupsRequest{
				MemberType:     groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_USER,
				MemberId:       userID.String(),
				OrganizationId: organizationID.String(),
				PageSize:       store.MaxListPageSize,
				PageToken:      pageToken,
			})
			if err != nil {
				return nil, fmt.Errorf("list member groups: %w", err)
			}
			groups = append(groups, response.GetGroups()...)
			pageToken = response.GetNextPageToken()
			if pageToken == "" {
				break
			}
		}
	}
	return groups, nil
}

func (s *Server) listUserOrganizationIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	response, err := s.authorizationClient.ListObjects(ctx, &authorizationv1.ListObjectsRequest{
		Type:     organizationType,
		Relation: organizationMemberRelation,
		User:     identityObjectPrefix + userID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list organization memberships: %w", err)
	}
	organizationIDs := make([]uuid.UUID, 0, len(response.GetObjects()))
	for _, object := range response.GetObjects() {
		organizationIDValue, ok := strings.CutPrefix(object, organizationObjectPrefix)
		if !ok {
			return nil, fmt.Errorf("authorization returned invalid organization object %q", object)
		}
		organizationID, err := uuid.Parse(organizationIDValue)
		if err != nil {
			return nil, fmt.Errorf("parse organization object %q: %w", object, err)
		}
		organizationIDs = append(organizationIDs, organizationID)
	}
	return organizationIDs, nil
}

func (s *Server) HandleGroupMembershipEvent(ctx context.Context, subject string, data []byte) error {
	switch subject {
	case groupMembershipAddedSubject:
		event := &groupsv1.GroupMembershipAddedEvent{}
		if err := proto.Unmarshal(data, event); err != nil {
			return fmt.Errorf("unmarshal group membership added event: %w", err)
		}
		return s.handleUserMembershipChange(ctx, event.GetMemberType(), event.GetMemberId(), event.GetGroupId())
	case groupMembershipRemovedSubject:
		event := &groupsv1.GroupMembershipRemovedEvent{}
		if err := proto.Unmarshal(data, event); err != nil {
			return fmt.Errorf("unmarshal group membership removed event: %w", err)
		}
		return s.handleUserMembershipChange(ctx, event.GetMemberType(), event.GetMemberId(), event.GetGroupId())
	default:
		return nil
	}
}

func (s *Server) handleUserMembershipChange(ctx context.Context, memberType groupsv1.GroupMemberType, memberID string, groupID string) error {
	if memberType != groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_USER {
		return nil
	}
	userID, err := uuid.Parse(memberID)
	if err != nil {
		return fmt.Errorf("parse group membership member id: %w", err)
	}
	candidateRemoveAttributes := []string{}
	if groupID != "" {
		candidateRemoveAttributes = append(candidateRemoveAttributes, groupRoleAttribute(groupID))
	}
	return s.syncUserDeviceGroupRoles(ctx, userID, candidateRemoveAttributes)
}

func (s *Server) ReconcileAllUserDeviceGroupRoles(ctx context.Context) error {
	pageToken := (*store.PageCursor)(nil)
	for {
		result, err := s.store.ListUsers(ctx, store.MaxListPageSize, pageToken)
		if err != nil {
			return fmt.Errorf("list users: %w", err)
		}
		for _, user := range result.Users {
			if err := s.syncUserDeviceGroupRoles(ctx, user.Meta.ID, nil); err != nil {
				return err
			}
		}
		if result.NextCursor == nil {
			return nil
		}
		pageToken = result.NextCursor
	}
}

func (s *Server) syncUserDeviceGroupRoles(ctx context.Context, userID uuid.UUID, candidateRemoveAttributes []string) error {
	desiredAttributes, err := s.userGroupRoleAttributes(ctx, userID)
	if err != nil {
		return err
	}
	removeAttributes := staleCandidateAttributes(desiredAttributes, candidateRemoveAttributes)
	devices, err := s.listAllUserDevices(ctx, userID)
	if err != nil {
		return err
	}
	for _, device := range devices {
		_, err := s.zitiManagementClient.PatchIdentityRoleAttributes(ctx, &zitimanagementv1.PatchIdentityRoleAttributesRequest{
			ZitiIdentityId: device.OpenZitiIdentityID,
			Add:            desiredAttributes,
			Remove:         removeAttributes,
		})
		if err != nil {
			return fmt.Errorf("patch device role attributes: %w", err)
		}
	}
	return nil
}

func (s *Server) listAllUserDevices(ctx context.Context, userID uuid.UUID) ([]store.Device, error) {
	devices := []store.Device{}
	pageCursor := (*store.PageCursor)(nil)
	for {
		result, err := s.store.ListDevices(ctx, userID, store.MaxListPageSize, pageCursor)
		if err != nil {
			return nil, fmt.Errorf("list devices: %w", err)
		}
		devices = append(devices, result.Devices...)
		if result.NextCursor == nil {
			return devices, nil
		}
		pageCursor = result.NextCursor
	}
}

func staleCandidateAttributes(desiredAttributes []string, candidateAttributes []string) []string {
	desired := make(map[string]struct{}, len(desiredAttributes))
	for _, attr := range desiredAttributes {
		desired[attr] = struct{}{}
	}
	remove := []string{}
	seen := map[string]struct{}{}
	for _, attr := range candidateAttributes {
		if attr == "" {
			continue
		}
		if _, ok := desired[attr]; ok {
			continue
		}
		if _, ok := seen[attr]; ok {
			continue
		}
		seen[attr] = struct{}{}
		remove = append(remove, attr)
	}
	sort.Strings(remove)
	return remove
}

func groupRoleAttribute(groupID string) string {
	return groupRoleAttributePrefix + groupID
}
