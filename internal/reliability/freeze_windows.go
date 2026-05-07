//go:build windows

package reliability

import "fmt"

func sendProcessGroupSignal(pgid int, sig processSignal) error {
	_ = pgid
	_ = sig
	return fmt.Errorf("process group signaling is not supported on windows")
}

func processGroupRunning(pgid int) (bool, error) {
	_ = pgid
	return false, nil
}
