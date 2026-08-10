package server

import (
	"context"
	"testing"

	zitimanagementv1 "github.com/agynio/users/.gen/go/agynio/api/ziti_management/v1"
	"github.com/agynio/users/internal/store"
	"github.com/google/uuid"
)

func TestPollDeviceLivenessRecordsEnrollmentAndConnectivity(t *testing.T) {
	userID := uuid.New()
	onlineID := uuid.New()
	offlineID := uuid.New()
	fakeStore := newFakeUserStore()
	fakeStore.users = []store.User{{Meta: store.EntityMeta{ID: userID}}}
	fakeStore.devices[userID] = []store.Device{
		storeDevice(onlineID, userID, "laptop", "ziti-online"),
		storeDevice(offlineID, userID, "phone", "ziti-offline"),
	}
	ziti := &fakeZitiManagementClient{
		livenessByID: map[string]zitimanagementv1.IdentityEnrollmentState{
			"ziti-online":  zitimanagementv1.IdentityEnrollmentState_IDENTITY_ENROLLMENT_STATE_ENROLLED,
			"ziti-offline": zitimanagementv1.IdentityEnrollmentState_IDENTITY_ENROLLMENT_STATE_ENROLLED,
		},
		onlineByID: map[string]bool{"ziti-online": true},
	}
	server := NewWithGroups(fakeStore, &fakeAuthorizationClient{}, &fakeIdentityClient{}, ziti, &fakeGroupsClient{})

	if err := server.PollDeviceLiveness(context.Background()); err != nil {
		t.Fatalf("PollDeviceLiveness: %v", err)
	}
	if len(fakeStore.livenessUpdates) != 2 {
		t.Fatalf("expected both devices written, got %#v", fakeStore.livenessUpdates)
	}
	online := findLivenessUpdate(t, fakeStore, onlineID)
	if online.Status != store.DeviceStatusEnrolled || online.Connectivity != store.DeviceConnectivityOnline {
		t.Fatalf("unexpected online update: %#v", online)
	}
	if online.EnrolledAt == nil || online.LastSeenAt == nil {
		t.Fatalf("expected enrolled_at and last_seen_at set: %#v", online)
	}
	offline := findLivenessUpdate(t, fakeStore, offlineID)
	if offline.Connectivity != store.DeviceConnectivityOffline {
		t.Fatalf("unexpected offline update: %#v", offline)
	}
	// Never seen online, so there is no last-seen moment to claim.
	if offline.LastSeenAt != nil {
		t.Fatalf("expected no last_seen_at for a device never seen online: %#v", offline)
	}
}

// An unchanged offline device is not worth a write; an online one is, so
// last_seen_at keeps advancing.
func TestPollDeviceLivenessSkipsUnchangedOfflineDevices(t *testing.T) {
	userID := uuid.New()
	deviceID := uuid.New()
	fakeStore := newFakeUserStore()
	fakeStore.users = []store.User{{Meta: store.EntityMeta{ID: userID}}}
	fakeStore.devices[userID] = []store.Device{storeDevice(deviceID, userID, "laptop", "ziti-offline")}
	ziti := &fakeZitiManagementClient{livenessByID: map[string]zitimanagementv1.IdentityEnrollmentState{
		"ziti-offline": zitimanagementv1.IdentityEnrollmentState_IDENTITY_ENROLLMENT_STATE_ENROLLED,
	}}
	server := NewWithGroups(fakeStore, &fakeAuthorizationClient{}, &fakeIdentityClient{}, ziti, &fakeGroupsClient{})

	if err := server.PollDeviceLiveness(context.Background()); err != nil {
		t.Fatalf("PollDeviceLiveness: %v", err)
	}
	if len(fakeStore.livenessUpdates) != 1 {
		t.Fatalf("expected the pending -> enrolled transition written, got %#v", fakeStore.livenessUpdates)
	}

	fakeStore.livenessUpdates = nil
	if err := server.PollDeviceLiveness(context.Background()); err != nil {
		t.Fatalf("PollDeviceLiveness: %v", err)
	}
	if len(fakeStore.livenessUpdates) != 0 {
		t.Fatalf("expected no churn once converged, got %#v", fakeStore.livenessUpdates)
	}
}

func TestPollDeviceLivenessKeepsGoingWhenOneDeviceFails(t *testing.T) {
	userID := uuid.New()
	fakeStore := newFakeUserStore()
	fakeStore.users = []store.User{{Meta: store.EntityMeta{ID: userID}}}
	device := storeDevice(uuid.New(), userID, "laptop", "ziti-enrolled")
	unprovisioned := storeDevice(uuid.New(), userID, "phone", "")
	fakeStore.devices[userID] = []store.Device{unprovisioned, device}
	ziti := &fakeZitiManagementClient{livenessByID: map[string]zitimanagementv1.IdentityEnrollmentState{
		"ziti-enrolled": zitimanagementv1.IdentityEnrollmentState_IDENTITY_ENROLLMENT_STATE_ENROLLED,
	}}
	server := NewWithGroups(fakeStore, &fakeAuthorizationClient{}, &fakeIdentityClient{}, ziti, &fakeGroupsClient{})

	if err := server.PollDeviceLiveness(context.Background()); err != nil {
		t.Fatalf("PollDeviceLiveness: %v", err)
	}
	if len(fakeStore.livenessUpdates) != 1 || fakeStore.livenessUpdates[0].ID != device.ID {
		t.Fatalf("expected the enrolled device still promoted, got %#v", fakeStore.livenessUpdates)
	}
}

func findLivenessUpdate(t *testing.T, fakeStore *fakeUserStore, deviceID uuid.UUID) store.UpdateDeviceLivenessInput {
	t.Helper()
	for _, update := range fakeStore.livenessUpdates {
		if update.ID == deviceID {
			return update
		}
	}
	t.Fatalf("no liveness update for device %s", deviceID)
	return store.UpdateDeviceLivenessInput{}
}
