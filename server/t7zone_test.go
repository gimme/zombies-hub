package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// t7FastfileOf is a fastfile of Black Ops III with the zone, in blocks of blockSize bytes of it. With padAfter at 0
// or more, a block of padding follows that many blocks, and the rest start at the file's next multiple of 8 MB. Its
// header has a checksum of the zone, so that the fastfiles of two zones differ there.
func t7FastfileOf(zone []byte, blockSize, padAfter int) []byte {
	out := make([]byte, t7HeaderSize)
	copy(out, "TAff0000\x51\x02\x00\x00\x00\x01")
	binary.LittleEndian.PutUint32(out[16:], crc32.ChecksumIEEE(zone))
	binary.LittleEndian.PutUint64(out[144:], uint64(len(zone)))
	block := func(compressed []byte, makes int) {
		for _, v := range []int{len(compressed), makes, (len(compressed) + 3) &^ 3, len(out)} {
			out = binary.LittleEndian.AppendUint32(out, uint32(v))
		}
		out = append(out, compressed...)
		out = append(out, make([]byte, -len(compressed)&3)...)
	}
	for n := 0; len(zone) > 0; n++ {
		if n == padAfter {
			block(bytes.Repeat([]byte{0xaf}, 8), 0)
			out = append(out, make([]byte, -len(out)&(8<<20-1))...)
		}
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		w.Write(zone[:min(blockSize, len(zone))])
		w.Close()
		block(z.Bytes(), min(blockSize, len(zone)))
		zone = zone[min(blockSize, len(zone)):]
	}
	return out
}

// vint is a number of a VCDIFF delta: 7 bits a byte, the highest first, all but the last with their top bit set.
func vint(v int) []byte {
	b := []byte{byte(v & 0x7f)}
	for v >>= 7; v > 0; v >>= 7 {
		b = append([]byte{byte(v&0x7f) | 0x80}, b...)
	}
	return b
}

// deltaWindow is a window of a delta of Black Ops III's: its indicator, the size and the position of its source
// segment when the indicator has the bit 1, another number when it has the bit 4, the size of its delta, the delta
// (the size of what it makes, no flags, its three sections' sizes, and the sections), and a checksum of 4 bytes.
func deltaWindow(indicator byte, segment, position, makes int, data, instructions, addresses []byte) []byte {
	delta := slices.Concat(vint(makes), []byte{0}, vint(len(data)), vint(len(instructions)), vint(len(addresses)),
		data, instructions, addresses)
	w := []byte{indicator}
	if indicator&1 != 0 {
		w = slices.Concat(w, vint(segment), vint(position))
	}
	if indicator&4 != 0 {
		w = append(w, vint(31693)...)
	}
	return slices.Concat(w, vint(len(delta)), delta, []byte{0xde, 0xad, 0xbe, 0xef})
}

// t7DeltaOf is the delta of the fastfile with the given header, which makes a zone of size bytes with the windows.
func t7DeltaOf(fastfile []byte, size int, windows ...[]byte) []byte {
	head := make([]byte, t7HeaderSize)
	copy(head, "TAff0000\x51\x02\x00\x00\x00\x06")
	binary.LittleEndian.PutUint64(head[144:], uint64(size))
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write([]byte("\xd6\xc3\xc4\x00\x0c"))
	for _, window := range windows {
		w.Write(window)
	}
	w.Close()
	return slices.Concat(head, fastfile[:t7HeaderSize], make([]byte, 40), z.Bytes())
}

