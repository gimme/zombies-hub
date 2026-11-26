package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Black Ops III's zones, which have the pictures of the T7 maps in the game's map select (mapart.go). The game shows
// a wide picture of each map there, by the name that its map table gives (t7.go: t7MapList). The pictures are in the
// zones of the game's menus (t7PreviewZones), whole: the game streams none of them from its image packs (xpak.go).
//
// A fastfile (zone/<name>.ff) is a header of 584 bytes and then its zone, in blocks. The header starts with
// "TAff0000". Its byte at 13 says how the blocks are compressed (1: with zlib), and it has the size of the zone at
// 144, in 8 bytes. A block is four numbers of 4 bytes each, and then its data: the size of the data compressed, the
// size of what it decompresses to, the size of the data in the file, and the block's offset in the file. A block
// that decompresses to nothing is padding: the next block starts at the file's next multiple of 8 MB.
//
// An update of the game does not replace a fastfile. It puts a delta beside it (zone/<name>.fd), and the zone that
// the game loads is the fastfile's with the delta applied (t7Delta). So a picture that an update added, as most maps'
// are, is in no fastfile as it is. A delta starts with a header like a fastfile's, with the size of the new zone,
// and then has the header of the fastfile that it is for, as that file has it. Its data is one zlib stream, from
// 1208 on.
//
// The data is a VCDIFF delta (RFC 3284) with the default code table: a header of 5 bytes ("\xd6\xc3\xc4", a version
// of 0, and a byte of flags), and then windows, each of which makes the next 2 MB of the new zone. A window is as RFC
// 3284 has it, but for three things. Its first byte always has the bit 8. With the bit 4, a number follows the
// position of its source segment, which is where the encoder's buffer of the old zone began: the decoder needs it
// for nothing. And 4 bytes follow each window, a checksum that the server does not check.
//
// An image that a zone has whole is a header, the image's name, a 0, and the image's pixels. The header ends before
// the name with, from 72 bytes before it: the image's width and its height, twice (2 bytes each); 32 bytes; the size
// of its pixels (8 bytes); its format, as DXGI numbers it (8 bytes; 98 is BC7); 8 bytes of 0xff; and 8 bytes more. A
// reference to an image, as the map table has, is its name alone. The pictures of the maps have one level, so their
// pixels are their blocks (bc7.go).

// t7PreviewZones are the zones that have the T7 maps' pictures in the game's map select: the zone of the game's
// menus, and the zone of what its updates changed of them, which has The Giant's.
var t7PreviewZones = []string{"core_frontend", "core_frontend_patch"}

// The sizes of a fastfile's header, of a delta's two headers and the numbers after them, and of an image's header in
// a zone (above). zoneImageLimit is the most that an image's pixels may be: a map's picture is 0.7 MB.
const (
	t7HeaderSize    = 584
	t7DeltaStart    = 2*t7HeaderSize + 40
	zoneImageHeader = 72
	zoneImageLimit  = 16 << 20
)

// t7Block is a block of a fastfile: where what it decompresses to is in the zone, where its data is in the file, and
// the sizes of the two.
type t7Block struct {
	off, at      int64
	csize, dsize int
}

// t7Fastfile is an open fastfile, of which any part of the zone can be read (readAt). It keeps the last blocks that
// it decompressed.
type t7Fastfile struct {
	f      *os.File
	head   []byte // its header
	size   int64  // of its zone
	blocks []t7Block
	kept   map[int][]byte // the blocks it keeps, decompressed
	order  []int          // the blocks it keeps, the one read longest ago first
}

// t7BlocksKept is how many blocks an open fastfile keeps decompressed, about 70 MB of them. A window of a delta
// copies from many places in up to 128 MB of the old zone. With 128 blocks kept, the delta of the menus' zone had
// each block decompressed seven times over, and took 17 s to apply. With 300 it takes 5 s, and with every block
// kept, 4 s.
const t7BlocksKept = 300

