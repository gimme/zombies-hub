package main

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
)

// bc7Block is a BC7 block of a mode: its partition, its rotation and its choice of index, each endpoint's red, green,
// blue and alpha as the block has them, the lowest bits that the mode has (one for each endpoint or for each subset),
// and each texel's index, and its second index in a mode that has one. The bits go from the first byte's lowest on.
// A block is 128 bits, whatever its mode.
func bc7Block(t *testing.T, mode, partition, rotation, choice int, ends [][4]int, low, index, index2 []int) []byte {
	t.Helper()
	var block [16]byte
	at := 0
	put := func(v, bits int) {
		for i := 0; i < bits; i++ {
			block[at/8] |= byte(v>>i&1) << (at % 8)
			at++
		}
	}
	m := bc7Modes[mode]
	put(1<<mode, mode+1)
	put(partition, m.partition)
	put(rotation, m.rotation)
	put(choice, m.choice)
	for c := 0; c < 3; c++ {
		for _, e := range ends {
			put(e[c], m.colour)
		}
	}
	for _, e := range ends {
		put(e[3], m.alpha)
	}
	for _, bit := range low {
		put(bit, 1)
	}
	anchor := func(i int) bool {
		if i == 0 {
			return true
		}
		for s := 1; s < m.subsets; s++ {
			if i == int(bc7Anchors[m.subsets][s-1][partition]) {
				return true
			}
		}
		return false
	}
	for i, v := range index {
		bits := m.index
		if anchor(i) {
			bits--
		}
		put(v, bits)
	}
	for i, v := range index2 {
		bits := m.index2
		if i == 0 {
			bits--
		}
		put(v, bits)
	}
	if at != 128 {
		t.Fatalf("a block of mode %d of %d bits", mode, at)
	}
	return block[:]
}

// texels is 16 indices: 0 but for those given, each a texel and its index.
func texels(set ...int) []int {
	index := make([]int, 16)
	for i := 0; i+1 < len(set); i += 2 {
		index[set[i]] = set[i+1]
	}
	return index
}

// The tables of BC7's partitions: every texel has a subset that the block has, the first texel's is the first, and
// each other subset's anchor is one of its texels.
func TestBC7Tables(t *testing.T) {
	for subsets := 2; subsets <= 3; subsets++ {
		for p, texels := range bc7Partitions[subsets] {
			if len(texels) != 16 || texels[0] != '0' || strings.Trim(texels, "012"[:subsets]) != "" {
				t.Errorf("partition %d of %d subsets: %q", p, subsets, texels)
				continue
			}
			for s := 1; s < subsets; s++ {
				if a := bc7Anchors[subsets][s-1][p]; a > 15 || int(texels[a]-'0') != s {
					t.Errorf("partition %d of %d subsets: subset %d's anchor is texel %d, of %q", p, subsets, s, a, texels)
				}
			}
		}
	}
}