// deltaBetween is the windows of a delta that makes the zone made from an old zone, each window of the given size:
// what is the old zone's, by where it is in it, is copied from it, and the rest is added. made lists the new zone's
// parts in order, each either a part of the old zone (from and to) or new bytes (add).
func deltaBetween(old []byte, window int, made ...deltaPart) (zone []byte, windows [][]byte) {
	type piece struct {
		from int // in the old zone, or -1 for bytes that are added
		data []byte
	}
	var pieces []piece
	for _, p := range made {
		if p.add != nil {
			pieces = append(pieces, piece{-1, p.add})
			zone = append(zone, p.add...)
		} else {
			pieces = append(pieces, piece{p.from, old[p.from:p.to]})
			zone = append(zone, old[p.from:p.to]...)
		}
	}
	for n := 0; len(pieces) > 0; n++ {
		var data, instructions, addresses []byte
		makes, copies := 0, false
		for makes < window && len(pieces) > 0 {
			p := &pieces[0]
			k := min(window-makes, len(p.data))
			if p.from < 0 {
				instructions = append(append(instructions, 1), vint(k)...) // add, of the size that follows
				data = append(data, p.data[:k]...)
			} else {
				instructions = append(append(instructions, 19), vint(k)...) // copy, of the size that follows
				addresses = append(addresses, vint(p.from)...)
				p.from, copies = p.from+k, true
			}
			if p.data, makes = p.data[k:], makes+k; len(p.data) == 0 {
				pieces = pieces[1:]
			}
		}
		// a window that copies nothing has no source segment, and every other one that has one has the number after it
		indicator := byte(0x08)
		if copies {
			indicator = 0x09 + byte(n%2)*4
		}
		windows = append(windows, deltaWindow(indicator, len(old), 0, makes, data, instructions, addresses))
	}
	return zone, windows
}

// deltaPart is a part of a new zone (deltaBetween): the old zone's from from up to to, or the bytes add.
type deltaPart struct {
	from, to int
	add      []byte
}

// zoneImageOf is an image as a zone has it whole: its header, its name, and its pixels.
func zoneImageOf(name string, w, h int, format uint64, pixels []byte) []byte {
	head := make([]byte, zoneImageHeader)
	for _, at := range []int{0, 4} {
		binary.LittleEndian.PutUint16(head[at:], uint16(w))
		binary.LittleEndian.PutUint16(head[at+2:], uint16(h))
	}
	binary.LittleEndian.PutUint64(head[40:], uint64(len(pixels)))
	binary.LittleEndian.PutUint64(head[48:], format)
	copy(head[56:], "\xff\xff\xff\xff\xff\xff\xff\xff\x53\x5f\xe4\x3e")
	return slices.Concat(head, []byte(name), []byte{0}, pixels)
}

// filler is n bytes that are no text, the same each time for a seed.
func filler(seed, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i+seed)*31+(i+seed)>>7) | 0x80
	}
	return b
}

// readPieces returns all that next reads, and how many pieces it was in.
func readPieces(t *testing.T, next func() ([]byte, error)) (zone []byte, pieces int) {
	t.Helper()
	for {
		piece, err := next()
		if err == io.EOF {
			return zone, pieces
		}
		if err != nil {
			t.Fatal(err)
		}
		zone, pieces = append(zone, piece...), pieces+1
	}
}

// VCDIFF's default code table: an instruction or two for each byte.
func TestVCDIFFCodes(t *testing.T) {
	add := func(size uint8) vcdiffOp { return vcdiffOp{kind: vcdiffAdd, size: size} }
	cp := func(size, mode uint8) vcdiffOp { return vcdiffOp{kind: vcdiffCopy, size: size, mode: mode} }
	for code, want := range map[int][2]vcdiffOp{0: {{kind: vcdiffRun}}, 1: {add(0)}, 18: {add(17)}, 19: {cp(0, 0)},
		20: {cp(4, 0)}, 34: {cp(18, 0)}, 35: {cp(0, 1)}, 162: {cp(18, 8)}, 163: {add(1), cp(4, 0)}, 165: {add(1), cp(6, 0)},
		174: {add(4), cp(6, 0)}, 234: {add(4), cp(6, 5)}, 235: {add(1), cp(4, 6)}, 246: {add(4), cp(4, 8)},
		247: {cp(4, 0), add(1)}, 255: {cp(4, 8), add(1)}} {
		if vcdiffCodes[code] != want {
			t.Errorf("code %d: %+v, want %+v", code, vcdiffCodes[code], want)
		}
	}
}

