package hostmetrics

import "testing"

func TestDarwinParsers(t *testing.T) {
	if p := parseDarwinCPU("CPU usage: 10% user, 10% sys, 80% idle\nCPU usage: 20% user, 15% sys, 65% idle\n"); p == nil || *p != 35 {
		t.Fatalf("CPU interval = %v", p)
	}
	if parseDarwinCPU("CPU usage: 10% user, 10% sys, 80% idle") != nil {
		t.Fatal("first CPU sample must not be used")
	}
	ram := parseDarwinMemory(100*16384, "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 10.\nPages inactive: 20.\nPages speculative: 5.\nPages wired down: 40.\n")
	if ram == nil || ram.Percent != 65 || ram.Available != 35*16384 {
		t.Fatalf("RAM = %+v", ram)
	}
	swap := parseDarwinSwap("total = 1024.00M  used = 256.00M  free = 768.00M  (encrypted)")
	if swap == nil || swap.Percent != 25 {
		t.Fatalf("swap = %+v", swap)
	}
	if parseDarwinMemory(100, "broken") != nil || parseDarwinSwap("broken") != nil {
		t.Fatal("malformed readings became zero")
	}
	net := parseDarwinNetwork("Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\nen0 1500 <Link#4> aa:bb:cc:dd:ee:ff 10 1 1000 20 2 2000 0\nen0 1500 192.168.1 192.168.1.2 10 0 1000 20 0 2000 0\nutun0 1500 <Link#5> aa:bb:cc:dd:ee:ff 10 0 9999 20 0 9999 0\n")
	if net == nil || len(net.Interfaces) != 1 || net.Received != 1000 || net.Sent != 2000 || net.Interfaces[0].Errors != 3 {
		t.Fatalf("network = %+v", net)
	}
}
