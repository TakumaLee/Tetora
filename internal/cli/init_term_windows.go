//go:build windows

package cli

import "errors"

// menuSetRawMode is not supported on Windows; returning an error causes
// interactiveChoose to fall back to number-based input automatically.
func menuSetRawMode() (string, error) {
	return "", errors.New("interactive menu not supported on Windows")
}

func menuRestoreMode(_ string) {}
