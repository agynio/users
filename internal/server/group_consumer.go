package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

const groupMembershipSubject = "agyn.groups.membership.>"

var (
	groupMembershipRetryInitial   = time.Second
	groupMembershipRetryMax       = 30 * time.Second
	groupMembershipConnectTimeout = 10 * time.Second
)

type groupMembershipSubscription interface {
	Unsubscribe() error
}

type groupMembershipSubscriber func(context.Context) (groupMembershipSubscription, error)

func ConnectNATS(url string) (*nats.Conn, error) {
	conn, err := nats.Connect(url, nats.Timeout(groupMembershipConnectTimeout))
	if err != nil {
		return nil, fmt.Errorf("connect to nats: %w", err)
	}
	return conn, nil
}

func (s *Server) StartGroupMembershipConsumerLoop(ctx context.Context, url string, durable string) {
	s.StartGroupMembershipConsumerLoopWithSubscriber(ctx, func(context.Context) (groupMembershipSubscription, error) {
		conn, err := ConnectNATS(url)
		if err != nil {
			return nil, err
		}
		subscription, err := s.StartGroupMembershipConsumer(conn, durable)
		if err != nil {
			conn.Close()
			return nil, err
		}
		return natsGroupMembershipSubscription{conn: conn, subscription: subscription}, nil
	})
}

func (s *Server) StartGroupMembershipConsumerLoopWithSubscriber(ctx context.Context, subscribe groupMembershipSubscriber) {
	go func() {
		retryDelay := groupMembershipRetryInitial
		for {
			if ctx.Err() != nil {
				return
			}
			subscription, err := subscribe(ctx)
			if err == nil {
				log.Printf("group membership consumer started")
				<-ctx.Done()
				if err := subscription.Unsubscribe(); err != nil {
					log.Printf("group membership consumer unsubscribe failed: %v", err)
				}
				return
			}
			log.Printf("group membership consumer unavailable: %v; retrying in %s", err, retryDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			retryDelay = nextGroupMembershipRetryDelay(retryDelay)
		}
	}()
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

func nextGroupMembershipRetryDelay(delay time.Duration) time.Duration {
	next := delay * 2
	if next > groupMembershipRetryMax {
		return groupMembershipRetryMax
	}
	return next
}

type natsGroupMembershipSubscription struct {
	conn         *nats.Conn
	subscription *nats.Subscription
}

func (s natsGroupMembershipSubscription) Unsubscribe() error {
	err := s.subscription.Unsubscribe()
	s.conn.Close()
	return err
}