// openT7Fastfile opens a fastfile and reads where its blocks are.
func openT7Fastfile(file string) (*t7Fastfile, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	ff := &t7Fastfile{f: f, head: make([]byte, t7HeaderSize), kept: map[int][]byte{}}
	if _, err := io.ReadFull(f, ff.head); err != nil || string(ff.head[:8]) != "TAff0000" {
		f.Close()
		return nil, errors.New("not a fastfile of Black Ops III")
	}
	if ff.head[13] != 1 {
		f.Close()
		return nil, fmt.Errorf("a fastfile compressed in way %d, not with zlib (1)", ff.head[13])
	}
	total := int64(binary.LittleEndian.Uint64(ff.head[144:]))
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	head := make([]byte, 16)
	for at := int64(t7HeaderSize); ff.size < total && at+16 <= st.Size(); {
		if _, err := f.ReadAt(head, at); err != nil {
			f.Close()
			return nil, err
		}
		csize, dsize := int64(binary.LittleEndian.Uint32(head)), int64(binary.LittleEndian.Uint32(head[4:]))
		bsize := int64(binary.LittleEndian.Uint32(head[8:]))
		switch {
		case dsize == 0 && csize == 0:
			at = st.Size() // nothing more
		case dsize == 0:
			at = (at + 8<<20) &^ (8<<20 - 1)
		case csize > bsize || dsize > 64<<20 || at+16+bsize > st.Size():
			f.Close()
			return nil, fmt.Errorf("a block of %d bytes for %d, in %d, at %d of %d", csize, dsize, bsize, at, st.Size())
		default:
			ff.blocks = append(ff.blocks, t7Block{ff.size, at + 16, int(csize), int(dsize)})
			ff.size += dsize
			at += 16 + bsize
		}
	}
	if ff.size != total {
		f.Close()
		return nil, fmt.Errorf("a zone of %d bytes in its blocks, and of %d by its header", ff.size, total)
	}
	return ff, nil
}

func (ff *t7Fastfile) close() { ff.f.Close() }

// block returns block i of the zone, decompressed. The slice is the fastfile's own: it is not to be changed.
func (ff *t7Fastfile) block(i int) ([]byte, error) {
	if data, ok := ff.kept[i]; ok {
		if at := slices.Index(ff.order, i); at != len(ff.order)-1 {
			ff.order = append(slices.Delete(ff.order, at, at+1), i)
		}
		return data, nil
	}
	b := ff.blocks[i]
	compressed := make([]byte, b.csize)
	if _, err := ff.f.ReadAt(compressed, b.at); err != nil {
		return nil, err
	}
	r, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	data := make([]byte, b.dsize)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("the block at %d of the zone: %w", b.off, err)
	}
	if len(ff.order) >= t7BlocksKept {
		delete(ff.kept, ff.order[0])
		ff.order = ff.order[1:]
	}
	ff.kept[i], ff.order = data, append(ff.order, i)
	return data, nil
}

// readAt fills p with the zone's bytes from off on.
func (ff *t7Fastfile) readAt(p []byte, off int64) error {
	if off < 0 || off+int64(len(p)) > ff.size {
		return fmt.Errorf("%d bytes at %d of a zone of %d", len(p), off, ff.size)
	}
	i := sort.Search(len(ff.blocks), func(i int) bool { return ff.blocks[i].off+int64(ff.blocks[i].dsize) > off })
	for ; len(p) > 0; i++ {
		data, err := ff.block(i)
		if err != nil {
			return err
		}
		n := copy(p, data[off-ff.blocks[i].off:])
		p, off = p[n:], off+int64(n)
	}
	return nil
}

// pieces returns what reads the zone piece by piece, a block at a time: each call returns the next piece, and io.EOF
// after the last.
func (ff *t7Fastfile) pieces() func() ([]byte, error) {
	i := 0
	return func() ([]byte, error) {
		if i == len(ff.blocks) {
			return nil, io.EOF
		}
		i++
		return ff.block(i - 1)
	}
}

// vcdiffOp is an instruction of a VCDIFF delta: what it does (vcdiffAdd, vcdiffRun or vcdiffCopy, or nothing), the
// size of what it makes, or 0 where the size follows it, and for a copy the mode of its address.
type vcdiffOp struct{ kind, size, mode uint8 }

const (
	vcdiffNone = iota
	vcdiffAdd
	vcdiffRun
	vcdiffCopy
)

