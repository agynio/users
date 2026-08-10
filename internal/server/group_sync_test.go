package server

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	authorizationv1 "github.com/agynio/users/.gen/go/agynio/api/authorization/v1"
	groupsv1 "github.com/agynio/users/.gen/go/agynio/api/groups/v1"
	identityv1 "github.com/agynio/users/.gen/go/agynio/api/identity/v1"
	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
	zitimanagementv1 "github.com/agynio/users/.gen/go/agynio/api/ziti_management/v1"
	"github.com/agynio/users/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestCreateDeviceIncludesCurrentGroupAttributes(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	groupA := uuid.New().String()
	groupB := uuid.New().String()
	deviceID := uuid.New()
	ziti := &fakeZitiManagementClient{createResponse: &zitimanagementv1.CreateDeviceIdentityResponse{
		ZitiIdentityId: "ziti-device-1",
		EnrollmentJwt:  "jwt-1",
	}}
	fakeStore := newFakeUserStore()
	fakeStore.createDevice = storeDevice(deviceID, userID, "laptop", "ziti-device-1")
	server := NewWithGroups(
		fakeStore,
		&fakeAuthorizationClient{objects: []string{organizationObjectPrefix + orgID.String()}},
		&fakeIdentityClient{},
		ziti,
		&fakeGroupsClient{groupsByOrg: map[string][]*groupsv1.Group{
			orgID.String(): {{Meta: &groupsv1.EntityMeta{Id: groupB}}, {Meta: &groupsv1.EntityMeta{Id: groupA}}, {Meta: &groupsv1.EntityMeta{Id: groupA}}},
		}},
	)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-identity-id", userID.String()))
	response, err := server.CreateDevice(ctx, &usersv1.CreateDeviceRequest{Name: "laptop"})
	require.NoError(t, err)
	require.Equal(t, deviceID.String(), response.GetDevice().GetMeta().GetId())
	require.NotNil(t, ziti.createRequest)
	require.Equal(t, userID.String(), ziti.createRequest.GetUserIdentityId())
	require.Equal(t, "laptop", ziti.createRequest.GetName())
	require.ElementsMatch(t, []string{groupRoleAttribute(groupA), groupRoleAttribute(groupB)}, ziti.createRequest.GetAdditionalRoleAttributes())
}

func TestGroupMembershipEventPatchesAllCurrentDevices(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	groupID := uuid.New().String()
	fakeStore := newFakeUserStore()
	fakeStore.devices[userID] = []store.Device{
		storeDevice(uuid.New(), userID, "laptop", "ziti-device-1"),
		storeDevice(uuid.New(), userID, "desktop", "ziti-device-2"),
	}
	ziti := &fakeZitiManagementClient{}
	server := NewWithGroups(
		fakeStore,
		&fakeAuthorizationClient{objects: []string{organizationObjectPrefix + orgID.String()}},
		&fakeIdentityClient{},
		ziti,
		&fakeGroupsClient{groupsByOrg: map[string][]*groupsv1.Group{orgID.String(): {{Meta: &groupsv1.EntityMeta{Id: groupID}}}}},
	)
	payload := mustMarshal(t, &groupsv1.GroupMembershipAddedEvent{
		GroupId:    groupID,
		MemberType: groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_USER,
		MemberId:   userID.String(),
	})

	err := server.HandleGroupMembershipEvent(context.Background(), groupMembershipAddedSubject, payload)
	require.NoError(t, err)
	require.Len(t, ziti.patchRequests, 2)
	require.ElementsMatch(t, []string{"ziti-device-1", "ziti-device-2"}, []string{
		ziti.patchRequests[0].GetZitiIdentityId(),
		ziti.patchRequests[1].GetZitiIdentityId(),
	})
	for _, request := range ziti.patchRequests {
		require.Equal(t, []string{groupRoleAttribute(groupID)}, request.GetAdd())
		require.Empty(t, request.GetRemove())
	}
}

