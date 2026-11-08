package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func signalWork(wake chan struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (s *whatsAppService) wakeWorkers(key string) {
	if key == "push" || key == "" {
		signalWork(s.pushWake)
	}
	if key == "push" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != "" {
		if c := s.accounts[strings.ToLower(key)]; c != nil {
			signalWork(c.deliveries)
		}
		return
	}
	for _, c := range s.accounts {
		signalWork(c.deliveries)
	}
}

// Notifications are hints; reconnect and periodic sweeps recover durable queued work.
func (s *whatsAppService) listenForWork(config *pgx.ConnConfig) {
	defer s.wg.Done()
	delay := time.Second
	for s.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		conn, err := pgx.ConnectConfig(ctx, config.Copy())
		if err == nil {
			_, err = conn.Exec(ctx, "LISTEN ipo_worker_wake")
		}
		cancel()
		if err == nil {
			delay = time.Second
			s.wakeWorkers("")
			for {
				notification, waitErr := conn.WaitForNotification(s.ctx)
				if waitErr != nil {
					break
				}
				s.wakeWorkers(notification.Payload)
			}
		}
		if conn != nil {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
			_ = conn.Close(closeCtx)
			closeCancel()
		}
		if s.ctx.Err() != nil {
			return
		}
		log.Print("Worker notification connection unavailable; reconnecting")
		retry := time.NewTimer(delay)
		select {
		case <-s.ctx.Done():
			retry.Stop()
			return
		case <-retry.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}
