package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Black Ops II's textures, which the page shows as the T6 maps' pictures (mapart.go). Black Ops II keeps the pixels of
// its textures in image packs, zone/all/*.ipak, which its zones point to. A pack holds whole IWI files (version 27),
// each under the hash of its image's name (ipakHash). The server finds an image by that hash alone, so it reads
// nothing of the zones, which are encrypted.
//
// A pack is a header and its sections, all little-endian. The header has the magic ("KAPI"), the version, the pack's
// size, and the number of sections, 4 bytes each. Each section has 16 bytes: its type, its offset in the pack, its
// size, and its number of items. Section 1 is the index: 16 bytes for each image, which are the hash of the image's
// data, the hash of its name, the offset of its data in section 2, and the size of its data there.
//
// An image's data is in blocks. A block starts at a multiple of 128 bytes of the data, with a header of 128 bytes:
// the number of the block's commands (the top byte) and the offset in the file where the block's output goes (the
// rest), then 31 commands of 4 bytes each, a command's size (the low 3 bytes) and its kind (the top byte). The data of
// the commands comes after the header, one after the other. A command of kind 0 is the file's next bytes as they are,
// one of kind 1 is the next bytes compressed with LZO1X (lzo1x), and one of any other kind is padding to be skipped.
// A block of padding alone gives the offset 0.

// packEntry is where an image is in the packs: the pack, and the offset and the size of its data in the pack.
type packEntry struct {
	file     string
	at, size int64
}

// ipakHash returns the hash of an image's name in the packs: the game's R_HashString. It ignores case.
func ipakHash(name string) uint32 {
	var h uint32
	for i := 0; i < len(name); i++ {
		h = 33*h ^ uint32(name[i]|0x20)
	}
	return h
}

// t6Image returns the decoded texture of the given name from Black Ops II's packs. It returns os.ErrNotExist when the
// packs lack it, or when the server has no Black Ops II. The caller holds h.art.
func (h *web) t6Image(name string) (*image.NRGBA, error) {
	if h.packs == nil {
		h.packs = h.findPacks()
	}
	e, ok := h.packs[ipakHash(name)]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, os.ErrNotExist)
	}
	data, err := e.read()
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", name, filepath.Base(e.file), err)
	}
	return decodeIWI(data)
}

// findPacks reads the index of each of Black Ops II's packs. It returns where each image is, by the hash of its name.
// When two packs have an image, it is the one in a patch's pack (patch_zm.ipak, zm_nuked_patch.ipak), whose zone
// the game loads after the others, else the one in the pack whose name is last. (The lobby's frame of the maps' cards
// is one that patch_zm.ipak changes: zm.ipak's has no band for the card's words.) It is called once.
func (h *web) findPacks() map[uint32]packEntry {
	entries := map[uint32]packEntry{}
	if h.t6Dir == "" {
		return entries
	}
	packs, _ := filepath.Glob(filepath.Join(h.t6Dir, "zone", "all", "*.ipak"))
	patch := func(file string) bool { return strings.Contains(strings.ToLower(filepath.Base(file)), "patch") }
	slices.SortFunc(packs, func(a, b string) int {
		if patch(a) != patch(b) {
			return map[bool]int{true: 1, false: -1}[patch(a)]
		}
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	for _, file := range packs {
		if err := readPackIndex(file, entries); err != nil {
			log.Printf("server: %s: %v", file, err)
		}
	}
	return entries
}

// readPackIndex adds the images of a pack to entries (above).
func readPackIndex(file string, entries map[uint32]packEntry) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 16)
	if _, err := io.ReadFull(f, head); err != nil || string(head[:4]) != "KAPI" {
		return errors.New("not an image pack")
	}
	n := binary.LittleEndian.Uint32(head[12:])
	if n > 64 {
		return fmt.Errorf("an image pack of %d sections", n)
	}
	sections := make([]byte, 16*n)
	if _, err := io.ReadFull(f, sections); err != nil {
		return err
	}
	var index []byte
	data := int64(-1)
	for i := 0; i < int(n); i++ {
		s := sections[16*i:]
		at, count := int64(binary.LittleEndian.Uint32(s[4:])), int64(binary.LittleEndian.Uint32(s[12:]))
		switch binary.LittleEndian.Uint32(s) {
		case 1:
			if count > 1<<20 {
				return fmt.Errorf("an index of %d images", count)
			}
			index = make([]byte, 16*count)
			if _, err := f.ReadAt(index, at); err != nil {
				return err
			}
		case 2:
			data = at
		}
	}
	if index == nil || data < 0 {
		return errors.New("an image pack without its index or its data")
	}
	for i := 0; i < len(index); i += 16 {
		at, size := binary.LittleEndian.Uint32(index[i+8:]), binary.LittleEndian.Uint32(index[i+12:])
		entries[binary.LittleEndian.Uint32(index[i+4:])] = packEntry{file, data + int64(at), int64(size)}
	}
	return nil
}

// packLimit is the most that an image's file in the packs may be. Black Ops II's largest pictures (2048x2048 DXT5)
// are 4 MB.
const packLimit = 64 << 20

