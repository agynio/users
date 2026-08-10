package server

import (
	"context"
	"fmt"
	"log"
	"time"

	zitimanagementv1 "github.com/agynio/users/.gen/go/agynio/api/ziti_management/v1"
	"github.com/agynio/users/internal/store"
)

// Devices are written pending and only OpenZiti knows when the enrollment token
// is redeemed, so without polling they stay pending forever.
func (s *Server) StartDeviceLivenessPolling(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.PollDeviceLiveness(ctx); err != nil {
					log.Printf("device liveness polling failed: %v", err)
				}
			}
		}
	}()
}

func (s *Server) PollDeviceLiveness(ctx context.Context) error {
	pageToken := (*store.PageCursor)(nil)
	for {
		result, err := s.store.ListUsers(ctx, store.MaxListPageSize, pageToken)
		if err != nil {
			return fmt.Errorf("list users: %w", err)
		}
		for _, user := range result.Users {
			devices, err := s.listAllUserDevices(ctx, user.Meta.ID)
			if err != nil {
				log.Printf("list devices for user %s failed: %v", user.Meta.ID, err)
				continue
			}
			for _, device := range devices {
				if err := s.pollDeviceLiveness(ctx, device); err != nil {
					log.Printf("poll device liveness %s failed: %v", device.ID, err)
				}
			}
		}
		if result.NextCursor == nil {
			return nil
		}
		pageToken = result.NextCursor
	}
}

func (s *Server) pollDeviceLiveness(ctx context.Context, device store.Device) error {
	if device.OpenZitiIdentityID == "" {
		return nil
	}
	response, err := s.zitiManagementClient.GetIdentityLiveness(ctx, &zitimanagementv1.GetIdentityLivenessRequest{ZitiIdentityId: device.OpenZitiIdentityID})
	if err != nil {
		return err
	}
	status, err := deviceStatusFromZiti(response.GetEnrollmentState())
	if err != nil {
		return err
	}
	connectivity := deviceConnectivityFromZiti(response.GetHasEdgeRouterConnection())
	// An offline device that has not changed has nothing to record; an online
	// one is written every pass so last_seen_at keeps advancing.
	if status == device.Status && connectivity == device.Connectivity && connectivity == store.DeviceConnectivityOffline {
		return nil
	}
	now := time.Now().UTC()
	enrolledAt := device.EnrolledAt
	if enrolledAt == nil && status == store.DeviceStatusEnrolled {
		enrolledAt = &now
	}
	lastSeenAt := device.LastSeenAt
	if connectivity == store.DeviceConnectivityOnline {
		lastSeenAt = &now
	}
	if _, err := s.store.UpdateDeviceLiveness(ctx, store.UpdateDeviceLivenessInput{
		ID:           device.ID,
		Status:       status,
		Connectivity: connectivity,
		EnrolledAt:   enrolledAt,
		LastSeenAt:   lastSeenAt,
	}); err != nil {
		return err
	}
	return nil
}

func deviceConnectivityFromZiti(hasEdgeRouterConnection bool) store.DeviceConnectivity {
	if hasEdgeRouterConnection {
		return store.DeviceConnectivityOnline
	}
	return store.DeviceConnectivityOffline
}

func deviceStatusFromZiti(state zitimanagementv1.IdentityEnrollmentState) (store.DeviceStatus, error) {
	switch state {
	case zitimanagementv1.IdentityEnrollmentState_IDENTITY_ENROLLMENT_STATE_PENDING:
		return store.DeviceStatusPending, nil
	case zitimanagementv1.IdentityEnrollmentState_IDENTITY_ENROLLMENT_STATE_ENROLLED:
		return store.DeviceStatusEnrolled, nil
	default:
		return "", fmt.Errorf("unknown OpenZiti enrollment state %v", state)
	}
}