func TestGroupMembershipEventsAreDuplicateAndOutOfOrderSafe(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	groupID := uuid.New().String()
	fakeStore := newFakeUserStore()
	fakeStore.devices[userID] = []store.Device{storeDevice(uuid.New(), userID, "laptop", "ziti-device-1")}
	ziti := &fakeZitiManagementClient{}
	groups := &fakeGroupsClient{groupsByOrg: map[string][]*groupsv1.Group{orgID.String(): {}}}
	server := NewWithGroups(
		fakeStore,
		&fakeAuthorizationClient{objects: []string{organizationObjectPrefix + orgID.String()}},
		&fakeIdentityClient{},
		ziti,
		groups,
	)
	removed := mustMarshal(t, &groupsv1.GroupMembershipRemovedEvent{
		GroupId:    groupID,
		MemberType: groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_USER,
		MemberId:   userID.String(),
	})
	added := mustMarshal(t, &groupsv1.GroupMembershipAddedEvent{
		GroupId:    groupID,
		MemberType: groupsv1.GroupMemberType_GROUP_MEMBER_TYPE_USER,
		MemberId:   userID.String(),
	})

	require.NoError(t, server.HandleGroupMembershipEvent(context.Background(), groupMembershipRemovedSubject, removed))
	require.NoError(t, server.HandleGroupMembershipEvent(context.Background(), groupMembershipRemovedSubject, removed))
	require.NoError(t, server.HandleGroupMembershipEvent(context.Background(), groupMembershipAddedSubject, added))
	require.Len(t, ziti.patchRequests, 3)
	for _, request := range ziti.patchRequests {
		require.Empty(t, request.GetAdd())
		require.Equal(t, []string{groupRoleAttribute(groupID)}, request.GetRemove())
	}

	groups.groupsByOrg[orgID.String()] = []*groupsv1.Group{{Meta: &groupsv1.EntityMeta{Id: groupID}}}
	require.NoError(t, server.HandleGroupMembershipEvent(context.Background(), groupMembershipAddedSubject, added))
	lastRequest := ziti.patchRequests[len(ziti.patchRequests)-1]
	require.Equal(t, []string{groupRoleAttribute(groupID)}, lastRequest.GetAdd())
	require.Empty(t, lastRequest.GetRemove())
}

func TestReconcileAllUserDeviceGroupRolesPatchesMissingDesiredAttrs(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	groupID := uuid.New().String()
	fakeStore := newFakeUserStore()
	fakeStore.users = []store.User{{Meta: store.EntityMeta{ID: userID}}}
	fakeStore.devices[userID] = []store.Device{storeDevice(uuid.New(), userID, "laptop", "ziti-device-1")}
	ziti := &fakeZitiManagementClient{}
	server := NewWithGroups(
		fakeStore,
		&fakeAuthorizationClient{objects: []string{organizationObjectPrefix + orgID.String()}},
		&fakeIdentityClient{},
		ziti,
		&fakeGroupsClient{groupsByOrg: map[string][]*groupsv1.Group{orgID.String(): {{Meta: &groupsv1.EntityMeta{Id: groupID}}}}},
	)

	err := server.ReconcileAllUserDeviceGroupRoles(context.Background())
	require.NoError(t, err)
	require.Len(t, ziti.patchRequests, 1)
	require.Equal(t, "ziti-device-1", ziti.patchRequests[0].GetZitiIdentityId())
	require.Equal(t, []string{groupRoleAttribute(groupID)}, ziti.patchRequests[0].GetAdd())
	require.Empty(t, ziti.patchRequests[0].GetRemove())
}

func TestListUserGroupsPaginatesOrganizationsAndGroups(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	firstGroupID := uuid.New().String()
	secondGroupID := uuid.New().String()
	groups := &fakeGroupsClient{pagedGroupsByOrg: map[string][][]*groupsv1.Group{
		orgID.String(): {{{Meta: &groupsv1.EntityMeta{Id: firstGroupID}}}, {{Meta: &groupsv1.EntityMeta{Id: secondGroupID}}}},
	}}
	server := NewWithGroups(
		newFakeUserStore(),
		&fakeAuthorizationClient{objects: []string{organizationObjectPrefix + orgID.String()}},
		&fakeIdentityClient{},
		&fakeZitiManagementClient{},
		groups,
	)

	attrs, err := server.userGroupRoleAttributes(context.Background(), userID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{groupRoleAttribute(firstGroupID), groupRoleAttribute(secondGroupID)}, attrs)
	require.Len(t, groups.requests, 2)
	require.Empty(t, groups.requests[0].GetPageToken())
	require.Equal(t, "page-1", groups.requests[1].GetPageToken())
}

