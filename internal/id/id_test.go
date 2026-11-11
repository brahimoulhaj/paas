package id

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		v := New()
		if len(v) != 26 {
			t.Fatalf("length %d, want 26", len(v))
		}
		if seen[v] {
			t.Fatalf("duplicate id %s", v)
		}
		seen[v] = true
	}
}

func TestSortableByTime(t *testing.T) {
	a := at(time.UnixMilli(1_700_000_000_000))
	b := at(time.UnixMilli(1_700_000_000_001))
	if !(a < b) {
		t.Fatalf("%s should sort before %s", a, b)
	}
	// Known timestamp prefix: the first 10 characters encode the time only.
	if got := at(time.UnixMilli(0))[:10]; got != "0000000000" {
		t.Fatalf("epoch prefix = %s", got)
	}
}

func TestNewIsStrictlyIncreasing(t *testing.T) {
	prev := New()
	for i := 0; i < 20000; i++ { // far more than one per millisecond
		next := New()
		if next <= prev {
			t.Fatalf("id %d: %s is not after %s", i, next, prev)
		}
		prev = next
	}
}