// A block of each of BC7's modes: its endpoints widened to 8 bits with their lowest bits, its texels between them by
// their indices, its subsets by its partition, an anchor's index a bit shorter, and its alpha swapped with a channel.
func TestDecodeBC7(t *testing.T) {
	white, black := [4]int{127, 127, 127}, [4]int{}
	for _, c := range []struct {
		name  string
		block []byte
		want  map[int]color.NRGBA
	}{
		// one subset, 7 bits a channel and a lowest bit for each endpoint, indices of 4 bits
		{"mode 6", bc7Block(t, 6, 0, 0, 0, [][4]int{{127, 0, 64, 127}, {0, 127, 32, 0}}, []int{1, 0}, texels(1, 15, 2, 8), nil),
			map[int]color.NRGBA{0: {255, 1, 129, 255}, 1: {0, 254, 64, 0}, 2: {120, 135, 94, 120}, 15: {255, 1, 129, 255}}},
		// an index for the colour and one for the alpha, and the alpha swapped with the green
		{"mode 5", bc7Block(t, 5, 0, 2, 0, [][4]int{{127, 0, 0, 10}, {127, 0, 0, 200}}, nil, texels(), texels(1, 3, 2, 1)),
			map[int]color.NRGBA{0: {255, 10, 0, 0}, 1: {255, 200, 0, 0}, 2: {255, 72, 0, 0}}},
		// the index of 2 bits is the colour's, and the alpha is swapped with the red
		{"mode 4", bc7Block(t, 4, 0, 1, 0, [][4]int{{31, 0, 0, 63}, {0, 31, 0, 0}}, nil, texels(1, 3), texels()),
			map[int]color.NRGBA{0: {255, 0, 0, 255}, 1: {255, 255, 0, 0}}},
		// the index of 3 bits is the colour's, and the one of 2 bits the alpha's
		{"mode 4, the other index", bc7Block(t, 4, 0, 0, 1, [][4]int{{31, 0, 0, 63}, {0, 31, 0, 0}}, nil, texels(1, 3), texels(2, 7)),
			map[int]color.NRGBA{0: {255, 0, 0, 255}, 1: {255, 0, 0, 0}, 2: {0, 255, 0, 255}}},
		// two subsets, the upper and the lower half, with a lowest bit each
		{"mode 1", bc7Block(t, 1, 13, 0, 0, [][4]int{{63, 0, 0}, {0, 0, 63}, {0, 63, 0}, {63, 63, 63}}, []int{1, 0},
			texels(3, 7, 12, 7), nil),
			map[int]color.NRGBA{0: {255, 2, 2, 255}, 3: {2, 2, 255, 255}, 8: {0, 253, 0, 255}, 12: {253, 253, 253, 255},
				15: {0, 253, 0, 255}}},
		// two subsets, the left and the right half; the last texel is the second's anchor, with an index of one bit
		{"mode 3", bc7Block(t, 3, 0, 0, 0, [][4]int{white, black, {127, 0, 0}, {0, 0, 127}}, []int{1, 0, 1, 0},
			texels(1, 3, 2, 3, 15, 1), nil),
			map[int]color.NRGBA{0: {255, 255, 255, 255}, 1: {0, 0, 0, 255}, 2: {0, 0, 254, 255}, 3: {255, 1, 1, 255},
				15: {171, 1, 84, 255}}},
		// two subsets with alpha
		{"mode 7", bc7Block(t, 7, 13, 0, 0, [][4]int{{31, 31, 31, 31}, {}, {31, 0, 0, 15}, {31, 0, 0, 15}}, []int{1, 0, 0, 0},
			texels(1, 3), nil),
			map[int]color.NRGBA{0: {255, 255, 255, 255}, 1: {}, 8: {251, 0, 0, 121}, 15: {251, 0, 0, 121}}},
		// three subsets: the upper half and the two rows under it, in one of 16 partitions
		{"mode 0", bc7Block(t, 0, 8, 0, 0, [][4]int{{15, 0, 0}, {15, 0, 0}, {0, 15, 0}, {0, 15, 0}, {0, 0, 15}, {0, 0, 15}},
			[]int{1, 1, 0, 0, 1, 1}, texels(), nil),
			map[int]color.NRGBA{0: {255, 8, 8, 255}, 7: {255, 8, 8, 255}, 8: {0, 247, 0, 255}, 12: {8, 8, 255, 255}}},
		// three subsets whose anchors are texels 10 and 8
		{"mode 2", bc7Block(t, 2, 23, 0, 0, [][4]int{{31, 31, 31}, {}, {31, 0, 0}, {0, 31, 0}, {0, 0, 31}, {31, 31, 0}}, nil,
			texels(1, 3, 4, 3, 8, 1, 9, 3, 13, 3, 14, 3, 15, 3), nil),
			map[int]color.NRGBA{0: {255, 255, 255, 255}, 1: {0, 0, 0, 255}, 4: {0, 255, 0, 255}, 5: {255, 0, 0, 255},
				8: {84, 84, 171, 255}, 9: {255, 255, 0, 255}, 10: {255, 0, 0, 255}, 11: {255, 255, 255, 255},
				13: {255, 255, 0, 255}, 14: {0, 255, 0, 255}, 15: {0, 0, 0, 255}}},
		{"no mode", make([]byte, 16), map[int]color.NRGBA{0: {}, 15: {}}},
	} {
		var px [16]color.NRGBA
		decodeBC7Block(c.block, &px)
		for i, want := range c.want {
			if px[i] != want {
				t.Errorf("%s: texel %d is %v, want %v", c.name, i, px[i], want)
			}
		}
	}

	// a picture: its blocks row by row, and no more of its last blocks than it is wide and high
	red := bc7Block(t, 6, 0, 0, 0, [][4]int{{127, 0, 0, 127}, {127, 0, 0, 127}}, []int{1, 1}, texels(), nil)
	blue := bc7Block(t, 6, 0, 0, 0, [][4]int{{0, 0, 127, 127}, {0, 0, 127, 127}}, []int{1, 1}, texels(), nil)
	img, err := decodeBC7(joined(red, blue, blue, red), 6, 5)
	if err != nil || img.Bounds() != image.Rect(0, 0, 6, 5) {
		t.Fatalf("a picture of 6x5: %v, %v", img, err)
	}
	for at, want := range map[image.Point]color.NRGBA{{0, 0}: {255, 1, 1, 255}, {3, 3}: {255, 1, 1, 255}, {4, 0}: {1, 1, 255, 255},
		{0, 4}: {1, 1, 255, 255}, {5, 4}: {255, 1, 1, 255}} {
		if c := img.NRGBAAt(at.X, at.Y); c != want {
			t.Errorf("a picture of 6x5 at %v: %v, want %v", at, c, want)
		}
	}
	if _, err := decodeBC7(joined(red, blue, blue), 6, 5); err == nil {
		t.Errorf("a picture of four blocks decoded from three")
	}
	if _, err := decodeBC7(nil, 0, 4); err == nil {
		t.Errorf("a picture of no width decoded")
	}
}

// joined returns the byte slices one after another.
func joined(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}
