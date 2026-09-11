package booking

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Expirer gives seats back that nobody confirmed. It exists because a hold
// that is never confirmed and never cancelled would otherwise keep a seat out
// of sale forever.
type Expirer struct {
	repo  Repository
	every time.Duration
	batch int
	log   *slog.Logger
}

func NewExpirer(repo Repository, every time.Duration, batch int, log *slog.Logger) *Expirer {
	return &Expirer{repo: repo, every: every, batch: batch, log: log}
}

func (e *Expirer) Run(ctx context.Context) {
	ticker := time.NewTicker(e.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.sweep(ctx)
		}
	}
}

// sweep keeps going while it fills a batch: one tick should not leave a
// backlog behind just because the batch was sized for a quiet minute.
func (e *Expirer) sweep(ctx context.Context) {
	for {
		released, err := e.repo.Expire(ctx, e.batch)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				e.log.Error("expiring holds", "error", err)
			}
			return
		}
		if released > 0 {
			e.log.Info("released expired holds", "count", released)
		}
		if released < e.batch {
			return
		}
	}
}
