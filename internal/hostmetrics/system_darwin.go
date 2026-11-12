package hostmetrics

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func readSystem(ctx context.Context, options Options) reading {
	r := reading{Snapshot: Snapshot{OS: "macOS"}}
	r.Hostname, _ = os.Hostname()
	cores, _ := unix.SysctlUint32("hw.logicalcpu")
	r.CPU.Cores = int(cores)
	r.CPU.Model, _ = unix.Sysctl("machdep.cpu.brand_string")
	if boot, err := unix.SysctlTimeval("kern.boottime"); err == nil {
		seconds := uint64(max(0, time.Now().Unix()-boot.Sec))
		r.Uptime = &seconds
	}
	command := func(path string, args ...string) string {
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(bounded, path, args...).Output()
		if err != nil {
			return ""
		}
		return string(out)
	}
	// top's second sample gives an interval reading rather than a boot-time average.
	r.CPU.Percent = parseDarwinCPU(command("/usr/bin/top", "-l", "2", "-s", "1", "-n", "0", "-stats", "cpu"))
	if r.CPU.Percent == nil {
		r.Unavailable = append(r.Unavailable, "cpu")
	}
	r.CPU.Load = parseLoad(strings.Trim(command("/usr/sbin/sysctl", "-n", "vm.loadavg"), "{} \n"))
	if len(r.CPU.Load) == 0 {
		r.Unavailable = append(r.Unavailable, "load")
	}
	total, err := unix.SysctlUint64("hw.memsize")
	if err == nil {
		r.Memory = parseDarwinMemory(total, command("/usr/bin/vm_stat"))
	}
	if r.Memory == nil {
		r.Unavailable = append(r.Unavailable, "memory")
	}
	r.Swap = parseDarwinSwap(command("/usr/sbin/sysctl", "-n", "vm.swapusage"))
	if r.Swap == nil {
		r.Unavailable = append(r.Unavailable, "swap")
	}
	r.Disk, err = readDisk(options.DataDir)
	if err != nil {
		r.Unavailable = append(r.Unavailable, "disk")
	}
	r.Network = parseDarwinNetwork(command("/usr/sbin/netstat", "-ibn"))
	if r.Network == nil {
		r.Unavailable = append(r.Unavailable, "network")
	}
	r.CollectedAt = time.Now().UTC()
	return r
}

var darwinCPU = regexp.MustCompile(`CPU usage:.*?([\d.]+)% idle`)
var darwinPageSize = regexp.MustCompile(`page size of (\d+) bytes`)
var darwinSwap = regexp.MustCompile(`total = ([\d.]+)([KMG])\s+used = ([\d.]+)([KMG])\s+free = ([\d.]+)([KMG])`)

func parseDarwinCPU(body string) *float64 {
	matches := darwinCPU.FindAllStringSubmatch(body, -1)
	if len(matches) < 2 {
		return nil
	}
	idle, err := strconv.ParseFloat(matches[len(matches)-1][1], 64)
	if err != nil || idle < 0 || idle > 100 {
		return nil
	}
	return pointer(100 - idle)
}

func parseDarwinMemory(total uint64, body string) *Memory {
	m := darwinPageSize.FindStringSubmatch(body)
	if len(m) != 2 || total == 0 {
		return nil
	}
	page, _ := strconv.ParseUint(m[1], 10, 64)
	values := map[string]uint64{}
	for line := range strings.SplitSeq(body, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.Trim(strings.TrimSpace(value), "."), 10, 64)
		if err == nil {
			values[key] = n
		}
	}
	if _, ok := values["Pages free"]; !ok {
		return nil
	}
	// Inactive pages include reclaimable file cache; compressed/wired memory stays used.
	available := values["Pages free"] + values["Pages inactive"] + values["Pages speculative"]
	return memory(total, available*page)
}

func parseDarwinSwap(body string) *Memory {
	m := darwinSwap.FindStringSubmatch(body)
	if len(m) != 7 {
		return nil
	}
	bytes := func(value, unit string) uint64 {
		n, _ := strconv.ParseFloat(value, 64)
		return uint64(n * map[string]float64{"K": 1024, "M": 1024 * 1024, "G": 1024 * 1024 * 1024}[unit])
	}
	return memory(bytes(m[1], m[2]), bytes(m[5], m[6]))
}

func parseDarwinNetwork(body string) *Network {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return nil
	}
	header := strings.Fields(lines[0])
	columns := map[string]int{}
	for i, field := range header {
		columns[field] = i
	}
	for _, field := range []string{"Name", "Network", "Ibytes", "Obytes", "Ierrs", "Oerrs"} {
		if _, ok := columns[field]; !ok {
			return nil
		}
	}
	out := &Network{Interfaces: []Interface{}}
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) <= columns["Obytes"] || !strings.HasPrefix(f[columns["Network"]], "<Link#") {
			continue
		}
		name := strings.TrimSuffix(f[columns["Name"]], "*")
		if !physicalInterface(name) {
			continue
		}
		number := func(column string) uint64 { n, _ := strconv.ParseUint(f[columns[column]], 10, 64); return n }
		nic := Interface{Name: name, Received: number("Ibytes"), Sent: number("Obytes"), Errors: number("Ierrs") + number("Oerrs")}
		out.Received += nic.Received
		out.Sent += nic.Sent
		out.Interfaces = append(out.Interfaces, nic)
	}
	return out
}
