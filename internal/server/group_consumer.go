package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

const groupMembershipSubject = "agyn.groups.membership.>"

func ConnectNATS(url string) (*nats.Conn, error) {
	conn, err := nats.Connect(url, nats.Timeout(10*time.Second))
	if err != nil {
		return nil, fmt.Errorf("connect to nats: %w", err)
	}
	return conn, nil
}

func (s *Server) StartGroupMembershipConsumer(conn *nats.Conn, durable string) (*nats.Subscription, error) {
	js, err := conn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("create jetstream context: %w", err)
	}
	subscription, err := js.Subscribe(groupMembershipSubject, func(msg *nats.Msg) {
		if err := s.HandleGroupMembershipEvent(context.Background(), msg.Subject, msg.Data); err != nil {
			log.Printf("group membership event failed: %v", err)
			if nakErr := msg.Nak(); nakErr != nil {
				log.Printf("group membership event nak failed: %v", nakErr)
			}
			return
		}
		if err := msg.Ack(); err != nil {
			log.Printf("group membership event ack failed: %v", err)
		}
	}, nats.Durable(durable), nats.ManualAck(), nats.AckExplicit(), nats.DeliverAll())
	if err != nil {
		return nil, fmt.Errorf("subscribe to group membership events: %w", err)
	}
	return subscription, nil
}

func (s *Server) StartGroupRoleReconciliation(ctx context.Context, interval time.Duration) {
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
				if err := s.ReconcileAllUserDeviceGroupRoles(ctx); err != nil {
					log.Printf("group role reconciliation failed: %v", err)
				}
			}
		}
	}()
}
