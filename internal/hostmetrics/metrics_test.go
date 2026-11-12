package hostmetrics

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinuxHostReading(t *testing.T) {
	proc := t.TempDir()
	files := map[string]string{
		"stat":                "cpu 100 20 50 800 10 5 5 10 99 20\ncpu0 0\ncpu1 0\n",
		"cpuinfo":             "model name : Test host CPU\n",
		"meminfo":             "MemTotal: 1000 kB\nMemAvailable: 600 kB\nMemFree: 100 kB\nSwapTotal: 200 kB\nSwapFree: 150 kB\n",
		"loadavg":             "0.10 0.20 0.30 1/123 12\n",
		"uptime":              "12345.67 9999.0\n",
		"1/root/etc/hostname": "server.example\n",
		"sys/kernel/hostname": "panel-container\n",
		// The container's virtual interface must not replace the host's counters.
		"net/dev": "Inter-\n eth0: 99999 0 0 0 0 0 0 0 99999 0 0 0 0 0 0 0\n",
		"1/net/dev": "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast |bytes packets errs drop fifo colls carrier compressed\n" +
			" eth0: 1024 10 2 3 0 0 0 0 2048 20 4 5 0 0 0 0\n" +
			" lo: 900000 0 0 0 0 0 0 0 900000 0 0 0 0 0 0 0\n" +
			" veth123: 800000 0 0 0 0 0 0 0 800000 0 0 0 0 0 0 0\n",
	}
	for name, content := range files {
		path := filepath.Join(proc, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := readLinuxSystem(context.Background(), Options{ProcRoot: proc, DataDir: proc})
	if r.Hostname != "server.example" || r.CPU.Cores != 2 || r.CPU.Model != "Test host CPU" || r.totalCPU != 1000 || r.idleCPU != 810 || !r.cpuValid {
		t.Fatalf("host CPU = %+v, totals %d/%d", r, r.totalCPU, r.idleCPU)
	}
	if r.Memory == nil || r.Memory.Percent != 40 || r.Memory.Used != 400*1024 || r.Swap == nil || r.Swap.Percent != 25 {
		t.Fatalf("memory = %+v, swap = %+v", r.Memory, r.Swap)
	}
	if r.Network == nil || len(r.Network.Interfaces) != 1 || r.Network.Received != 1024 || r.Network.Sent != 2048 || r.Network.Interfaces[0].Errors != 6 || r.Network.Interfaces[0].Drops != 8 {
		t.Fatalf("network = %+v", r.Network)
	}
	if r.Uptime == nil || *r.Uptime != 12345 || len(r.CPU.Load) != 3 || r.CPU.Load[2] != 0.3 || r.Disk == nil || r.Disk.Total == 0 || len(r.Unavailable) != 0 {
		t.Fatalf("host data = %+v", r.Snapshot)
	}
}

func TestSamplingRatesResetsAndHistory(t *testing.T) {
	start := time.Now().UTC()
	step := 0
	c := New(Options{})
	c.read = func(context.Context, Options) reading {
		i := step
		step++
		r := reading{Snapshot: Snapshot{CollectedAt: start.Add(time.Duration(i) * Interval), CPU: CPU{Cores: 2}, Memory: memory(100, 60), Network: &Network{Interfaces: []Interface{{Name: "eth0", Received: uint64(1000 + i*100), Sent: uint64(2000 + i*50)}}}}, totalCPU: uint64(1000 + i*100), idleCPU: uint64(800 + i*60), cpuValid: true}
		if i == 2 { // Host reboot/interface reset: no unsigned subtraction spikes.
			r.totalCPU, r.idleCPU = 10, 5
			r.Network.Interfaces[0].Received, r.Network.Interfaces[0].Sent = 1, 1
		}
		return r
	}
	c.collect(context.Background())
	if s := c.Snapshot(); s.CPU.Percent != nil || s.Network.ReceiveRate != nil || len(s.History) != 1 {
		t.Fatalf("first sample must establish a baseline: %+v", s)
	}
	c.collect(context.Background())
	s := c.Snapshot()
	if s.CPU.Percent == nil || *s.CPU.Percent != 40 || *s.Network.ReceiveRate != 20 || *s.Network.SendRate != 10 {
		t.Fatalf("interval rates = %+v, %+v", s.CPU, s.Network)
	}
	// Callers cannot corrupt the collector's history by modifying the returned slice.
	s.History[0].At = time.Time{}
	if c.Snapshot().History[0].At.IsZero() {
		t.Fatal("history was not copied")
	}
	c.collect(context.Background())
	if s := c.Snapshot(); s.CPU.Percent != nil || s.Network.ReceiveRate != nil {
		t.Fatalf("counter reset emitted a rate: %+v", s)
	}
	for range historySize + 5 {
		c.collect(context.Background())
	}
	if s := c.Snapshot(); len(s.History) != historySize || !s.History[len(s.History)-1].At.Equal(s.CollectedAt) {
		t.Fatalf("bounded history = %+v", s)
	}
}

func TestUnavailableAndMalformedCounters(t *testing.T) {
	ram, swap := parseMemory("")
	if ram != nil || swap != nil || parseNetwork("broken") != nil {
		t.Fatal("missing readings became zero usage")
	}
	if _, _, _, valid := parseCPU("cpu 1 2 bad 4"); valid {
		t.Fatal("malformed CPU accepted")
	}
	for _, body := range []string{"NaN 1 2", "+Inf 1 2", "1 2", "-1 0 0"} {
		if parseLoad(body) != nil {
			t.Fatalf("invalid load %q accepted", body)
		}
	}
	if m := memory(100, 200); m.Percent != 0 || m.Available != 100 || math.IsNaN(m.Percent) {
		t.Fatal(m)
	}
	if m := memory(0, 0); m.Percent != 0 || math.IsNaN(m.Percent) {
		t.Fatal(m)
	}
	r := readLinuxSystem(context.Background(), Options{ProcRoot: t.TempDir(), DataDir: "/missing-disk-path"})
	if r.Memory != nil || r.Network != nil || r.Disk != nil || len(r.Unavailable) < 4 {
		t.Fatalf("missing host files = %+v", r)
	}
}

func TestCollectorStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := New(Options{})
	started := make(chan struct{})
	c.read = func(ctx context.Context, _ Options) reading { close(started); <-ctx.Done(); return reading{} }
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector did not stop")
	}
}