// A window of a delta: what it adds, runs of a byte, and copies from its source segment and from what it has made,
// by each mode of address.
func TestVCDIFFWindow(t *testing.T) {
	const segment = "0123456789abcdefghijklmnopqrstuvwxyz"
	source := func(p []byte, off int64) error {
		copy(p, segment[off:])
		return nil
	}
	instructions := []byte{
		21,   // copy 5, from an address from the start: abcde
		4,    // add 3: XYZ
		0, 4, // a run of 4
		36,  // copy 4, from an address back from here: XYZ-, which the window made
		38,  // copy 6, from 2 back: over what the copy itself makes
		52,  // copy 4, from an address on from the first one used: klmn
		116, // copy 4, from an address that was used, by its byte: XYZ- again
		163, // add 1, and copy 4 from the start: !0123
		22,  // copy 6: the segment's last 2, and then the window's first 4
		247, // copy 4, and add 1: uvwx?
	}
	addresses := []byte{10, 7, 2, 10, 41, 0, 34, 30}
	const want = "abcde" + "XYZ" + "----" + "XYZ-" + "Z-Z-Z-" + "klmn" + "XYZ-" + "!0123" + "yzabcd" + "uvwx?"
	got, err := vcdiffWindow([]byte("XYZ-!?"), instructions, addresses, int64(len(segment)), len(want), source)
	if err != nil || string(got) != want {
		t.Errorf("the window: %q, %v, want %q", got, err, want)
	}
	for name, c := range map[string]struct {
		data, instructions, addresses string
		makes                         int
	}{
		"a copy from where the window is":  {"", "\x14", "\x24", 4},
		"a copy from before the segment":   {"", "\x24", "\x25", 4},
		"more added than the data":         {"XY", "\x04", "", 3},
		"less made than the window":        {"XYZ", "\x04", "", 5},
		"more made than the window":        {"XYZ", "\x04", "", 2},
		"data left over":                   {"XY", "\x02", "", 1},
		"an address left over":             {"X", "\x02", "\x00", 1},
		"a run without its size":           {"X", "\x00", "", 1},
		"a copy without its address":       {"", "\x14", "", 4},
		"a copy without its address' byte": {"", "\x74", "", 4},
	} {
		if got, err := vcdiffWindow([]byte(c.data), []byte(c.instructions), []byte(c.addresses), int64(len(segment)),
			c.makes, source); err == nil {
			t.Errorf("%s: %q", name, got)
		}
	}
	// numbers: 7 bits a byte; none of more than 8 bytes, or cut short
	for _, v := range []int{0, 127, 128, 0x200000, 0x19b4ed75} {
		at := 0
		if got, ok := vcdiffInt(append(vint(v), 0xff), &at); !ok || got != int64(v) || at != len(vint(v)) {
			t.Errorf("the number %d: %d, %v, %d bytes", v, got, ok, at)
		}
	}
	for name, b := range map[string][]byte{"cut short": {0x81, 0x80}, "nothing": {}, "of 9 bytes": bytes.Repeat([]byte{0x81}, 9)} {
		at := 0
		if got, ok := vcdiffInt(b, &at); ok {
			t.Errorf("a number %s: %d", name, got)
		}
	}
}

