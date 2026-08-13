package history

import (
	"crypto/rand"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a lexicographically-sortable, collision-resistant
// identifier: 48 bits of milliseconds since the Unix epoch followed by 80
// bits of randomness, encoded in 26 Crockford base32 characters.
func NewULID(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	ms := uint64(t.UnixMilli())
	var entropy [10]byte
	_, _ = rand.Read(entropy[:])

	// 26 chars × 5 bits = 130 slots. The first 2 are padding zero so the
	// 128 data bits fit exactly, MSB-first.
	var bits [130]byte
	for i := 0; i < 48; i++ {
		if (ms>>(47-i))&1 == 1 {
			bits[2+i] = 1
		}
	}
	for i := 0; i < 80; i++ {
		if (entropy[i/8]>>(7-i%8))&1 == 1 {
			bits[2+48+i] = 1
		}
	}

	out := make([]byte, 26)
	for c := 0; c < 26; c++ {
		var val byte
		for b := 0; b < 5; b++ {
			val = val<<1 | bits[c*5+b]
		}
		out[c] = crockford[val]
	}
	return string(out)
}
