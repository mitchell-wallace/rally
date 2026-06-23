package keyboard

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/muesli/cancelreader"
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

// ConfirmWindow is the default span within which the second press of a
// double-press shortcut must arrive to fire. It is exported so UI consumers can
// size the "press again" hint's time-to-live to match.
const ConfirmWindow = 4 * time.Second

// control bytes for shortcuts.
const (
	ctrlC = 0x03
	ctrlS = 0x13
	ctrlP = 0x10
	ctrlX = 0x18
)

// Press is a decoded shortcut event delivered to consumers. The first press of
// a double-press shortcut arrives with Confirmed=false (it only arms the
// action); the second press within ConfirmWindow arrives with Confirmed=true
// and is the one consumers act on.
type Press struct {
	Action    Action
	Confirmed bool
}

// ArmMessage returns the "press X again to <verb>" hint shown after the first
// press of a double-press shortcut, so the operator always sees that the press
// registered and what a second press will do.
func ArmMessage(a Action) string {
	switch a {
	case ActionQuit:
		return "press Ctrl+C again to quit now"
	case ActionSkip:
		return "press Ctrl+S again to skip"
	case ActionPause:
		return "press Ctrl+P again to pause"
	case ActionStop:
		return "press Ctrl+X again to graceful-stop"
	default:
		return ""
	}
}

// ActMessage returns the present-progressive echo shown when a double-press
// shortcut fires, so the operator sees exactly which action is being taken.
func ActMessage(a Action) string {
	switch a {
	case ActionQuit:
		return "quitting…"
	case ActionSkip:
		return "skipping…"
	case ActionPause:
		return "pausing…"
	case ActionStop:
		return "stopping…"
	default:
		return ""
	}
}

// Keyboard captures raw terminal input and converts double-press shortcuts into
// Press events.
type Keyboard struct {
	in            io.Reader
	out           io.Writer
	state         *term.State
	confirmWindow time.Duration
	closeOnStop   bool

	mu      sync.Mutex
	cr      cancelreader.CancelReader
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	pending Action
	firstAt time.Time
}

// NewKeyboard creates a new Keyboard that reads from in. out is retained for
// API compatibility; feedback is now surfaced by consumers (the live status
// line and countdown) rather than written here, so the keyboard never races the
// monitor for the terminal.
func NewKeyboard(in io.Reader, out io.Writer) *Keyboard {
	return &Keyboard{
		in:            in,
		out:           out,
		confirmWindow: ConfirmWindow,
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
// It returns a channel that receives Press events: an arming first press
// (Confirmed=false) and a confirming second press (Confirmed=true).
func (k *Keyboard) Start(ctx context.Context) <-chan Press {
	ch := make(chan Press, 4)
	ctx, cancel := context.WithCancel(ctx)
	k.cancel = cancel

	// Wrap the input in a cancelable reader so Stop can unblock a pending Read
	// instead of leaking a goroutine parked on stdin.Read. Those leaked readers
	// used to accumulate across attempts/waits and randomly swallow the next
	// keystroke, which is what made shortcuts fire only "sometimes". For a real
	// terminal this uses epoll/kqueue/select; for a non-file reader (tests) it
	// falls back to a flag-based reader unblocked via SetCloseOnStop.
	if cr, err := cancelreader.NewReader(k.in); err == nil {
		k.cr = cr
	}

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
	// Unblock a Read parked on the terminal so readLoop exits promptly without
	// leaving a goroutine behind to steal a future keystroke.
	if k.cr != nil {
		k.cr.Cancel()
	}
	if k.closeOnStop {
		if c, ok := k.in.(io.Closer); ok {
			_ = c.Close()
		}
	}
	k.wg.Wait()
	if k.cr != nil {
		_ = k.cr.Close()
		k.cr = nil
	}
	return k.RestoreMode()
}

// SetCloseOnStop configures whether Stop should close the input reader.
func (k *Keyboard) SetCloseOnStop(v bool) {
	k.closeOnStop = v
}

func (k *Keyboard) readLoop(ctx context.Context, byteCh chan<- byte) {
	defer k.wg.Done()
	var reader io.Reader = k.in
	if k.cr != nil {
		reader = k.cr
	}
	buf := make([]byte, 1)
	for {
		n, err := reader.Read(buf)
		if err != nil {
			// Canceled, closed, or EOF: the keyboard is shutting down.
			return
		}
		if n > 0 {
			select {
			case byteCh <- buf[0]:
			case <-ctx.Done():
				return
			}
		}
		// Backstop: bail out if the context was cancelled between reads.
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (k *Keyboard) processLoop(ctx context.Context, byteCh <-chan byte, ch chan<- Press) {
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

			// The first press of a shortcut arms it and emits an unconfirmed
			// Press so the UI can show a "press X again" hint; the second press
			// of the same shortcut within confirmWindow emits a confirmed Press
			// that consumers act on.
			k.mu.Lock()
			now := time.Now()
			confirmed := k.pending == action && now.Sub(k.firstAt) <= k.confirmWindow
			if confirmed {
				k.pending = ActionNone
			} else {
				k.pending = action
				k.firstAt = now
			}
			k.mu.Unlock()

			select {
			case ch <- Press{Action: action, Confirmed: confirmed}:
			case <-ctx.Done():
				return
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
