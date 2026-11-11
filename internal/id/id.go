// Package id generates ULIDs: 26-character, lexicographically time-sortable ids.
package id

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford base32

var (
	mu     sync.Mutex
	lastMS uint64
	last   [16]byte
)

// New returns a new ULID (48-bit millisecond timestamp + 80 random bits). Ids from one
// process are strictly increasing: within the same millisecond (or if the clock steps
// back) the previous id is incremented instead of drawing new random bits, so sorting by
// id is sorting by creation order.
func New() string {
	mu.Lock()
	defer mu.Unlock()
	ms := uint64(time.Now().UnixMilli())
	if ms > lastMS {
		lastMS = ms
		last = fill(ms)
	} else {
		for i := 15; i >= 0; i-- { // increment as one big-endian number
			last[i]++
			if last[i] != 0 {
				break
			}
		}
	}
	return encode(last)
}

func at(t time.Time) string { return encode(fill(uint64(t.UnixMilli()))) }

func fill(ms uint64) [16]byte {
	var b [16]byte
	b[0], b[1], b[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	b[3], b[4], b[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		panic("id: crypto/rand failed: " + err.Error())
	}
	return b
}

func encode(b [16]byte) string {
	hi, lo := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = alphabet[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
