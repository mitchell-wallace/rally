package tuitabs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

type controls struct {
	mu sync.Mutex

	ch     chan runtimeevent.Press
	active bool

	armedAction runtimeevent.OperatorAction
	armedAt     time.Time

	resumeCh chan struct{}
	now      func() time.Time
}

var _ runtimeevent.ControlSource = (*controls)(nil)

func newControls() *controls {
	return &controls{now: time.Now}
}

func (c *controls) Start(ctx context.Context) (<-chan runtimeevent.Press, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active {
		return nil, errors.New("tuitabs controls already started")
	}
	ch := make(chan runtimeevent.Press, 8)
	c.ch = ch
	c.active = true
	c.armedAction = runtimeevent.OperatorActionNone
	c.armedAt = time.Time{}
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		c.stopSessionLocked(ch)
		c.mu.Unlock()
	}()
	return ch, nil
}

func (c *controls) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopSessionLocked(c.ch)
}

func (c *controls) stopSessionLocked(ch chan runtimeevent.Press) {
	if !c.active || c.ch == nil || c.ch != ch {
		return
	}
	close(c.ch)
	c.ch = nil
	c.active = false
	c.armedAction = runtimeevent.OperatorActionNone
	c.armedAt = time.Time{}
}

func (c *controls) WaitResume(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	resumeCh := make(chan struct{})
	c.mu.Lock()
	c.resumeCh = resumeCh
	c.mu.Unlock()
	select {
	case <-resumeCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *controls) press(action runtimeevent.OperatorAction) (pressFeedback, bool) {
	if action == runtimeevent.OperatorActionNone {
		return pressFeedback{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active || c.ch == nil {
		return pressFeedback{}, false
	}
	now := c.now()
	confirmed := c.armedAction == action && now.Sub(c.armedAt) <= runtimeevent.ConfirmWindow
	if confirmed {
		c.armedAction = runtimeevent.OperatorActionNone
		c.armedAt = time.Time{}
	} else {
		c.armedAction = action
		c.armedAt = now
	}
	press := runtimeevent.Press{Action: action, Confirmed: confirmed}
	select {
	case c.ch <- press:
	default:
	}
	if confirmed {
		return pressFeedback{message: runtimeevent.ActMessage(action), deadline: now.Add(runtimeevent.ConfirmWindow), confirmed: true}, true
	}
	return pressFeedback{message: runtimeevent.ArmMessage(action), deadline: now.Add(runtimeevent.ConfirmWindow)}, true
}

func (c *controls) resume() {
	c.mu.Lock()
	ch := c.resumeCh
	c.resumeCh = nil
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

type pressFeedback struct {
	message   string
	deadline  time.Time
	confirmed bool
}

func actionForKey(key string) runtimeevent.OperatorAction {
	switch key {
	case "ctrl+c":
		return runtimeevent.OperatorActionQuit
	case "ctrl+s":
		return runtimeevent.OperatorActionSkip
	case "ctrl+p":
		return runtimeevent.OperatorActionPause
	case "ctrl+x":
		return runtimeevent.OperatorActionStop
	default:
		return runtimeevent.OperatorActionNone
	}
}
