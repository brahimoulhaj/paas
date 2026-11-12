//go:build !linux && !darwin

package hostmetrics

import (
	"context"
	"errors"
	"os"
	"time"
)

func readSystem(_ context.Context, _ Options) reading {
	hostname, _ := os.Hostname()
	return reading{Snapshot: Snapshot{CollectedAt: time.Now().UTC(), Hostname: hostname,
		Unavailable: []string{"cpu", "memory", "swap", "disk", "network", "load"}}}
}

func readDisk(_ string) (*Disk, error) { return nil, errors.New("unsupported platform") }
