// Package dockermetrics collects read-only Docker daemon information. Disk scans
// are shared by all clients and run at most once a minute.
package dockermetrics

import (
	"context"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

const Interval = 15 * time.Second
const diskInterval = time.Minute

type Client interface {
	Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error)
	NetworkList(context.Context, client.NetworkListOptions) (client.NetworkListResult, error)
	DiskUsage(context.Context, client.DiskUsageOptions) (client.DiskUsageResult, error)
	ClientVersion() string
}

type Engine struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	APIVersion    string `json:"api_version"`
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	Kernel        string `json:"kernel"`
	StorageDriver string `json:"storage_driver"`
	LoggingDriver string `json:"logging_driver"`
	CgroupDriver  string `json:"cgroup_driver"`
	CgroupVersion string `json:"cgroup_version"`
	RootDir       string `json:"root_dir"`
	Runtime       string `json:"runtime"`
	CPUs          int    `json:"cpus"`
	Memory        int64  `json:"memory"`
	LiveRestore   bool   `json:"live_restore"`
}

type Containers struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Paused  int `json:"paused"`
	Stopped int `json:"stopped"`
}

type Usage struct {
	Count       int64  `json:"count"`
	Active      int64  `json:"active"`
	Size        *int64 `json:"size"`
	Reclaimable *int64 `json:"reclaimable"`
}

type Storage struct {
	Containers Usage `json:"containers"`
	Images     Usage `json:"images"`
	Volumes    Usage `json:"volumes"`
	BuildCache Usage `json:"build_cache"`
}

type Snapshot struct {
	CheckedAt   time.Time   `json:"checked_at"`
	CollectedAt *time.Time  `json:"collected_at"`
	Online      bool        `json:"online"`
	Engine      *Engine     `json:"engine"`
	Containers  *Containers `json:"containers"`
	Images      *int        `json:"images"`
	Networks    *int        `json:"networks"`
	Storage     *Storage    `json:"storage"`
	StorageAt   *time.Time  `json:"storage_at"`
	Unavailable []string    `json:"unavailable"`
}

type Collector struct {
	client        Client
	mu            sync.RWMutex
	latest        Snapshot
	nextDisk      time.Time
	storageFailed bool
	now           func() time.Time
}

func New(cli Client) *Collector { return &Collector{client: cli, now: time.Now} }

func (c *Collector) Run(ctx context.Context) {
	c.collect(ctx)
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect(ctx)
		}
	}
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.latest
	s.Unavailable = append([]string{}, s.Unavailable...)
	return s
}

func (c *Collector) collect(ctx context.Context) {
	s := c.Snapshot()
	checked := c.now().UTC()
	s.CheckedAt = checked
	infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	result, err := c.client.Info(infoCtx, client.InfoOptions{})
	cancel()
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		s.Online = false
		s.Unavailable = []string{"daemon"}
		c.publish(s)
		return
	}
	s.Online = true
	s.CollectedAt = &checked
	s.Unavailable = []string{}
	i := result.Info
	s.Engine = &Engine{Name: i.Name, Version: i.ServerVersion, APIVersion: c.client.ClientVersion(), OS: i.OperatingSystem,
		Architecture: i.Architecture, Kernel: i.KernelVersion, StorageDriver: i.Driver, LoggingDriver: i.LoggingDriver,
		CgroupDriver: i.CgroupDriver, CgroupVersion: i.CgroupVersion, RootDir: i.DockerRootDir,
		Runtime: i.DefaultRuntime, CPUs: i.NCPU, Memory: i.MemTotal, LiveRestore: i.LiveRestoreEnabled}
	s.Containers = &Containers{Total: i.Containers, Running: i.ContainersRunning, Paused: i.ContainersPaused, Stopped: i.ContainersStopped}
	s.Images = &i.Images
	netCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	networks, err := c.client.NetworkList(netCtx, client.NetworkListOptions{})
	cancel()
	s.Networks = nil
	if err != nil {
		s.Unavailable = append(s.Unavailable, "networks")
	} else {
		count := len(networks.Items)
		s.Networks = &count
	}
	if !checked.Before(c.nextDisk) {
		c.nextDisk = checked.Add(diskInterval)
		diskCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		du, err := c.client.DiskUsage(diskCtx, client.DiskUsageOptions{Containers: true, Images: true, Volumes: true, BuildCache: true})
		cancel()
		if err != nil {
			c.storageFailed = true
			s.Unavailable = append(s.Unavailable, "storage")
		} else {
			c.storageFailed = false
			s.Storage = storage(du)
			at := c.now().UTC()
			s.StorageAt = &at
		}
	} else if c.storageFailed || s.StorageAt == nil || checked.Sub(*s.StorageAt) >= 2*diskInterval {
		s.Unavailable = append(s.Unavailable, "storage")
	}
	if ctx.Err() != nil {
		return
	}
	c.publish(s)
}

func (c *Collector) publish(s Snapshot) { c.mu.Lock(); c.latest = s; c.mu.Unlock() }

func known(n int64) *int64 {
	if n < 0 {
		return nil
	}
	return &n
}

func storage(d client.DiskUsageResult) *Storage {
	s := &Storage{
		Containers: Usage{d.Containers.TotalCount, d.Containers.ActiveCount, known(d.Containers.TotalSize), known(d.Containers.Reclaimable)},
		Images:     Usage{d.Images.TotalCount, d.Images.ActiveCount, known(d.Images.TotalSize), known(d.Images.Reclaimable)},
		Volumes:    Usage{d.Volumes.TotalCount, d.Volumes.ActiveCount, known(d.Volumes.TotalSize), known(d.Volumes.Reclaimable)},
		BuildCache: Usage{d.BuildCache.TotalCount, d.BuildCache.ActiveCount, known(d.BuildCache.TotalSize), known(d.BuildCache.Reclaimable)},
	}
	return s
}