// vcdiffCodes is VCDIFF's default code table: the one or two instructions that each byte of a window's instructions
// stands for.
var vcdiffCodes = func() (codes [256][2]vcdiffOp) {
	n := 0
	put := func(first, second vcdiffOp) {
		codes[n] = [2]vcdiffOp{first, second}
		n++
	}
	put(vcdiffOp{kind: vcdiffRun}, vcdiffOp{})
	for size := uint8(0); size <= 17; size++ {
		put(vcdiffOp{kind: vcdiffAdd, size: size}, vcdiffOp{})
	}
	for mode := uint8(0); mode < 9; mode++ {
		put(vcdiffOp{kind: vcdiffCopy, mode: mode}, vcdiffOp{})
		for size := uint8(4); size <= 18; size++ {
			put(vcdiffOp{kind: vcdiffCopy, size: size, mode: mode}, vcdiffOp{})
		}
	}
	for mode := uint8(0); mode < 9; mode++ {
		for add := uint8(1); add <= 4; add++ {
			for size := uint8(4); size <= 6 && (mode < 6 || size == 4); size++ {
				put(vcdiffOp{kind: vcdiffAdd, size: add}, vcdiffOp{kind: vcdiffCopy, size: size, mode: mode})
			}
		}
	}
	for mode := uint8(0); mode < 9; mode++ {
		put(vcdiffOp{kind: vcdiffCopy, size: 4, mode: mode}, vcdiffOp{kind: vcdiffAdd, size: 1})
	}
	return codes
}()

var errVCDIFF = errors.New("a corrupt delta")

// vcdiffInt reads a number of a delta at b[*at:], and moves *at past it. The number is in bytes of 7 bits each, the
// highest first, all but its last with their top bit set. It returns false for a number that is cut short, or is of
// more than 8 bytes.
func vcdiffInt(b []byte, at *int) (int64, bool) {
	var v int64
	for n := 0; *at < len(b) && n < 8; n++ {
		c := b[*at]
		*at++
		if v = v<<7 | int64(c&0x7f); c < 0x80 {
			return v, true
		}
	}
	return 0, false
}

// t7Delta is an open delta of a fastfile (above), which makes the new zone window by window (next).
type t7Delta struct {
	f    *os.File
	z    io.ReadCloser
	r    *bufio.Reader // the delta, decompressed
	old  *t7Fastfile   // the fastfile that the delta is for
	left int64         // how much of the new zone is still to be made
}

// t7WindowLimit is the most that a window of a delta may make, and the most that its delta may be. The game's
// windows make 2 MB each.
const t7WindowLimit = 16 << 20

// openT7Delta opens the delta file for the fastfile old, which must be the one that the delta is for.
func openT7Delta(file string, old *t7Fastfile) (*t7Delta, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	head := make([]byte, t7DeltaStart)
	if _, err := io.ReadFull(f, head); err != nil || string(head[:8]) != "TAff0000" {
		f.Close()
		return nil, errors.New("not a delta of a fastfile of Black Ops III")
	}
	if head[13] != 6 {
		f.Close()
		return nil, fmt.Errorf("a delta compressed in way %d, not with zlib (6)", head[13])
	}
	if !bytes.Equal(head[t7HeaderSize:2*t7HeaderSize], old.head) {
		f.Close()
		return nil, errors.New("a delta of another version of its fastfile")
	}
	z, err := zlib.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		f.Close()
		return nil, err
	}
	d := &t7Delta{f: f, z: z, r: bufio.NewReaderSize(z, 1<<20), old: old,
		left: int64(binary.LittleEndian.Uint64(head[144:]))}
	start := make([]byte, 5)
	if _, err := io.ReadFull(d.r, start); err != nil || string(start[:4]) != "\xd6\xc3\xc4\x00" || start[4]&3 != 0 {
		d.close()
		return nil, fmt.Errorf("a delta that starts with % x, not a VCDIFF delta with the default code table", start)
	}
	return d, nil
}

func (d *t7Delta) close() {
	d.z.Close()
	d.f.Close()
}