// Groups answers Unauthenticated without a caller, and gRPC does not carry a
// server's incoming metadata onto the calls it makes. Reconciliation runs on a
// timer with no request behind it at all, so naming the user is the only thing
// that works on both paths -- and Groups lets a caller read its own
// memberships, which is exactly what this is.
func TestUserGroupRoleAttributesNamesTheUserToGroups(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	groupID := uuid.New().String()

	groups := &fakeGroupsClient{groupsByOrg: map[string][]*groupsv1.Group{
		orgID.String(): {{Meta: &groupsv1.EntityMeta{Id: groupID}}},
	}}
	server := NewWithGroups(
		newFakeUserStore(),
		&fakeAuthorizationClient{objects: []string{organizationObjectPrefix + orgID.String()}},
		&fakeIdentityClient{},
		&fakeZitiManagementClient{},
		groups,
	)

	// Background context: no incoming request, nothing to forward.
	attrs, err := server.userGroupRoleAttributes(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, []string{groupRoleAttribute(groupID)}, attrs)
	require.Equal(t, []string{userID.String()}, groups.callers)
}

func storeDevice(id uuid.UUID, userID uuid.UUID, name string, zitiIdentityID string) store.Device {
	jwt := "jwt"
	return store.Device{
		ID:                 id,
		IdentityID:         userID,
		Name:               name,
		OpenZitiIdentityID: zitiIdentityID,
		EnrollmentJWT:      &jwt,
		Status:             store.DeviceStatusPending,
		Connectivity:       store.DeviceConnectivityOffline,
		CreatedAt:          time.Unix(1, 0),
	}
}

func mustMarshal(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	require.NoError(t, err)
	return data
}

type fakeUserStore struct {
	mu              sync.Mutex
	users           []store.User
	devices         map[uuid.UUID][]store.Device
	createDevice    store.Device
	livenessUpdates []store.UpdateDeviceLivenessInput
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{devices: map[uuid.UUID][]store.Device{}}
}

func (s *fakeUserStore) ResolveOrCreateUser(_ context.Context, input store.UserInput) (store.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.OIDCSubject == input.OIDCSubject {
			return user, false, nil
		}
		if input.Username != "" && user.Username == input.Username {
			return store.User{}, false, store.AlreadyExists("username")
		}
	}
	user := store.User{
		Meta:        store.EntityMeta{ID: uuid.New()},
		OIDCSubject: input.OIDCSubject,
		Name:        input.Name,
		Email:       input.Email,
		Nickname:    input.Nickname,
		Username:    input.Username,
		PhotoURL:    input.PhotoURL,
	}
	s.users = append(s.users, user)
	return user, true, nil
}

func (s *fakeUserStore) CreateUser(context.Context, store.UserInput) (store.User, error) {
	panic("unexpected CreateUser")
}

func (s *fakeUserStore) GetUser(context.Context, uuid.UUID) (store.User, error) {
	panic("unexpected GetUser")
}

func (s *fakeUserStore) GetUserByOIDCSubject(context.Context, string) (store.User, error) {
	panic("unexpected GetUserByOIDCSubject")
}

func (s *fakeUserStore) BatchGetUsers(context.Context, []uuid.UUID) ([]store.User, error) {
	panic("unexpected BatchGetUsers")
}

func (s *fakeUserStore) UpdateUser(context.Context, uuid.UUID, store.UserUpdate) (store.User, error) {
	panic("unexpected UpdateUser")
}

func (s *fakeUserStore) DeleteUser(_ context.Context, identityID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, user := range s.users {
		if user.Meta.ID == identityID {
			s.users = append(s.users[:index], s.users[index+1:]...)
			return nil
		}
	}
	return store.NotFound("user")
}

func (s *fakeUserStore) ListUsers(_ context.Context, pageSize int32, cursor *store.PageCursor) (store.UserListResult, error) {
	start := 0
	if cursor != nil {
		for index, user := range s.users {
			if user.Meta.ID == cursor.AfterID {
				start = index + 1
				break
			}
		}
	}
	end := start + int(pageSize)
	if end > len(s.users) {
		end = len(s.users)
	}
	result := store.UserListResult{Users: append([]store.User{}, s.users[start:end]...)}
	if end < len(s.users) {
		result.NextCursor = &store.PageCursor{AfterID: s.users[end-1].Meta.ID}
	}
	return result, nil
}

