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
	"strconv"
	"strings"
)

// Black Ops III's textures, of which the page shows the T7 maps' loading screens (mapart.go). Black Ops III keeps the
// pixels of the textures that it streams in image packs, zone/*.xpak. The pack of a zone (core_patch.xpak, of core_patch.ff)
// lists the images that the zone streams, each by its name and under a key. An image's pixels are under its key in
// whichever pack has them: mostly a pack that every mode loads (base.xpak, initial.xpak), or one of an update of the
// game (base_patch_tu21.xpak). The game's map table, in the zone core_patch, names each map's loading screen
// (t7.go: t7MapList). So the server finds an image by its name in that zone's pack (t7Listing), and its pixels by its
// key in the others. It reads nothing of the zones.
//
// A pack is a header, the data of its items, and tables, all little-endian. The header has the magic ("KAPI") and, 8
// bytes each: at 32 the offset of the data; at 48 and 56 the number of items whose data the pack has, and the offset
// of the table of where they are; at 96, 104 and 112 the number of items the pack lists, and the offset and the size
// of its list. The table of where the items are has 24 bytes for each: its key, the offset of its data in the pack's
// data, and the size of its data there.
//
// The list has for each item its key and the length of its text, 8 bytes each, and then the text: lines of
// "<field>: <value>". An image's fields are its type (image), its name, its format (BC7), its size in texels, and the
// offset and the size of its pixels in its data (offset0 and size0). The field called width is the image's height,
// and the one called height its width. A loading screen is in BC7 and has one level, so its pixels are its blocks
// (bc7.go), 16 bytes each. The server reads no other image.
//
// An item's data is in blocks, as an image's is in Black Ops II's packs (ipak.go). A block starts at a multiple of 128
// bytes of the data, with a header of 128 bytes: the number of the block's commands and the offset in the item where
// the block's output goes, 4 bytes each, and then 30 commands of 4 bytes each, a command's size (the low 3 bytes) and
// its kind (the top byte). The data of the commands comes after the header, one after the other. A command of kind 0
// is the item's next bytes as they are, one of kind 3 is the next bytes compressed with LZ4 (lz4), and one of any
// other kind is padding to be skipped.

// t7Listing is the pack that lists the T7 maps' loading screens (above), in the game's folder zone.
const t7Listing = "core_patch.xpak"

// xpakImage is an image as a pack lists it: its key, its size in texels, its format, and where its largest level is
// in its data.
type xpakImage struct {
	key      uint64
	w, h     int
	format   string
	at, size int
}

// xpakItem is where an item's data is: the pack, and the offset and the size of the data in the pack.
type xpakItem struct {
	file     string
	at, size int64
}

// xpaks is what the server knows of Black Ops III's packs: the images that t7Listing lists, by name, and where the
// data of each is, by its key.
type xpaks struct {
	images map[string]xpakImage
	items  map[uint64]xpakItem
}

// t7Image returns the decoded texture of the given name from Black Ops III's packs. It returns os.ErrNotExist when the
// packs lack it, or when the server has no packs of Black Ops III. The caller holds h.art.
func (h *web) t7Image(name string) (*image.NRGBA, error) {
	if h.xpaks == nil {
		h.xpaks = findXpaks(h.t7Dir)
	}
	img, ok := h.xpaks.images[name]
	item, has := h.xpaks.items[img.key]
	if !ok || !has {
		return nil, fmt.Errorf("%s: %w", name, os.ErrNotExist)
	}
	if !strings.HasPrefix(img.format, "BC7") || img.size != (img.w+3)/4*((img.h+3)/4)*16 {
		return nil, fmt.Errorf("%s in %s: an image of %dx%d in format %s, of %d bytes", name, filepath.Base(item.file),
			img.w, img.h, img.format, img.size)
	}
	data, err := item.read()
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", name, filepath.Base(item.file), err)
	}
	if img.at < 0 || img.size < 0 || img.at > len(data) || img.size > len(data)-img.at {
		return nil, fmt.Errorf("%s in %s: %d bytes at %d of %d", name, filepath.Base(item.file), img.size, img.at, len(data))
	}
	decoded, err := decodeBC7(data[img.at:img.at+img.size], img.w, img.h)
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", name, filepath.Base(item.file), err)
	}
	return decoded, nil
}

// findXpaks reads the list of t7Listing, in the zone folder of the Black Ops III install dir, and finds the data of
// the images it lists in the folder's packs. It finds nothing where dir is "" or has no such pack. Two packs that
// have data under one key have the same data. It is called once.
func findXpaks(dir string) *xpaks {
	found := &xpaks{images: map[string]xpakImage{}, items: map[uint64]xpakItem{}}
	if dir == "" {
		return found
	}
	zone := filepath.Join(dir, "zone")
	images, err := xpakImages(filepath.Join(zone, t7Listing))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("server: %s: %v", filepath.Join(zone, t7Listing), err)
		}
		return found
	}
	found.images = images
	wanted := map[uint64]bool{}
	for _, img := range images {
		wanted[img.key] = true
	}
	packs, _ := filepath.Glob(filepath.Join(zone, "*.xpak"))
	slices.Sort(packs)
	for _, file := range packs {
		if err := xpakItems(file, wanted, found.items); err != nil {
			log.Printf("server: %s: %v", file, err)
		}
	}
	return found
}