// next makes the next window of the new zone (above), and returns io.EOF after the last. A window is its indicator,
// the size and the position of its source segment in the old zone when the indicator has the bit 1, the size of its
// delta, and its delta: the size of what it makes, a byte of flags, the sizes of its three sections, and the
// sections. They are the data that it adds, its instructions, and the addresses that it copies from.
func (d *t7Delta) next() ([]byte, error) {
	if d.left == 0 {
		return nil, io.EOF
	}
	number := func() (int64, error) {
		var v int64
		for n := 0; n < 8; n++ {
			c, err := d.r.ReadByte()
			if err != nil {
				return 0, errVCDIFF
			}
			if v = v<<7 | int64(c&0x7f); c < 0x80 {
				return v, nil
			}
		}
		return 0, errVCDIFF
	}
	indicator, err := d.r.ReadByte()
	if err != nil || indicator&2 != 0 {
		return nil, errVCDIFF
	}
	var segment, position int64
	if indicator&1 != 0 {
		if segment, err = number(); err != nil || segment > d.old.size {
			return nil, errVCDIFF
		}
		if position, err = number(); err != nil || position > d.old.size-segment {
			return nil, errVCDIFF
		}
	}
	if indicator&4 != 0 {
		if _, err := number(); err != nil { // where the encoder's buffer of the old zone began
			return nil, errVCDIFF
		}
	}
	size, err := number()
	if err != nil || size > t7WindowLimit {
		return nil, errVCDIFF
	}
	delta := make([]byte, size+4) // and the window's checksum
	if _, err := io.ReadFull(d.r, delta); err != nil {
		return nil, errVCDIFF
	}
	delta = delta[:size]
	at := 0
	makes, ok := vcdiffInt(delta, &at)
	if !ok || at >= len(delta) || delta[at] != 0 || makes > t7WindowLimit || makes > d.left {
		return nil, errVCDIFF
	}
	at++
	var sections [3][]byte
	sizes := [3]int64{}
	for i := range sizes {
		if sizes[i], ok = vcdiffInt(delta, &at); !ok {
			return nil, errVCDIFF
		}
	}
	for i, n := range sizes {
		if n > int64(len(delta)-at) {
			return nil, errVCDIFF
		}
		sections[i], at = delta[at:at+int(n)], at+int(n)
	}
	if at != len(delta) {
		return nil, errVCDIFF
	}
	out, err := vcdiffWindow(sections[0], sections[1], sections[2], segment, int(makes), func(p []byte, off int64) error {
		return d.old.readAt(p, position+off)
	})
	if err != nil {
		return nil, err
	}
	d.left -= makes
	return out, nil
}

// vcdiffWindow makes a window of makes bytes from its three sections: the data that it adds, its instructions, and
// the addresses that it copies from. An address is in the window's source segment, of the given size, which source
// reads, or after it, in what the window has made so far. The modes of an address are as RFC 3284 has them: from the
// start (0), back from here (1), on from one of the four addresses last used (2 to 5), or one of the 768 addresses
// that the three modes after those keep, by a byte.
func vcdiffWindow(data, instructions, addresses []byte, segment int64, makes int, source func(p []byte, off int64) error) ([]byte, error) {
	out := make([]byte, 0, makes)
	var near [4]int64
	var same [768]int64
	slot, at, addr := 0, 0, 0
	for at < len(instructions) {
		code := instructions[at]
		at++
		for _, op := range vcdiffCodes[code] {
			if op.kind == vcdiffNone {
				continue
			}
			size := int64(op.size)
			if size == 0 {
				var ok bool
				if size, ok = vcdiffInt(instructions, &at); !ok {
					return nil, errVCDIFF
				}
			}
			if size > int64(makes-len(out)) {
				return nil, errVCDIFF
			}
			switch op.kind {
			case vcdiffAdd:
				if size > int64(len(data)) {
					return nil, errVCDIFF
				}
				out, data = append(out, data[:size]...), data[size:]
			case vcdiffRun:
				if len(data) == 0 {
					return nil, errVCDIFF
				}
				for ; size > 0; size-- {
					out = append(out, data[0])
				}
				data = data[1:]
			case vcdiffCopy:
				here := segment + int64(len(out))
				var from int64
				if op.mode < 6 {
					v, ok := vcdiffInt(addresses, &addr)
					if !ok {
						return nil, errVCDIFF
					}
					switch from = v; {
					case op.mode == 1:
						from = here - v
					case op.mode > 1:
						from = near[op.mode-2] + v
					}
				} else {
					if addr >= len(addresses) {
						return nil, errVCDIFF
					}
					from = same[int(op.mode-6)*256+int(addresses[addr])]
					addr++
				}
				if from < 0 || from >= here {
					return nil, errVCDIFF
				}
				near[slot], slot, same[from%768] = from, (slot+1)%4, from
				// from the source segment, and then from what the window has made, which may be what the copy itself
				// makes
				if n := min(size, segment-from); n > 0 {
					out = out[:len(out)+int(n)]
					if err := source(out[len(out)-int(n):], from); err != nil {
						return nil, err
					}
					from, size = from+n, size-n
				}
				for size > 0 {
					t := int(from - segment)
					n := min(size, int64(len(out)-t))
					out = append(out, out[t:t+int(n)]...)
					from, size = from+n, size-n
				}
			}
		}
	}
	if len(out) != makes || len(data) != 0 || addr != len(addresses) {
		return nil, errVCDIFF
	}
	return out, nil
}