func (s *fakeUserStore) SearchUsers(context.Context, string, int32) ([]store.UserDirectoryEntry, error) {
	panic("unexpected SearchUsers")
}

func (s *fakeUserStore) CreateAPIToken(context.Context, store.CreateAPITokenInput) (store.APIToken, error) {
	panic("unexpected CreateAPIToken")
}

func (s *fakeUserStore) ListAPITokens(context.Context, uuid.UUID) ([]store.APIToken, error) {
	panic("unexpected ListAPITokens")
}

func (s *fakeUserStore) RevokeAPIToken(context.Context, uuid.UUID, uuid.UUID) error {
	panic("unexpected RevokeAPIToken")
}

func (s *fakeUserStore) ResolveAPIToken(context.Context, string) (store.APIToken, error) {
	panic("unexpected ResolveAPIToken")
}

func (s *fakeUserStore) CreateDevice(_ context.Context, input store.CreateDeviceInput) (store.Device, error) {
	device := s.createDevice
	device.IdentityID = input.IdentityID
	device.Name = input.Name
	device.OpenZitiIdentityID = input.OpenZitiIdentityID
	s.devices[input.IdentityID] = append(s.devices[input.IdentityID], device)
	return device, nil
}

func (s *fakeUserStore) ListDevices(_ context.Context, identityID uuid.UUID, pageSize int32, cursor *store.PageCursor) (store.DeviceListResult, error) {
	devices := s.devices[identityID]
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID.String() < devices[j].ID.String() })
	start := 0
	if cursor != nil {
		for index, device := range devices {
			if device.ID == cursor.AfterID {
				start = index + 1
				break
			}
		}
	}
	end := start + int(pageSize)
	if end > len(devices) {
		end = len(devices)
	}
	result := store.DeviceListResult{Devices: append([]store.Device{}, devices[start:end]...)}
	if end < len(devices) {
		result.NextCursor = &store.PageCursor{AfterID: devices[end-1].ID}
	}
	return result, nil
}

func (s *fakeUserStore) DeleteDevice(context.Context, uuid.UUID, uuid.UUID) (store.Device, error) {
	panic("unexpected DeleteDevice")
}

func (s *fakeUserStore) UpdateDeviceLiveness(_ context.Context, input store.UpdateDeviceLivenessInput) (store.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.livenessUpdates = append(s.livenessUpdates, input)
	for identityID, devices := range s.devices {
		for index, device := range devices {
			if device.ID == input.ID {
				s.devices[identityID][index].Status = input.Status
				s.devices[identityID][index].Connectivity = input.Connectivity
				s.devices[identityID][index].EnrolledAt = input.EnrolledAt
				s.devices[identityID][index].LastSeenAt = input.LastSeenAt
				return s.devices[identityID][index], nil
			}
		}
	}
	return store.Device{}, fmt.Errorf("device %s not found", input.ID)
}

type fakeAuthorizationClient struct {
	authorizationv1.UnimplementedAuthorizationServiceServer
	mu       sync.Mutex
	objects  []string
	writes   []*authorizationv1.WriteRequest
	writeErr error
}

func (c *fakeAuthorizationClient) Check(context.Context, *authorizationv1.CheckRequest, ...grpc.CallOption) (*authorizationv1.CheckResponse, error) {
	return &authorizationv1.CheckResponse{Allowed: true}, nil
}

func (c *fakeAuthorizationClient) Write(_ context.Context, request *authorizationv1.WriteRequest, _ ...grpc.CallOption) (*authorizationv1.WriteResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return nil, c.writeErr
	}
	c.writes = append(c.writes, proto.Clone(request).(*authorizationv1.WriteRequest))
	return &authorizationv1.WriteResponse{}, nil
}

// clusterAdmins replays the recorded tuple writes into the set of identities
// that currently hold cluster admin.
func (c *fakeAuthorizationClient) clusterAdmins() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	admins := []string{}
	for _, request := range c.writes {
		for _, tuple := range request.GetWrites() {
			if isClusterAdminTuple(tuple) {
				admins = append(admins, tuple.GetUser())
			}
		}
		for _, tuple := range request.GetDeletes() {
			if !isClusterAdminTuple(tuple) {
				continue
			}
			for index, admin := range admins {
				if admin == tuple.GetUser() {
					admins = append(admins[:index], admins[index+1:]...)
					break
				}
			}
		}
	}
	return admins
}