// xpakHeader reads a pack's header (above). It returns the offset of the pack's data, the number of items whose data
// it has and the offset of the table of where they are, and the offset and the size of its list.
func xpakHeader(f *os.File) (data, items, table, list, listSize int64, err error) {
	head := make([]byte, 120)
	if _, err := io.ReadFull(f, head); err != nil || string(head[:4]) != "KAPI" {
		return 0, 0, 0, 0, 0, errors.New("not an image pack")
	}
	at := func(i int) int64 { return int64(binary.LittleEndian.Uint64(head[i:])) }
	data, items, table, list, listSize = at(32), at(48), at(56), at(104), at(112)
	if data < 0 || items < 0 || items > 1<<20 || table < 0 || list < 0 || listSize < 0 || listSize > packLimit {
		return 0, 0, 0, 0, 0, errors.New("an image pack with a header out of range")
	}
	return data, items, table, list, listSize, nil
}

// xpakImages returns the images that a pack lists (above), by name.
func xpakImages(file string) (map[string]xpakImage, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, _, _, at, size, err := xpakHeader(f)
	if err != nil {
		return nil, err
	}
	list := make([]byte, size)
	if _, err := f.ReadAt(list, at); err != nil {
		return nil, err
	}
	images := map[string]xpakImage{}
	for len(list) >= 16 {
		key, n := binary.LittleEndian.Uint64(list), binary.LittleEndian.Uint64(list[8:])
		if n > uint64(len(list)-16) {
			return nil, errors.New("a list that ends in an item")
		}
		fields := map[string]string{}
		for _, line := range strings.Split(string(list[16:16+n]), "\n") {
			if name, value, ok := strings.Cut(line, ": "); ok {
				fields[name] = value
			}
		}
		list = list[16+n:]
		// the field called width is the height
		w, _ := strconv.Atoi(fields["height"])
		h, _ := strconv.Atoi(fields["width"])
		offset, err := strconv.Atoi(fields["offset0"])
		length, err2 := strconv.Atoi(fields["size0"])
		if fields["type"] != "image" || fields["name"] == "" || w < 1 || h < 1 || w > 4096 || h > 4096 || err != nil || err2 != nil {
			continue
		}
		images[fields["name"]] = xpakImage{key, w, h, fields["format"], offset, length}
	}
	return images, nil
}

// xpakItems adds to items where a pack has the data of each of the wanted keys.
func xpakItems(file string, wanted map[uint64]bool, items map[uint64]xpakItem) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	data, n, at, _, _, err := xpakHeader(f)
	if err != nil {
		return err
	}
	table := make([]byte, 24*n)
	if _, err := f.ReadAt(table, at); err != nil {
		return err
	}
	for i := 0; i < len(table); i += 24 {
		key := binary.LittleEndian.Uint64(table[i:])
		if _, had := items[key]; wanted[key] && !had {
			items[key] = xpakItem{file, data + int64(binary.LittleEndian.Uint64(table[i+8:])),
				int64(binary.LittleEndian.Uint64(table[i+16:]))}
		}
	}
	return nil
}

// read returns an item's data, from its blocks (above). It may be as large as an image in Black Ops II's packs
// (packLimit): Black Ops III's largest pictures (2048x2048 BC7) are 4 MB.
func (e xpakItem) read() ([]byte, error) {
	if e.size < 0 || e.size > packLimit {
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
		count, offset := int(binary.LittleEndian.Uint32(head)), int(binary.LittleEndian.Uint32(head[4:]))
		if count > 30 {
			return nil, fmt.Errorf("a block of %d commands", count)
		}
		at += 128
		start := len(out)
		for i := 0; i < count; i++ {
			c := binary.LittleEndian.Uint32(head[8+4*i:])
			size := int(c & 0xffffff)
			if size > len(raw)-at {
				return nil, errors.New("a command past the end of the item's data")
			}
			switch c >> 24 {
			case 0:
				if len(out)+size > packLimit {
					return nil, errors.New("too large an item")
				}
				out = append(out, raw[at:at+size]...)
			case 3:
				if out, err = lz4(out, raw[at:at+size], packLimit); err != nil {
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

var errLZ4 = errors.New("corrupt LZ4 data")

// lz4 appends what the LZ4 block in decompresses to, to out, and returns it. out may hold limit bytes at most. The
// block is sequences: each is literals, which are copied as they are, and then a match, which copies what the block
// output a distance back. A sequence starts with a byte whose high four bits are the number of literals and whose low
// four bits are the length of the match, less 4. A number of 15 is continued in bytes after it, each of which adds
// itself, up to the first that is not 255. The match's distance is two bytes, after the literals. The block ends with
// a sequence of literals alone.
func lz4(out, in []byte, limit int) ([]byte, error) {
	start := len(out)
	for ip := 0; ip < len(in); {
		token := int(in[ip])
		ip++
		// more returns n, continued in the bytes after it when it is 15
		more := func(n int) (int, bool) {
			for add := 255; n >= 15 && add == 255; n += add {
				if ip >= len(in) {
					return 0, false
				}
				add = int(in[ip])
				ip++
			}
			return n, true
		}
		literals, ok := more(token >> 4)
		if !ok || literals > len(in)-ip || literals > limit-len(out) {
			return nil, errLZ4
		}
		out = append(out, in[ip:ip+literals]...)
		if ip += literals; ip == len(in) {
			return out, nil
		}
		if len(in)-ip < 2 {
			return nil, errLZ4
		}
		distance := int(binary.LittleEndian.Uint16(in[ip:]))
		ip += 2
		n, ok := more(token & 15)
		if n += 4; !ok || distance == 0 || distance > len(out)-start || n > limit-len(out) {
			return nil, errLZ4
		}
		for from := len(out) - distance; n > 0; n-- {
			out = append(out, out[from])
			from++
		}
	}
	return nil, errLZ4 // no sequence of literals alone at its end
}