// read returns the file of an image in the packs, from its blocks (above).
func (e packEntry) read() ([]byte, error) {
	if e.size > packLimit {
		return nil, fmt.Errorf("%d bytes", e.size)
	}
	f, err := os.Open(e.file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw := make([]byte, e.size)
	if _, err := f.ReadAt(raw, e.at); err != nil {
		return nil, err
	}
	var out []byte
	for at := 0; at+128 <= len(raw); at = (at + 127) &^ 127 {
		head := raw[at : at+128]
		count, offset := int(head[3]), int(binary.LittleEndian.Uint32(head)&0xffffff)
		if count > 31 {
			return nil, fmt.Errorf("a block of %d commands", count)
		}
		at += 128
		start := len(out)
		for i := 0; i < count; i++ {
			c := binary.LittleEndian.Uint32(head[4+4*i:])
			size := int(c & 0xffffff)
			if size > len(raw)-at {
				return nil, errors.New("a command past the end of the image's data")
			}
			switch c >> 24 {
			case 0:
				if len(out)+size > packLimit {
					return nil, errors.New("too large an image")
				}
				out = append(out, raw[at:at+size]...)
			case 1:
				if out, err = lzo1x(out, raw[at:at+size], packLimit); err != nil {
					return nil, err
				}
			}
			at += size
		}
		if len(out) > start && offset != start {
			return nil, fmt.Errorf("a block for %d bytes in, at %d", offset, start)
		}
	}
	return out, nil
}

var errLZO = errors.New("corrupt LZO1X data")

// lzo1x appends what the LZO1X data in decompresses to, as lzo1x_decompress_safe does, to out, and returns it. out
// may hold limit bytes at most. The data is instructions of two kinds: literals, which are copied as they are, and
// matches, which copy what was output a distance back. The data ends with a match that goes back no distance.
//
// A literal run gives how many literals follow. A match gives the distance and the length of its copy, and its low
// two bits (in its first byte that gives the distance) how many literals follow it before the next match: none, in
// which case a literal run or a match comes next. Literals can follow a literal run only through a match. A length
// that does not fit in its bits is 0 there, and is continued in bytes after it: 255 for each 0, and then a byte that
// adds itself.
func lzo1x(out, in []byte, limit int) ([]byte, error) {
	start, ip, failed := len(out), 0, false
	next := func() int {
		if ip >= len(in) {
			failed = true
			return 0
		}
		ip++
		return int(in[ip-1])
	}
	length := func(n int) int {
		for ip < len(in) && in[ip] == 0 {
			n += 255
			ip++
		}
		return n + next()
	}
	literals := func(n int) {
		if n > len(in)-ip || n > limit-len(out) {
			failed = true
			return
		}
		out = append(out, in[ip:ip+n]...)
		ip += n
	}
	match := func(distance, n int) {
		if distance > len(out)-start || n > limit-len(out) {
			failed = true
			return
		}
		for from := len(out) - distance; n > 0; n-- {
			out = append(out, out[from])
			from++
		}
	}
	// The states of the decompression: a literal run or a match comes next (literalRun), a match after a literal
	// run (afterRun), the t literals after a match (afterMatch), or the match whose first byte is t (inMatch).
	const (
		literalRun = iota
		afterRun
		afterMatch
		inMatch
	)
	t, state := 0, literalRun
	if len(in) > 0 && in[0] > 17 { // the data starts with literals: the first byte less 17
		ip, t = 1, int(in[0])-17
		if state = afterMatch; t >= 4 {
			literals(t)
			state = afterRun
		}
	}
	for !failed {
		switch state {
		case literalRun:
			if t, state = next(), inMatch; t >= 16 {
				continue
			}
			if t == 0 {
				t = length(15)
			}
			literals(t + 3)
			state = afterRun
			continue
		case afterRun:
			if t, state = next(), inMatch; t >= 16 {
				continue
			}
			match(1+0x800+t>>2+next()<<2, 3) // 3 bytes from 2 KB back and more
		case afterMatch:
			literals(t)
			t, state = next(), inMatch
			continue
		case inMatch:
			switch {
			case t >= 64: // 3 to 8 bytes from up to 2 KB back
				match(1+(t>>2)&7+next()<<3, t>>5+1)
			case t >= 32: // from up to 16 KB back
				n := t & 31
				if n == 0 {
					n = length(31)
				}
				low := next()
				match(1+low>>2+next()<<6, n+2)
			case t >= 16: // from 16 KB to 48 KB back, or the end
				n := t & 7
				if n == 0 {
					n = length(7)
				}
				low := next()
				distance := (t&8)<<11 + low>>2 + next()<<6
				if distance == 0 && !failed {
					if ip != len(in) {
						return nil, errors.New("LZO1X data that goes on past its end")
					}
					return out, nil
				}
				match(distance+0x4000, n+2)
			default: // 2 bytes from up to 1 KB back
				match(1+t>>2+next()<<2, 2)
			}
		}
		if failed {
			break
		}
		if t = int(in[ip-2]) & 3; t == 0 {
			state = literalRun
		} else {
			state = afterMatch
		}
	}
	return nil, errLZO
}