func isClusterAdminTuple(tuple *authorizationv1.TupleKey) bool {
	return tuple.GetRelation() == adminRelation && tuple.GetObject() == clusterObject
}

func (c *fakeAuthorizationClient) ListObjects(context.Context, *authorizationv1.ListObjectsRequest, ...grpc.CallOption) (*authorizationv1.ListObjectsResponse, error) {
	return &authorizationv1.ListObjectsResponse{Objects: c.objects}, nil
}

func (c *fakeAuthorizationClient) BatchCheck(context.Context, *authorizationv1.BatchCheckRequest, ...grpc.CallOption) (*authorizationv1.BatchCheckResponse, error) {
	panic("unexpected BatchCheck")
}

func (c *fakeAuthorizationClient) Read(context.Context, *authorizationv1.ReadRequest, ...grpc.CallOption) (*authorizationv1.ReadResponse, error) {
	panic("unexpected Read")
}

func (c *fakeAuthorizationClient) ListUsers(context.Context, *authorizationv1.ListUsersRequest, ...grpc.CallOption) (*authorizationv1.ListUsersResponse, error) {
	panic("unexpected ListUsers")
}

type fakeIdentityClient struct{}

func (c *fakeIdentityClient) RegisterIdentity(context.Context, *identityv1.RegisterIdentityRequest, ...grpc.CallOption) (*identityv1.RegisterIdentityResponse, error) {
	return &identityv1.RegisterIdentityResponse{}, nil
}

func (c *fakeIdentityClient) GetIdentityType(context.Context, *identityv1.GetIdentityTypeRequest, ...grpc.CallOption) (*identityv1.GetIdentityTypeResponse, error) {
	panic("unexpected GetIdentityType")
}

func (c *fakeIdentityClient) BatchGetIdentityTypes(context.Context, *identityv1.BatchGetIdentityTypesRequest, ...grpc.CallOption) (*identityv1.BatchGetIdentityTypesResponse, error) {
	panic("unexpected BatchGetIdentityTypes")
}

func (c *fakeIdentityClient) SetNickname(context.Context, *identityv1.SetNicknameRequest, ...grpc.CallOption) (*identityv1.SetNicknameResponse, error) {
	panic("unexpected SetNickname")
}

func (c *fakeIdentityClient) RemoveNickname(context.Context, *identityv1.RemoveNicknameRequest, ...grpc.CallOption) (*identityv1.RemoveNicknameResponse, error) {
	panic("unexpected RemoveNickname")
}

func (c *fakeIdentityClient) ResolveNickname(context.Context, *identityv1.ResolveNicknameRequest, ...grpc.CallOption) (*identityv1.ResolveNicknameResponse, error) {
	panic("unexpected ResolveNickname")
}

func (c *fakeIdentityClient) BatchGetNicknames(context.Context, *identityv1.BatchGetNicknamesRequest, ...grpc.CallOption) (*identityv1.BatchGetNicknamesResponse, error) {
	panic("unexpected BatchGetNicknames")
}

type fakeZitiManagementClient struct {
	createResponse *zitimanagementv1.CreateDeviceIdentityResponse
	createRequest  *zitimanagementv1.CreateDeviceIdentityRequest
	patchRequests  []*zitimanagementv1.PatchIdentityRoleAttributesRequest
	livenessByID   map[string]zitimanagementv1.IdentityEnrollmentState
	onlineByID     map[string]bool
	livenessErr    error
}

func (c *fakeZitiManagementClient) GetIdentityLiveness(_ context.Context, request *zitimanagementv1.GetIdentityLivenessRequest, _ ...grpc.CallOption) (*zitimanagementv1.GetIdentityLivenessResponse, error) {
	if c.livenessErr != nil {
		return nil, c.livenessErr
	}
	state, ok := c.livenessByID[request.GetZitiIdentityId()]
	if !ok {
		state = zitimanagementv1.IdentityEnrollmentState_IDENTITY_ENROLLMENT_STATE_PENDING
	}
	return &zitimanagementv1.GetIdentityLivenessResponse{
		EnrollmentState:         state,
		HasEdgeRouterConnection: c.onlineByID[request.GetZitiIdentityId()],
	}, nil
}