// A fastfile's zone, block by block and from anywhere in it, past a block of padding; and what is no fastfile that
// the server reads.
func TestT7Fastfile(t *testing.T) {
	dir := t.TempDir()
	zone := filler(1, 5000)
	for name, file := range map[string][]byte{"plain.ff": t7FastfileOf(zone, 700, -1), "padded.ff": t7FastfileOf(zone, 700, 3)} {
		writeFile(t, filepath.Join(dir, name), string(file))
		ff, err := openT7Fastfile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, pieces := readPieces(t, ff.pieces()); !bytes.Equal(got, zone) || pieces != 8 {
			t.Errorf("%s: its zone is %d bytes in %d pieces, want %d in 8", name, len(got), pieces, len(zone))
		}
		part := make([]byte, 2000)
		if err := ff.readAt(part, 650); err != nil || !bytes.Equal(part, zone[650:2650]) {
			t.Errorf("%s: 2000 bytes at 650 of its zone: %v", name, err)
		}
		if err := ff.readAt(part, 3001); err == nil {
			t.Errorf("%s: read past the end of its zone", name)
		}
		ff.close()
	}
	// more blocks than a fastfile keeps: each is as it was when it is read again
	writeFile(t, filepath.Join(dir, "many.ff"), string(t7FastfileOf(zone, 10, -1)))
	ff, err := openT7Fastfile(filepath.Join(dir, "many.ff"))
	if err != nil || len(ff.blocks) != 500 {
		t.Fatalf("a fastfile of 500 blocks: %v", err)
	}
	if got, _ := readPieces(t, ff.pieces()); !bytes.Equal(got, zone) {
		t.Errorf("the zone of a fastfile of 500 blocks")
	}
	for _, at := range []int64{0, 4990, 5, 2500, 0, 4985} {
		part := make([]byte, 10)
		if err := ff.readAt(part, at); err != nil || !bytes.Equal(part, zone[at:at+10]) {
			t.Errorf("10 bytes at %d: %v", at, err)
		}
	}
	if len(ff.kept) > t7BlocksKept || len(ff.order) != len(ff.kept) {
		t.Errorf("it keeps %d blocks, in an order of %d", len(ff.kept), len(ff.order))
	}
	ff.close()

	good := t7FastfileOf(zone, 700, -1)
	lz4 := slices.Clone(good)
	lz4[13] = 4
	short := slices.Clone(good)
	binary.LittleEndian.PutUint64(short[144:], uint64(len(zone)+1))
	for name, file := range map[string][]byte{"another file": []byte("IWffu100, and more"), "compressed otherwise": lz4,
		"a zone shorter than its header says": short, "cut short": good[:len(good)-20]} {
		writeFile(t, filepath.Join(dir, "bad.ff"), string(file))
		if ff, err := openT7Fastfile(filepath.Join(dir, "bad.ff")); err == nil {
			ff.close()
			t.Errorf("%s opened as a fastfile", name)
		}
	}
}

