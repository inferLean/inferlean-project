//go:build !windows

package analyzer

import "syscall"

func probeDisk() (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0, 0, err
	}
	blockSize := uint64(stat.Bsize)
	total := stat.Blocks * blockSize
	available := stat.Bavail * blockSize
	return total, available, nil
}
