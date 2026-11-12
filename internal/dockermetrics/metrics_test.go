package dockermetrics

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

type fakeClient struct {
	infoErr, netErr, diskErr error
	diskCalls                int
	options                  client.DiskUsageOptions
}

func (f *fakeClient) ClientVersion() string { return "1.52" }
func (f *fakeClient) Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
	return client.SystemInfoResult{Info: system.Info{
		Name: "docker-host", ServerVersion: "29.0.0", Containers: 8, ContainersRunning: 5, ContainersPaused: 1, ContainersStopped: 2,
		Images: 12, NCPU: 4, MemTotal: 8 * 1024 * 1024 * 1024, Driver: "overlay2", DockerRootDir: "/var/lib/docker",
		HTTPProxy: "http://private-user:private-password@proxy.internal:8080",
	}}, f.infoErr
}
func (f *fakeClient) NetworkList(context.Context, client.NetworkListOptions) (client.NetworkListResult, error) {
	return client.NetworkListResult{Items: make([]network.Summary, 3)}, f.netErr
}
func (f *fakeClient) DiskUsage(_ context.Context, options client.DiskUsageOptions) (client.DiskUsageResult, error) {
	f.diskCalls++
	f.options = options
	return client.DiskUsageResult{
		Images:     client.ImagesDiskUsage{TotalCount: 12, ActiveCount: 5, TotalSize: 1024, Reclaimable: 512},
		Containers: client.ContainersDiskUsage{TotalCount: 8, ActiveCount: 6, TotalSize: 100, Reclaimable: 10},
		Volumes:    client.VolumesDiskUsage{TotalCount: 3, ActiveCount: 2, TotalSize: 1000, Reclaimable: 100},
		BuildCache: client.BuildCacheDiskUsage{TotalCount: 10, ActiveCount: 1, TotalSize: 2000, Reclaimable: 1500},
	}, f.diskErr
}

func TestDockerCountsAndCachedDisk(t *testing.T) {
	f := &fakeClient{}
	c := New(f)
	now := time.Now().UTC()
	c.now = func() time.Time { return now }
	c.collect(context.Background())
	s := c.Snapshot()
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-password") || strings.Contains(string(encoded), "HTTPProxy") || strings.Contains(string(encoded), "HttpProxy") {
		t.Fatal("Docker proxy credentials were included in metrics")
	}
	if !s.Online || s.Engine.Version != "29.0.0" || s.Engine.APIVersion != "1.52" || s.Containers.Running != 5 || s.Containers.Paused != 1 || s.Containers.Stopped != 2 || *s.Images != 12 || *s.Networks != 3 {
		t.Fatalf("Docker information = %+v", s)
	}
	if *s.Storage.Images.Size != 1024 || *s.Storage.BuildCache.Reclaimable != 1500 || s.Storage.Volumes.Count != 3 || s.StorageAt == nil {
		t.Fatalf("storage = %+v", s.Storage)
	}
	if !f.options.Containers || !f.options.Images || !f.options.Volumes || !f.options.BuildCache || f.options.Verbose {
		t.Fatalf("expected only aggregate disk usage: %+v", f.options)
	}
	for range 3 {
		now = now.Add(Interval)
		c.collect(context.Background())
	}
	if f.diskCalls != 1 {
		t.Fatalf("disk scanned %d times in 45 seconds", f.diskCalls)
	}
	now = now.Add(Interval)
	c.collect(context.Background())
	if f.diskCalls != 2 {
		t.Fatal("disk did not refresh after a minute")
	}
}

func TestDockerFailuresKeepLastKnownValues(t *testing.T) {
	f := &fakeClient{}
	c := New(f)
	now := time.Now().UTC()
	c.now = func() time.Time { return now }
	c.collect(context.Background())
	good := c.Snapshot()
	f.diskErr, f.netErr = errors.New("disk busy"), errors.New("network unavailable")
	now = now.Add(time.Minute)
	c.collect(context.Background())
	s := c.Snapshot()
	if !s.Online || s.Networks != nil || !s.StorageAt.Equal(*good.StorageAt) || len(s.Unavailable) != 2 {
		t.Fatalf("partial failure = %+v", s)
	}
	now = now.Add(Interval)
	c.collect(context.Background())
	s = c.Snapshot()
	if len(s.Unavailable) != 2 || f.diskCalls != 2 {
		t.Fatalf("disk failure was lost or retried too soon: %+v", s)
	}
	f.infoErr = errors.New("socket refused")
	now = now.Add(Interval)
	c.collect(context.Background())
	s = c.Snapshot()
	if s.Online || s.Containers.Running != 5 || len(s.Unavailable) != 1 || s.Unavailable[0] != "daemon" || !s.CollectedAt.Before(s.CheckedAt) {
		t.Fatalf("daemon down = %+v", s)
	}
	f.infoErr, f.diskErr, f.netErr = nil, nil, nil
	now = now.Add(time.Minute)
	c.collect(context.Background())
	if s := c.Snapshot(); !s.Online || len(s.Unavailable) != 0 || s.Networks == nil || !s.StorageAt.After(*good.StorageAt) {
		t.Fatalf("recovery = %+v", s)
	}
}

func TestDockerFirstFailureAndUnknownSizes(t *testing.T) {
	f := &fakeClient{infoErr: errors.New("daemon unavailable")}
	c := New(f)
	c.collect(context.Background())
	if s := c.Snapshot(); s.Online || s.Engine != nil || s.Containers != nil || s.Storage != nil || s.CollectedAt != nil {
		t.Fatalf("unavailable daemon reported zero values: %+v", s)
	}
	s := storage(client.DiskUsageResult{Volumes: client.VolumesDiskUsage{TotalSize: -1, Reclaimable: -1}})
	if s.Volumes.Size != nil || s.Volumes.Reclaimable != nil {
		t.Fatal("unsupported volume size was reported as zero")
	}
}
