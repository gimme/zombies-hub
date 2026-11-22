package main

import (
	"bytes"
	"crypto/rand"
	"slices"
	"testing"
)

func frames(t *testing.T, packet []byte, nextID *uint16) [][]byte {
	t.Helper()
	var datagrams [][]byte
	framePacket(packet, nextID, func(d []byte) error {
		if len(d) > maxDatagram {
			t.Fatalf("a %d-byte datagram", len(d))
		}
		datagrams = append(datagrams, slices.Clone(d))
		return nil
	})
	return datagrams
}

func randomPacket(size int) []byte {
	p := make([]byte, size)
	rand.Read(p)
	return p
}

func TestFramingRoundTrip(t *testing.T) {
	for _, size := range []int{0, 1, maxDatagram - 1, maxDatagram, 1264, 2 * fragmentSize, 2*fragmentSize + 1, maxPacket} {
		packet := randomPacket(size)
		var id uint16
		datagrams := frames(t, packet, &id)
		wantCount := 1
		if size+1 > maxDatagram {
			wantCount = (size + fragmentSize - 1) / fragmentSize
		}
		if len(datagrams) != wantCount {
			t.Fatalf("%d bytes: %d datagrams, want %d", size, len(datagrams), wantCount)
		}
		// backwards: reordering does not matter
		var r reassembly
		var got []byte
		for i := len(datagrams) - 1; i >= 0; i-- {
			if p := r.receive(datagrams[i]); p != nil {
				if got != nil {
					t.Fatalf("%d bytes: completed twice", size)
				}
				got = slices.Clone(p)
			}
		}
		if got == nil || !bytes.Equal(got, packet) {
			t.Fatalf("%d bytes: got %d bytes back", size, len(got))
		}
	}
}

func TestFramingTooLarge(t *testing.T) {
	var id uint16
	if d := frames(t, randomPacket(maxPacket+1), &id); len(d) != 0 {
		t.Fatalf("%d datagrams for a packet over the limit", len(d))
	}
}

func TestFramingInterleavedAndEvicted(t *testing.T) {
	var id uint16
	a, b, c := randomPacket(3*fragmentSize), randomPacket(3*fragmentSize), randomPacket(2*fragmentSize)
	fa, fb, fc := frames(t, a, &id), frames(t, b, &id), frames(t, c, &id)
	var r reassembly
	var done [][]byte
	feed := func(d []byte) {
		if p := r.receive(d); p != nil {
			done = append(done, slices.Clone(p))
		}
	}
	// two at once complete
	feed(fa[0])
	feed(fb[0])
	feed(fa[1])
	feed(fb[1])
	feed(fa[2])
	feed(fb[2])
	if len(done) != 2 || !bytes.Equal(done[0], a) || !bytes.Equal(done[1], b) {
		t.Fatalf("completed %d packets, want a and b", len(done))
	}

	// a third one while two are open drops the one started first (a): its first piece is gone, b is whole
	done = nil
	a, b = randomPacket(3*fragmentSize), randomPacket(3*fragmentSize)
	fa, fb = frames(t, a, &id), frames(t, b, &id)
	feed(fa[0])
	feed(fb[0])
	feed(fc[0])
	feed(fc[1])
	feed(fa[1])
	feed(fa[2])
	feed(fb[1])
	feed(fb[2])
	if len(done) != 2 || !bytes.Equal(done[0], c) || !bytes.Equal(done[1], b) {
		t.Fatalf("completed %d packets, want c and b", len(done))
	}
}

func TestFramingRefusesMalformed(t *testing.T) {
	full := make([]byte, fragmentHeader+fragmentSize)
	full[0] = frameFragment
	for name, d := range map[string][]byte{
		"empty":            {},
		"unknown type":     {0x02, 1, 2, 3},
		"header only":      {frameFragment, 0, 0, 0, 2},
		"one fragment":     append([]byte{frameFragment, 0, 0, 0, 1}, make([]byte, 10)...),
		"index past count": append([]byte{frameFragment, 0, 0, 2, 2}, make([]byte, 10)...),
		"short middle":     append([]byte{frameFragment, 0, 0, 0, 2}, make([]byte, fragmentSize-1)...),
		"long last":        append([]byte{frameFragment, 0, 0, 1, 2}, make([]byte, fragmentSize+1)...),
		"too many":         append([]byte{frameFragment, 0, 0, 0, maxFragments + 1}, make([]byte, fragmentSize)...),
	} {
		var r reassembly
		if p := r.receive(d); p != nil {
			t.Errorf("%s: returned %d bytes", name, len(p))
		}
	}

	// the same id with another count is not the same packet
	var r reassembly
	first := slices.Clone(full)
	first[4] = 3
	r.receive(first)
	second := slices.Clone(full)
	second[3], second[4] = 1, 2
	if p := r.receive(second); p != nil {
		t.Errorf("completed a packet from pieces with different counts")
	}

	// a duplicate piece does not count twice
	var r2 reassembly
	piece := slices.Clone(full)
	piece[4] = 2
	r2.receive(piece)
	if p := r2.receive(piece); p != nil {
		t.Errorf("completed a packet from one piece twice")
	}
}
