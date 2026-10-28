package phone

import (
	"context"
	"sync"
	"time"
)

// phoneGate has a usable zero value and never leaves an abandoned mutex waiter.
type phoneGate struct {
	once  sync.Once
	token chan struct{}
}

func (g *phoneGate) init() {
	g.once.Do(func() { g.token = make(chan struct{}, 1); g.token <- struct{}{} })
}
func (g *phoneGate) Lock() { _ = g.LockContext(context.Background()) }
func (g *phoneGate) LockContext(ctx context.Context) error {
	g.init()
	if err := contextError(ctx); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.token:
		if err := contextError(ctx); err != nil {
			g.Unlock()
			return err
		}
		return nil
	}
}
func (g *phoneGate) TryLock() bool {
	g.init()
	select {
	case <-g.token:
		return true
	default:
		return false
	}
}
func (g *phoneGate) Unlock() { g.token <- struct{}{} }

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
