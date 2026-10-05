//go:build windows

package sysutil

import "errors"

// DiskUsage is not implemented on Windows; run the binaries in Docker.
func DiskUsage(path string) (free, total int64, err error) {
	return -1, -1, errors.New("disk usage is not supported on windows")
}
