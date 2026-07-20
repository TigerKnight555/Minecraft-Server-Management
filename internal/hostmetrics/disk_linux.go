//go:build linux

package hostmetrics

import "golang.org/x/sys/unix"

func diskUsage(path string) (total, used uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	total = st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	if total >= free {
		used = total - free
	}
	return total, used, nil
}
