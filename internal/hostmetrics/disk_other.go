//go:build !linux

package hostmetrics

import "errors"

// Disk usage is only implemented for Linux — the deployment target. On other
// platforms (local development) the dashboard simply shows no disk data.
func diskUsage(string) (uint64, uint64, error) {
	return 0, 0, errors.New("disk usage not supported on this platform")
}