// t7ZonePieces opens the zone of the given name in the zone folder of a Black Ops III install: its fastfile, with its
// delta applied when it has one. It returns what reads the zone piece by piece, and what closes the files.
func t7ZonePieces(zoneDir, name string) (next func() ([]byte, error), done func(), err error) {
	ff, err := openT7Fastfile(filepath.Join(zoneDir, name+".ff"))
	if err != nil {
		return nil, nil, err
	}
	delta, err := openT7Delta(filepath.Join(zoneDir, name+".fd"), ff)
	if errors.Is(err, os.ErrNotExist) {
		return ff.pieces(), ff.close, nil
	}
	if err != nil {
		ff.close()
		return nil, nil, fmt.Errorf("%s.fd: %w", name, err)
	}
	return delta.next, func() {
		delta.close()
		ff.close()
	}, nil
}

// zoneImage is an image that a zone has whole: its size in texels, its format as DXGI numbers it, and its pixels.
type zoneImage struct {
	w, h   int
	format uint64
	data   []byte
}

// zoneImages reads a zone piece by piece, with next, and adds to found the images of the wanted names that the zone
// has whole (above), each the first time the zone has it. It stops once it has found them all. The names start
// alike, and it looks for what they start with.
func zoneImages(next func() ([]byte, error), wanted map[string]bool, found map[string]zoneImage) error {
	prefix, longest := "", 0
	for name := range wanted {
		if _, has := found[name]; has {
			continue
		}
		if longest == 0 {
			prefix = name
		}
		for !bytes.HasPrefix([]byte(name), []byte(prefix)) {
			prefix = prefix[:len(prefix)-1]
		}
		longest = max(longest, len(name))
	}
	if prefix == "" {
		return nil // nothing to find, or names that start with nothing alike
	}
	lacks := func() bool {
		for name := range wanted {
			if _, has := found[name]; !has {
				return true
			}
		}
		return false
	}
	// kept is the end of the zone so far: enough of it for a name that the next piece ends, and the header before it.
	// buf is that and the piece. filling is the image whose pixels the next pieces go on with, when there is one.
	var kept, buf []byte
	var filling struct {
		name string
		img  zoneImage
		need int
	}
	for lacks() {
		piece, err := next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		buf = append(append(buf[:0], kept...), piece...)
		from := 0 // a name that ends in this piece may start in what was kept
		if filling.need > 0 {
			n := min(filling.need, len(piece))
			filling.img.data, filling.need = append(filling.img.data, piece[:n]...), filling.need-n
			if filling.need == 0 {
				found[filling.name] = filling.img
			}
			from = len(kept) + n // and none starts in an image's pixels
		}
		for from < len(buf) {
			i := bytes.Index(buf[from:], []byte(prefix))
			if i < 0 {
				break
			}
			s := from + i
			from = s + 1
			end := bytes.IndexByte(buf[s:min(s+longest+1, len(buf))], 0)
			// a name that the piece cuts short is found with the next piece, and one that ended before this piece
			// was found then
			if end < 0 || s+end < len(kept) || s < zoneImageHeader {
				continue
			}
			name, head := string(buf[s:s+end]), buf[s-zoneImageHeader:s]
			w, h := int(binary.LittleEndian.Uint16(head)), int(binary.LittleEndian.Uint16(head[2:]))
			size := binary.LittleEndian.Uint64(head[40:])
			if _, has := found[name]; !wanted[name] || has || w == 0 || h == 0 || size == 0 || size > zoneImageLimit ||
				!bytes.Equal(head[:4], head[4:8]) || !bytes.Equal(head[56:64], bytes.Repeat([]byte{0xff}, 8)) {
				continue
			}
			img := zoneImage{w, h, binary.LittleEndian.Uint64(head[48:]), make([]byte, 0, size)}
			pixels := buf[s+end+1:]
			n := min(int(size), len(pixels))
			img.data = append(img.data, pixels[:n]...)
			if from = s + end + 1 + n; n < int(size) {
				filling.name, filling.img, filling.need = name, img, int(size)-n
			} else {
				found[name] = img
			}
		}
		kept = append(kept[:0], buf[max(len(buf)-zoneImageHeader-longest-1, 0):]...)
	}
	return nil
}

