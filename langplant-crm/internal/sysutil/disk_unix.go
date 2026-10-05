//go:build !windows

// Package sysutil holds small OS helpers shared by the server and the node.
package sysutil

import "syscall"

// DiskUsage returns free (available to unprivileged users) and total bytes of
// the filesystem containing path.
func DiskUsage(path string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), nil
}
