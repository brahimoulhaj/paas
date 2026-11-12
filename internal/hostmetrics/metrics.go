// Package hostmetrics samples the machine running the panel. Samples are shared by
// all dashboard clients; reading the API never starts another monitoring process.
package hostmetrics

import (
	"context"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Interval = 5 * time.Second
const historySize = 120 // ten minutes, kept in memory only

type CPU struct {
	Cores   int       `json:"cores"`
	Model   string    `json:"model"`
	Percent *float64  `json:"percent"`
	Load    []float64 `json:"load"` // one, five and fifteen minutes; nil if unavailable
}

type Memory struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Available uint64  `json:"available"`
	Percent   float64 `json:"percent"`
}

type Disk struct {
	Memory
	Path          string   `json:"path"`
	InodesPercent *float64 `json:"inodes_percent"`
}

type Interface struct {
	Name        string   `json:"name"`
	Received    uint64   `json:"received"`
	Sent        uint64   `json:"sent"`
	ReceiveRate *float64 `json:"receive_rate"`
	SendRate    *float64 `json:"send_rate"`
	Errors      uint64   `json:"errors"`
	Drops       uint64   `json:"drops"`
}

type Network struct {
	Received    uint64      `json:"received"`
	Sent        uint64      `json:"sent"`
	ReceiveRate *float64    `json:"receive_rate"`
	SendRate    *float64    `json:"send_rate"`
	Interfaces  []Interface `json:"interfaces"`
}

type Point struct {
	At          time.Time `json:"at"`
	CPU         *float64  `json:"cpu"`
	Memory      *float64  `json:"memory"`
	ReceiveRate *float64  `json:"receive_rate"`
	SendRate    *float64  `json:"send_rate"`
}

type Snapshot struct {
	CollectedAt time.Time `json:"collected_at"`
	Hostname    string    `json:"hostname"`
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	Uptime      *uint64   `json:"uptime_seconds"`
	CPU         CPU       `json:"cpu"`
	Memory      *Memory   `json:"memory"`
	Swap        *Memory   `json:"swap"`
	Disk        *Disk     `json:"disk"`
	Network     *Network  `json:"network"`
	History     []Point   `json:"history"`
	Unavailable []string  `json:"unavailable"`
}

type Options struct {
	DataDir string
	// ProcRoot defaults to /host/proc when mounted, otherwise /proc on Linux.
	ProcRoot string
}

type reading struct {
	Snapshot
	totalCPU, idleCPU uint64
	cpuValid          bool
}

type Collector struct {
	mu       sync.RWMutex
	options  Options
	read     func(context.Context, Options) reading
	previous reading
	latest   Snapshot
}

func New(options Options) *Collector {
	if options.DataDir == "" {
		options.DataDir = "/"
	}
	return &Collector{options: options, read: readSystem}
}

// Run samples immediately, then every five seconds until shutdown.
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
	s.History = append([]Point{}, s.History...)
	return s
}

func (c *Collector) collect(ctx context.Context) {
	r := c.read(ctx, c.options)
	if ctx.Err() != nil {
		return
	}
	if r.OS == "" {
		r.OS = runtime.GOOS
	}
	r.Arch = runtime.GOARCH
	if r.CPU.Cores == 0 {
		r.CPU.Cores = runtime.NumCPU()
	}
	if r.Unavailable == nil {
		r.Unavailable = []string{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.previous
	if r.cpuValid && prev.cpuValid && r.totalCPU > prev.totalCPU && r.idleCPU >= prev.idleCPU {
		total, idle := r.totalCPU-prev.totalCPU, r.idleCPU-prev.idleCPU
		if idle <= total {
			r.CPU.Percent = pointer(100 * float64(total-idle) / float64(total))
		}
	}
	if r.Network != nil {
		elapsed := r.CollectedAt.Sub(prev.CollectedAt).Seconds()
		prior := map[string]Interface{}
		if prev.Network != nil {
			for _, nic := range prev.Network.Interfaces {
				prior[nic.Name] = nic
			}
		}
		rx, tx, measured := 0.0, 0.0, false
		for i := range r.Network.Interfaces {
			nic := &r.Network.Interfaces[i]
			before, exists := prior[nic.Name]
			if !exists || elapsed <= 0 || nic.Received < before.Received || nic.Sent < before.Sent {
				continue
			}
			nic.ReceiveRate = pointer(float64(nic.Received-before.Received) / elapsed)
			nic.SendRate = pointer(float64(nic.Sent-before.Sent) / elapsed)
			rx += *nic.ReceiveRate
			tx += *nic.SendRate
			measured = true
		}
		if measured {
			r.Network.ReceiveRate, r.Network.SendRate = pointer(rx), pointer(tx)
		}
	}
	point := Point{At: r.CollectedAt, CPU: r.CPU.Percent}
	if r.Memory != nil {
		point.Memory = pointer(r.Memory.Percent)
	}
	if r.Network != nil {
		point.ReceiveRate, point.SendRate = r.Network.ReceiveRate, r.Network.SendRate
	}
	history := append(c.latest.History, point)
	if len(history) > historySize {
		history = history[len(history)-historySize:]
	}
	r.History = history
	c.previous, c.latest = r, r.Snapshot
}

func pointer(v float64) *float64 { return &v }

func memory(total, available uint64) *Memory {
	if total == 0 {
		return &Memory{}
	}
	available = min(total, available)
	used := total - available
	return &Memory{Total: total, Available: available, Used: used, Percent: math.Min(100, 100*float64(used)/float64(total))}
}

func parseLoad(body string) []float64 {
	f := strings.Fields(body)
	if len(f) < 3 {
		return nil
	}
	out := make([]float64, 3)
	for i := range out {
		n, err := strconv.ParseFloat(f[i], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

func physicalInterface(name string) bool {
	for _, prefix := range []string{"lo", "veth", "docker", "br-", "virbr", "cni", "flannel", "tun", "tap", "utun", "awdl", "llw", "anpi", "gif", "stf", "bridge"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return name != ""
}