// A zone is its fastfile's with the fastfile's delta applied. The images that it has whole are found in it, with
// their pixels: one that a block or a window cuts, one that the delta adds, and none that is only named.
func TestT7Zone(t *testing.T) {
	dir := t.TempDir()
	first, second := filler(2, 96), filler(3, 5000)
	// the first image's header ends a block of the fastfile, and its name starts the next
	old := slices.Concat(filler(4, 1328), zoneImageOf("img_preview_first", 8, 12, 98, first), filler(5, 922),
		[]byte(",img_preview_second\x00"), filler(6, 700))
	fastfile := t7FastfileOf(old, 700, -1)
	writeFile(t, filepath.Join(dir, "core.ff"), string(fastfile))
	wanted := map[string]bool{"img_preview_first": true, "img_preview_second": true, "img_preview_third": true}

	// without a delta: the fastfile's zone
	next, done, err := t7ZonePieces(dir, "core")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := readPieces(t, next); !bytes.Equal(got, old) {
		t.Errorf("the zone without a delta is not the fastfile's")
	}
	done()
	found := map[string]zoneImage{}
	next, done, _ = t7ZonePieces(dir, "core")
	if err := zoneImages(next, wanted, found); err != nil || len(found) != 1 || !bytes.Equal(found["img_preview_first"].data, first) ||
		found["img_preview_first"].w != 8 || found["img_preview_first"].h != 12 || found["img_preview_first"].format != 98 {
		t.Errorf("the images of the fastfile's zone: %v, %v", found, err)
	}
	done()

	// with a delta, which adds an image before the first and one at the zone's end, in windows of 2000 bytes
	third := filler(7, 64)
	zone, windows := deltaBetween(old, 2000, deltaPart{from: 0, to: 1000},
		deltaPart{add: zoneImageOf("img_preview_second", 100, 200, 99, second)}, deltaPart{from: 1000, to: len(old)},
		deltaPart{add: zoneImageOf("img_preview_third", 4, 4, 98, third)})
	if len(windows) != 5 {
		t.Fatalf("the delta has %d windows", len(windows))
	}
	writeFile(t, filepath.Join(dir, "core.fd"), string(t7DeltaOf(fastfile, len(zone), windows...)))
	next, done, err = t7ZonePieces(dir, "core")
	if err != nil {
		t.Fatal(err)
	}
	if got, pieces := readPieces(t, next); !bytes.Equal(got, zone) || pieces != 5 {
		t.Errorf("the zone with its delta: %d bytes in %d pieces, want %d in 5", len(got), pieces, len(zone))
	}
	done()
	found = map[string]zoneImage{}
	next, done, _ = t7ZonePieces(dir, "core")
	if err := zoneImages(next, wanted, found); err != nil || len(found) != 3 || !bytes.Equal(found["img_preview_first"].data, first) ||
		!bytes.Equal(found["img_preview_second"].data, second) || found["img_preview_second"].w != 100 ||
		found["img_preview_second"].format != 99 || !bytes.Equal(found["img_preview_third"].data, third) {
		t.Errorf("the images of the zone with its delta: %d, %v", len(found), err)
	}
	done()
	// the images that were found are not looked for again, nor found again
	found = map[string]zoneImage{"img_preview_first": {}, "img_preview_second": {}}
	next, done, _ = t7ZonePieces(dir, "core")
	if err := zoneImages(next, wanted, found); err != nil || len(found) != 3 || found["img_preview_first"].data != nil ||
		!bytes.Equal(found["img_preview_third"].data, third) {
		t.Errorf("the image that was still to find: %v", err)
	}
	done()

	// a delta of another fastfile, one that is cut short, and what is no delta
	other := t7FastfileOf(filler(8, 100), 700, -1)
	whole := t7DeltaOf(fastfile, len(zone), windows...)
	for name, file := range map[string][]byte{"of another fastfile": t7DeltaOf(other, len(zone), windows...),
		"a fastfile": fastfile, "without its windows": whole[:t7DeltaStart+2]} {
		writeFile(t, filepath.Join(dir, "core.fd"), string(file))
		if _, done, err := t7ZonePieces(dir, "core"); err == nil {
			done()
			t.Errorf("a delta %s opened", name)
		}
	}
	for name, windows := range map[string][][]byte{
		"that makes less than its zone": windows[:4],
		"with a window cut short":       {windows[0], windows[1][:40]},
		"that copies past the old zone": {deltaWindow(0x09, len(old), 1, 4, nil, []byte{20}, []byte{0})},
		"with flags in a window":        {slices.Concat([]byte{0x08, 7, 1, 1, 1, 1, 0, 'x', 2}, make([]byte, 4))},
	} {
		writeFile(t, filepath.Join(dir, "core.fd"), string(t7DeltaOf(fastfile, len(zone), windows...)))
		next, done, err := t7ZonePieces(dir, "core")
		if err != nil {
			t.Errorf("a delta %s: %v", name, err)
			continue
		}
		for err == nil {
			_, err = next()
		}
		if done(); err == io.EOF {
			t.Errorf("a delta %s made its zone", name)
		}
	}
	if _, _, err := t7ZonePieces(dir, "none"); !os.IsNotExist(err) {
		t.Errorf("a zone without a fastfile: %v", err)
	}
}
