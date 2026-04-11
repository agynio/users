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
)

func TestDevicesE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	conn, err := grpc.DialContext(ctx, usersAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
	})

	client := usersv1.NewUsersServiceClient(conn)

	t.Run("CreateDevice", func(t *testing.T) {
		identityID, authedCtx := createUserContext(t, ctx, client)
		resp, err := client.CreateDevice(authedCtx, &usersv1.CreateDeviceRequest{Name: "laptop"})
		require.NoError(t, err)
		require.NotNil(t, resp.Device)
		require.NotNil(t, resp.Device.Meta)
		require.NotEmpty(t, resp.Device.Meta.Id)
		require.NotNil(t, resp.Device.Meta.CreatedAt)
		require.Equal(t, identityID, resp.Device.UserIdentityId)
		require.Equal(t, "laptop", resp.Device.Name)
		require.NotEmpty(t, resp.Device.OpenzitiIdentityId)
		require.Equal(t, usersv1.DeviceStatus_DEVICE_STATUS_PENDING, resp.Device.Status)
		require.NotEmpty(t, resp.EnrollmentJwt)
	})

	t.Run("CreateDeviceValidation", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		_, err := client.CreateDevice(authedCtx, &usersv1.CreateDeviceRequest{})
		requireStatusCode(t, err, codes.InvalidArgument)
	})

	t.Run("ListDevices", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		firstResp := createDevice(t, authedCtx, client, "device-"+uuid.NewString())
		secondResp := createDevice(t, authedCtx, client, "device-"+uuid.NewString())

		listResp, err := client.ListDevices(authedCtx, &usersv1.ListDevicesRequest{})
		require.NoError(t, err)
		require.Len(t, listResp.Devices, 2)
		require.True(t, hasDeviceID(listResp.Devices, firstResp.Device.Meta.Id))
		require.True(t, hasDeviceID(listResp.Devices, secondResp.Device.Meta.Id))
		require.Empty(t, listResp.NextPageToken)
	})

	t.Run("ListDevicesEmpty", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		listResp, err := client.ListDevices(authedCtx, &usersv1.ListDevicesRequest{})
		require.NoError(t, err)
		require.Empty(t, listResp.Devices)
		require.Empty(t, listResp.NextPageToken)
	})

	t.Run("DeleteDevice", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		createResp := createDevice(t, authedCtx, client, "remove")

		_, err := client.DeleteDevice(authedCtx, &usersv1.DeleteDeviceRequest{Id: createResp.Device.Meta.Id})
		require.NoError(t, err)

		listResp, err := client.ListDevices(authedCtx, &usersv1.ListDevicesRequest{})
		require.NoError(t, err)
		require.Empty(t, listResp.Devices)
	})

	t.Run("DeleteDeviceNotFound", func(t *testing.T) {
		_, authedCtx := createUserContext(t, ctx, client)
		_, err := client.DeleteDevice(authedCtx, &usersv1.DeleteDeviceRequest{Id: uuid.NewString()})
		requireStatusCode(t, err, codes.NotFound)
	})

	t.Run("DeleteDeviceCrossUser", func(t *testing.T) {
		_, firstCtx := createUserContext(t, ctx, client)
		createResp := createDevice(t, firstCtx, client, "owner")
		_, secondCtx := createUserContext(t, ctx, client)

		_, err := client.DeleteDevice(secondCtx, &usersv1.DeleteDeviceRequest{Id: createResp.Device.Meta.Id})
		requireStatusCode(t, err, codes.NotFound)
	})
}

func createDevice(
	t *testing.T,
	ctx context.Context,
	client usersv1.UsersServiceClient,
	name string,
) *usersv1.CreateDeviceResponse {
	t.Helper()
	resp, err := client.CreateDevice(ctx, &usersv1.CreateDeviceRequest{Name: name})
	require.NoError(t, err)
	return resp
}

func hasDeviceID(devices []*usersv1.Device, id string) bool {
	for _, device := range devices {
		if device.GetMeta().GetId() == id {
			return true
		}
	}
	return false
}
