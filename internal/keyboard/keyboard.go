package keyboard

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"golang.org/x/term"
)

// Action represents a keyboard shortcut action.
type Action int

const (
	ActionNone Action = iota
	ActionQuit
	ActionSkip
	ActionPause
	ActionStop
)

// control bytes for shortcuts.
const (
	ctrlC = 0x03
	ctrlS = 0x13
	ctrlP = 0x10
	ctrlX = 0x18
)

// Keyboard captures raw terminal input and converts double-press shortcuts into Actions.
type Keyboard struct {
	in            io.Reader
	out           io.Writer
	state         *term.State
	confirmWindow time.Duration
	closeOnStop   bool

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	pending Action
	firstAt time.Time
}

// NewKeyboard creates a new Keyboard that reads from in and writes confirmation messages to out.
func NewKeyboard(in io.Reader, out io.Writer) *Keyboard {
	return &Keyboard{
		in:            in,
		out:           out,
		confirmWindow: 4 * time.Second,
	}
}

// SetRawMode puts the terminal into raw mode.
func (k *Keyboard) SetRawMode() error {
	if !term.IsTerminal(int(stdinFd(k.in))) {
		return nil
	}
	oldState, err := term.MakeRaw(int(stdinFd(k.in)))
	if err != nil {
		return err
	}
	k.state = oldState
	return nil
}

// RestoreMode restores the terminal to its previous state.
func (k *Keyboard) RestoreMode() error {
	if k.state == nil {
		return nil
	}
	return term.Restore(int(stdinFd(k.in)), k.state)
}

// Start begins listening for keyboard shortcuts in a goroutine.
// It returns a channel that receives Actions when a double-press is confirmed.
func (k *Keyboard) Start(ctx context.Context) <-chan Action {
	ch := make(chan Action, 1)
	ctx, cancel := context.WithCancel(ctx)
	k.cancel = cancel

	byteCh := make(chan byte, 8)
	k.wg.Add(2)
	go k.readLoop(ctx, byteCh)
	go k.processLoop(ctx, byteCh, ch)
	return ch
}

// Stop cancels the listener and restores the terminal.
func (k *Keyboard) Stop() error {
	if k.cancel != nil {
		k.cancel()
	}
	if k.closeOnStop {
		if c, ok := k.in.(io.Closer); ok {
			_ = c.Close()
		}
	}
	k.wg.Wait()
	return k.RestoreMode()
}

// SetCloseOnStop configures whether Stop should close the input reader.
func (k *Keyboard) SetCloseOnStop(v bool) {
	k.closeOnStop = v
}

func (k *Keyboard) readLoop(ctx context.Context, byteCh chan<- byte) {
	defer k.wg.Done()
	buf := make([]byte, 1)
	for {
		// Read in a goroutine so context cancellation can interrupt a blocking stdin.
		type readResult struct {
			b   byte
			err error
		}
		ch := make(chan readResult, 1)
		go func() {
			n, err := k.in.Read(buf)
			if err != nil {
				ch <- readResult{err: err}
				return
			}
			if n > 0 {
				ch <- readResult{b: buf[0]}
			}
		}()
		select {
		case <-ctx.Done():
			return
		case r := <-ch:
			if r.err != nil {
				return
			}
			select {
			case byteCh <- r.b:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (k *Keyboard) processLoop(ctx context.Context, byteCh <-chan byte, ch chan<- Action) {
	defer k.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-byteCh:
			action := actionForByte(b)
			if action == ActionNone {
				continue
			}

			// Double-press is silent: the first press only arms the action,
			// the second press within confirmWindow fires it. No on-screen
			// "press X again" hint — the shortcut legend printed once at the
			// top of each try already documents the keys.
			k.mu.Lock()
			now := time.Now()
			if k.pending == action && now.Sub(k.firstAt) <= k.confirmWindow {
				k.pending = ActionNone
				k.mu.Unlock()
				select {
				case ch <- action:
				case <-ctx.Done():
					return
				}
			} else {
				k.pending = action
				k.firstAt = now
				k.mu.Unlock()
			}
		}
	}
}

func actionForByte(b byte) Action {
	switch b {
	case ctrlC:
		return ActionQuit
	case ctrlS:
		return ActionSkip
	case ctrlP:
		return ActionPause
	case ctrlX:
		return ActionStop
	default:
		return ActionNone
	}
}

// stdinFd attempts to extract a file descriptor from an io.Reader.
// Falls back to 0 (stdin) when the reader is not an *os.File.
func stdinFd(r io.Reader) int {
	type fdHolder interface {
		Fd() uintptr
	}
	if f, ok := r.(fdHolder); ok {
		return int(f.Fd())
	}
	return 0
}

// SetConfirmWindow sets the double-press confirmation window (for tests).
func (k *Keyboard) SetConfirmWindow(d time.Duration) {
	k.confirmWindow = d
}

// SimulateKeyPress writes a control byte to the keyboard's input (test helper).
func (k *Keyboard) SimulateKeyPress(b byte) {
	if r, ok := k.in.(*bytes.Reader); ok {
		// If it's a bytes.Reader we can't write to it after creation.
		// This helper is only useful when in is an io.PipeReader or similar.
		_ = r
	}
}