// The size of a Black Ops III map's preview (t7PreviewOf): the game's pictures are 1280x540 or 1148x484.
const t7PreviewWidth, t7PreviewHeight = 640, 270

// t7Preview returns a Black Ops III map's preview: its picture in the game's map select, of the given name, as the
// page shows it (t7PreviewOf). It returns false when the game's zones lack the picture, or when the server has none
// of those zones. The first call reads the zones for the pictures of all the T7 maps, which takes some seconds: the
// server makes it as it starts (main.go). The previews have their own lock, so that no other picture waits for them
// (h.art).
func (h *web) t7Preview(name string) (mapPicture, bool) {
	h.t7Once.Do(func() { h.t7Previews = findT7Previews(h.t7Dir) })
	pic, ok := h.t7Previews[name]
	return pic, ok
}

// findT7Previews reads the pictures of the T7 maps in the game's map select from the zones that have them
// (t7PreviewZones), in the zone folder of the Black Ops III install dir, and returns each as the page shows it, by
// its name. It finds none where dir is "". It logs the pictures that the zones lack. It is called once.
func findT7Previews(dir string) map[string]mapPicture {
	previews := map[string]mapPicture{}
	if dir == "" {
		return previews
	}
	wanted, found := map[string]bool{}, map[string]zoneImage{}
	for _, m := range t7MapList {
		if m.preview != "" {
			wanted[m.preview] = true
		}
	}
	for _, zone := range t7PreviewZones {
		next, done, err := t7ZonePieces(filepath.Join(dir, "zone"), zone)
		if err == nil {
			err = zoneImages(next, wanted, found)
			done()
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("server: %s: %v", filepath.Join(dir, "zone", zone+".ff"), err)
		}
	}
	for name, img := range found {
		pic, err := t7PreviewOf(img)
		if err != nil {
			log.Printf("server: %s: %v", name, err)
			continue
		}
		previews[name] = pic
	}
	var lack []string
	for _, m := range t7MapList {
		if _, ok := previews[m.preview]; !ok && m.preview != "" {
			lack = append(lack, m.title)
		}
	}
	if len(lack) > 0 {
		log.Printf("server: the zones of Black Ops III's menus in %s lack the pictures of %d of its %d maps: %s",
			filepath.Join(dir, "zone"), len(lack), len(t7MapList), strings.Join(lack, ", "))
	}
	return previews
}

// t7PreviewOf returns a Black Ops III map's picture in the game's map select as the page shows it, from the image
// that a zone has of it: a JPEG of 640x270. The picture is in BC7 (98 as DXGI numbers it, or 99 for sRGB) and has
// one level. It is opaque, whatever alpha its texture has (t7Screen).
func t7PreviewOf(img zoneImage) (mapPicture, error) {
	if img.format != 98 && img.format != 99 || len(img.data) != (img.w+3)/4*((img.h+3)/4)*16 {
		return mapPicture{}, fmt.Errorf("an image of %dx%d in format %d, of %d bytes", img.w, img.h, img.format, len(img.data))
	}
	decoded, err := decodeBC7(img.data, img.w, img.h)
	if err != nil {
		return mapPicture{}, err
	}
	return jpegOf(scaled(opaque(decoded), decoded.Bounds(), t7PreviewWidth, t7PreviewHeight))
}
