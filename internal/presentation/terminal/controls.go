package terminal

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/muesli/cancelreader"

	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

// Controls adapts terminal keyboard input to the runner's runtime-event
// control contract.
type Controls struct {
	in  io.Reader
	out io.Writer

	mu     sync.Mutex
	kb     *keyboard.Keyboard
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ runtimeevent.ControlSource = (*Controls)(nil)

// NewControls returns a terminal control source backed by keyboard input.
func NewControls(in io.Reader, out io.Writer) *Controls {
	if in == nil {
		in = strings.NewReader("")
	}
	if out == nil {
		out = io.Discard
	}
	return &Controls{in: in, out: out}
}

// Start begins a fresh keyboard session with its own raw-mode lifecycle.
func (c *Controls) Start(ctx context.Context) (<-chan runtimeevent.Press, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kb != nil {
		return nil, errors.New("terminal controls already started")
	}

	kb := keyboard.NewKeyboard(c.in, c.out)
	if err := kb.SetRawMode(); err != nil {
		return nil, err
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	keyboardCh := kb.Start(sessionCtx)
	out := make(chan runtimeevent.Press, 4)

	c.kb = kb
	c.cancel = cancel
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer close(out)
		for {
			select {
			case <-sessionCtx.Done():
				return
			case press, ok := <-keyboardCh:
				if !ok {
					return
				}
				select {
				case out <- runtimeevent.Press{
					Action:    operatorAction(press.Action),
					Confirmed: press.Confirmed,
				}:
				case <-sessionCtx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

// Stop ends the current keyboard session, restoring terminal state and
// releasing any blocked reader.
func (c *Controls) Stop() {
	c.mu.Lock()
	kb := c.kb
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if kb != nil {
		_ = kb.Stop()
	}
	c.wg.Wait()

	c.mu.Lock()
	if c.kb == kb {
		c.kb = nil
		c.cancel = nil
	}
	c.mu.Unlock()
}

// WaitResume mirrors the current pause path: leave raw mode, then block until
// the operator presses Enter.
func (c *Controls) WaitResume(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.Stop()
	if err := ctx.Err(); err != nil {
		return err
	}

	reader := c.in
	var cr cancelreader.CancelReader
	if cancelable, err := cancelreader.NewReader(c.in); err == nil {
		cr = cancelable
		reader = cr
		defer cr.Close()
	}

	done := make(chan struct{})
	go func() {
		_, _ = bufio.NewReader(reader).ReadString('\n')
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if cr != nil {
			if cr.Cancel() {
				<-done
			}
		}
		return ctx.Err()
	}
}

func operatorAction(action keyboard.Action) runtimeevent.OperatorAction {
	switch action {
	case keyboard.ActionQuit:
		return runtimeevent.OperatorActionQuit
	case keyboard.ActionSkip:
		return runtimeevent.OperatorActionSkip
	case keyboard.ActionPause:
		return runtimeevent.OperatorActionPause
	case keyboard.ActionStop:
		return runtimeevent.OperatorActionStop
	default:
		return runtimeevent.OperatorActionNone
	}
}
