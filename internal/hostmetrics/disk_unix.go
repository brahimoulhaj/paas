//go:build linux || darwin

package hostmetrics

import "golang.org/x/sys/unix"

// Stat the data directory, including its real backing filesystem when bind-mounted
// or in a Docker volume. Reserved blocks are excluded from available disk space.
func readDisk(path string) (*Disk, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return nil, err
	}
	total := s.Blocks * uint64(s.Bsize)
	free := s.Bfree * uint64(s.Bsize)
	available := s.Bavail * uint64(s.Bsize)
	used := total - min(total, free)
	d := &Disk{Memory: Memory{Total: total, Used: used, Available: available}, Path: path}
	if used+available > 0 {
		d.Percent = 100 * float64(used) / float64(used+available)
	}
	if s.Files > 0 {
		d.InodesPercent = pointer(100 * float64(s.Files-min(s.Files, s.Ffree)) / float64(s.Files))
	}
	return d, nil
}