func (c *fakeZitiManagementClient) CreateDeviceIdentity(_ context.Context, request *zitimanagementv1.CreateDeviceIdentityRequest, _ ...grpc.CallOption) (*zitimanagementv1.CreateDeviceIdentityResponse, error) {
	c.createRequest = proto.Clone(request).(*zitimanagementv1.CreateDeviceIdentityRequest)
	if c.createResponse == nil {
		return nil, fmt.Errorf("missing create response")
	}
	return c.createResponse, nil
}

func (c *fakeZitiManagementClient) DeleteDeviceIdentity(context.Context, *zitimanagementv1.DeleteDeviceIdentityRequest, ...grpc.CallOption) (*zitimanagementv1.DeleteDeviceIdentityResponse, error) {
	return &zitimanagementv1.DeleteDeviceIdentityResponse{}, nil
}

func (c *fakeZitiManagementClient) PatchIdentityRoleAttributes(_ context.Context, request *zitimanagementv1.PatchIdentityRoleAttributesRequest, _ ...grpc.CallOption) (*zitimanagementv1.PatchIdentityRoleAttributesResponse, error) {
	c.patchRequests = append(c.patchRequests, proto.Clone(request).(*zitimanagementv1.PatchIdentityRoleAttributesRequest))
	return &zitimanagementv1.PatchIdentityRoleAttributesResponse{}, nil
}

type fakeGroupsClient struct {
	groupsByOrg      map[string][]*groupsv1.Group
	pagedGroupsByOrg map[string][][]*groupsv1.Group
	requests         []*groupsv1.ListMemberGroupsRequest
	// Groups reads the caller off the outgoing metadata, so a fake that only
	// records the request cannot tell an authorized call from a rejected one.
	callers []string
}

func (c *fakeGroupsClient) ListMemberGroups(ctx context.Context, request *groupsv1.ListMemberGroupsRequest, _ ...grpc.CallOption) (*groupsv1.ListMemberGroupsResponse, error) {
	caller := ""
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if values := md.Get("x-identity-id"); len(values) > 0 {
			caller = values[0]
		}
	}
	c.callers = append(c.callers, caller)
	c.requests = append(c.requests, proto.Clone(request).(*groupsv1.ListMemberGroupsRequest))
	if c.pagedGroupsByOrg != nil {
		pages := c.pagedGroupsByOrg[request.GetOrganizationId()]
		pageIndex := 0
		if request.GetPageToken() != "" {
			_, err := fmt.Sscanf(request.GetPageToken(), "page-%d", &pageIndex)
			if err != nil {
				return nil, err
			}
		}
		response := &groupsv1.ListMemberGroupsResponse{Groups: append([]*groupsv1.Group{}, pages[pageIndex]...)}
		if pageIndex+1 < len(pages) {
			response.NextPageToken = fmt.Sprintf("page-%d", pageIndex+1)
		}
		return response, nil
	}
	return &groupsv1.ListMemberGroupsResponse{Groups: append([]*groupsv1.Group{}, c.groupsByOrg[request.GetOrganizationId()]...)}, nil
}

func TestGroupMembershipConsumerLoopRetriesWithoutBlocking(t *testing.T) {
	originalInitial := groupMembershipRetryInitial
	originalMax := groupMembershipRetryMax
	groupMembershipRetryInitial = time.Millisecond
	groupMembershipRetryMax = time.Millisecond
	defer func() {
		groupMembershipRetryInitial = originalInitial
		groupMembershipRetryMax = originalMax
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription := &fakeGroupMembershipSubscription{}
	attempts := make(chan int, 3)
	server := NewWithGroups(newFakeUserStore(), &fakeAuthorizationClient{}, &fakeIdentityClient{}, &fakeZitiManagementClient{}, nil)

	server.StartGroupMembershipConsumerLoopWithSubscriber(ctx, func(context.Context) (groupMembershipSubscription, error) {
		attempts <- len(attempts) + 1
		if len(attempts) < 2 {
			return nil, fmt.Errorf("nats unavailable")
		}
		return subscription, nil
	})

	require.Eventually(t, func() bool { return len(attempts) >= 2 }, time.Second, time.Millisecond)
	require.False(t, subscription.unsubscribed)
	cancel()
	require.Eventually(t, func() bool { return subscription.unsubscribed }, time.Second, time.Millisecond)
}

type fakeGroupMembershipSubscription struct {
	unsubscribed bool
}

func (s *fakeGroupMembershipSubscription) Unsubscribe() error {
	s.unsubscribed = true
	return nil
}
