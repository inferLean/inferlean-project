//go:build windows

package analyzer

import "errors"

func probeDisk() (uint64, uint64, error) {
	return 0, 0, errors.New("disk probing not implemented on windows")
}
