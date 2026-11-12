package hostmetrics

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func readLinuxSystem(ctx context.Context, options Options) reading {
	r := reading{Snapshot: Snapshot{CollectedAt: time.Now().UTC(), OS: "Linux"}}
	root := options.ProcRoot
	if root == "" {
		root = "/proc"
		if _, err := os.Stat("/host/proc/stat"); err == nil {
			root = "/host/proc"
		}
	}
	read := func(name string) string {
		if ctx.Err() != nil {
			return ""
		}
		b, _ := os.ReadFile(filepath.Join(root, name))
		return string(b)
	}
	if root != "/proc" {
		r.Hostname = strings.TrimSpace(read("1/root/etc/hostname"))
	}
	if r.Hostname == "" {
		r.Hostname = strings.TrimSpace(read("sys/kernel/hostname"))
	}
	if r.Hostname == "" {
		r.Hostname, _ = os.Hostname()
	}
	r.totalCPU, r.idleCPU, r.CPU.Cores, r.cpuValid = parseCPU(read("stat"))
	if !r.cpuValid {
		r.Unavailable = append(r.Unavailable, "cpu")
	}
	for line := range strings.SplitSeq(read("cpuinfo"), "\n") {
		key, value, _ := strings.Cut(line, ":")
		if strings.TrimSpace(key) == "model name" || strings.TrimSpace(key) == "Hardware" {
			r.CPU.Model = strings.TrimSpace(value)
			break
		}
	}
	r.CPU.Load = parseLoad(read("loadavg"))
	if len(r.CPU.Load) == 0 {
		r.Unavailable = append(r.Unavailable, "load")
	}
	if fields := strings.Fields(read("uptime")); len(fields) > 0 {
		if uptime, err := strconv.ParseFloat(fields[0], 64); err == nil && uptime >= 0 {
			seconds := uint64(uptime)
			r.Uptime = &seconds
		}
	}
	r.Memory, r.Swap = parseMemory(read("meminfo"))
	if r.Memory == nil {
		r.Unavailable = append(r.Unavailable, "memory")
	}
	if r.Swap == nil {
		r.Unavailable = append(r.Unavailable, "swap")
	}
	var err error
	r.Disk, err = readDisk(options.DataDir)
	if err != nil {
		r.Unavailable = append(r.Unavailable, "disk")
	}
	// /proc/net/dev is namespace-relative. With a host proc mount, explicitly read
	// host PID 1's network namespace. Never label the panel's eth0 as host traffic.
	networkFile := "net/dev"
	_, container := os.Stat("/.dockerenv")
	if root != "/proc" {
		networkFile = "1/net/dev"
	}
	if root == "/proc" && container == nil {
		r.Unavailable = append(r.Unavailable, "host_network_mount")
	} else {
		r.Network = parseNetwork(read(networkFile))
		if r.Network == nil {
			r.Unavailable = append(r.Unavailable, "network")
		}
	}
	return r
}

func parseCPU(body string) (total, idle uint64, cores int, valid bool) {
	for line := range strings.SplitSeq(body, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] != "cpu" {
			if strings.HasPrefix(f[0], "cpu") {
				cores++
			}
			continue
		}
		if len(f) < 5 {
			continue
		}
		valid = true
		// user/nice already include guest time; count only the first eight fields.
		for i := 1; i < len(f) && i <= 8; i++ {
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				valid = false
				break
			}
			total += n
			if i == 4 || i == 5 {
				idle += n
			} // idle plus iowait
		}
	}
	return
}

func parseMemory(body string) (*Memory, *Memory) {
	values := map[string]uint64{}
	for line := range strings.SplitSeq(body, "\n") {
		key, value, ok := strings.Cut(line, ":")
		f := strings.Fields(value)
		if !ok || len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err == nil {
			values[key] = n * 1024
		}
	}
	var ram, swap *Memory
	if total := values["MemTotal"]; total > 0 {
		available, ok := values["MemAvailable"]
		if !ok {
			available = values["MemFree"] + values["Buffers"] + values["Cached"]
		}
		ram = memory(total, available)
	}
	if total, ok := values["SwapTotal"]; ok {
		swap = memory(total, values["SwapFree"])
	}
	return ram, swap
}

func parseNetwork(body string) *Network {
	if !strings.Contains(body, "Inter-") {
		return nil
	}
	out := &Network{Interfaces: []Interface{}}
	for line := range strings.SplitSeq(body, "\n") {
		name, counters, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || !physicalInterface(name) {
			continue
		}
		f := strings.Fields(counters)
		if len(f) < 16 {
			continue
		}
		numbers := make([]uint64, 16)
		valid := true
		for i := range numbers {
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				valid = false
				break
			}
			numbers[i] = n
		}
		if !valid {
			continue
		}
		nic := Interface{Name: name, Received: numbers[0], Sent: numbers[8], Errors: numbers[2] + numbers[10], Drops: numbers[3] + numbers[11]}
		out.Received += nic.Received
		out.Sent += nic.Sent
		out.Interfaces = append(out.Interfaces, nic)
	}
	return out
}
