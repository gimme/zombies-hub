package main

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
)

// BC7, the compression of Black Ops III's pictures (xpak.go). A picture is blocks of 4x4 texels, 16 bytes each, row
// by row. A block's bits are read from its first byte's lowest bit on. It is in one of eight modes: the mode is the
// number of 0 bits before the block's first 1 bit.
//
// A block has one to three subsets of its texels, each with two endpoints, which are colours. A texel's colour is
// between the two endpoints of its subset, as far along as the texel's index says (bc7Weights). The mode says how
// many subsets there are, and the block which of 64 partitions of its texels they are (bc7Partitions). After the mode
// and the partition, the block has the endpoints, channel by channel (all the reds, then the greens, the blues and
// the alphas), then the endpoints' lowest bits where the mode has them (one for each endpoint, or one for each
// subset), and then the indices. Each subset has an anchor, a texel whose index has one bit less, because its top
// bit is always 0: the block's first texel, and for the other subsets a texel that the partition gives (bc7Anchors).
//
// Modes 4 and 5 have one subset, and two indices for each texel: one for the colour and one for the alpha. In mode 4
// the two have 2 and 3 bits, and a bit of the block says which is the colour's. Both modes can swap the alpha with one
// of the colour's channels (the block's rotation), after the texel's colour is worked out.

// bc7Mode is what a mode's blocks have: the number of subsets, the bits of the partition, of the rotation and of the
// choice of index, the bits of each endpoint's colour channels and of its alpha, whether each endpoint or each subset
// has a lowest bit of its own, and the bits of a texel's index and of its second index.
type bc7Mode struct {
	subsets, partition, rotation, choice, colour, alpha int
	endpointBit, subsetBit                              bool
	index, index2                                       int
}

var bc7Modes = [8]bc7Mode{
	{subsets: 3, partition: 4, colour: 4, endpointBit: true, index: 3},
	{subsets: 2, partition: 6, colour: 6, subsetBit: true, index: 3},
	{subsets: 3, partition: 6, colour: 5, index: 2},
	{subsets: 2, partition: 6, colour: 7, endpointBit: true, index: 2},
	{subsets: 1, rotation: 2, choice: 1, colour: 5, alpha: 6, index: 2, index2: 3},
	{subsets: 1, rotation: 2, colour: 7, alpha: 8, index: 2, index2: 2},
	{subsets: 1, colour: 7, alpha: 7, endpointBit: true, index: 4},
	{subsets: 2, partition: 6, colour: 5, alpha: 5, endpointBit: true, index: 2},
}

// bc7Weights is how far along from its subset's first endpoint to its second a texel's colour is, out of 64, by the
// texel's index: for an index of 2, 3 and 4 bits.
var bc7Weights = [5][]int{
	2: {0, 21, 43, 64},
	3: {0, 9, 18, 27, 37, 46, 55, 64},
	4: {0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64},
}

// bc7Partitions is the subset of each of a block's 16 texels, row by row, in each of the 64 partitions: of a block
// with two subsets, and of one with three. A block with one subset has none.
var bc7Partitions = [4][64]string{
	2: {
		"0011001100110011", "0001000100010001", "0111011101110111", "0001001100110111",
		"0000000100010011", "0011011101111111", "0001001101111111", "0000000100110111",
		"0000000000010011", "0011011111111111", "0000000101111111", "0000000000010111",
		"0001011111111111", "0000000011111111", "0000111111111111", "0000000000001111",
		"0000100011101111", "0111000100000000", "0000000010001110", "0111001100010000",
		"0011000100000000", "0000100011001110", "0000000010001100", "0111001100110001",
		"0011000100010000", "0000100010001100", "0110011001100110", "0011011001101100",
		"0001011111101000", "0000111111110000", "0111000110001110", "0011100110011100",
		"0101010101010101", "0000111100001111", "0101101001011010", "0011001111001100",
		"0011110000111100", "0101010110101010", "0110100101101001", "0101101010100101",
		"0111001111001110", "0001001111001000", "0011001001001100", "0011101111011100",
		"0110100110010110", "0011110011000011", "0110011010011001", "0000011001100000",
		"0100111001000000", "0010011100100000", "0000001001110010", "0000010011100100",
		"0110110010010011", "0011011011001001", "0110001110011100", "0011100111000110",
		"0110110011001001", "0110001100111001", "0111111010000001", "0001100011100111",
		"0000111100110011", "0011001111110000", "0010001011101110", "0100010001110111",
	},
	3: {
		"0011001102212222", "0001001122112221", "0000200122112211", "0222002200110111",
		"0000000011221122", "0011001100220022", "0022002211111111", "0011001122112211",
		"0000000011112222", "0000111111112222", "0000111122222222", "0012001200120012",
		"0112011201120112", "0122012201220122", "0011011211221222", "0011200122002220",
		"0001001101121122", "0111001120012200", "0000112211221122", "0022002200221111",
		"0111011102220222", "0001000122212221", "0000001101220122", "0000110022102210",
		"0122012200110000", "0012001211222222", "0110122112210110", "0000011012211221",
		"0022110211020022", "0110011020022222", "0011012201220011", "0000200022112221",
		"0000000211221222", "0222002200120011", "0011001200220222", "0120012001200120",
		"0000111122220000", "0120120120120120", "0120201212010120", "0011220011220011",
		"0011112222000011", "0101010122222222", "0000000021212121", "0022112200221122",
		"0022001100220011", "0220122102201221", "0101222222220101", "0000212121212121",
		"0101010101012222", "0222011102220111", "0002111200021112", "0000211221122112",
		"0222011101110222", "0002111211120002", "0110011001102222", "0000000021122112",
		"0110011022222222", "0022001100110022", "0022112211220022", "0000000000002112",
		"0002000100020001", "0222122202221222", "0101222222222222", "0111201122012220",
	},
}

// bc7Anchors is the anchor of each subset but the first, in each of the 64 partitions: of the second subset of a
// block with two, and of the second and the third of a block with three.
var bc7Anchors = [4][2][64]uint8{
	2: {{
		15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
		15, 2, 8, 2, 2, 8, 8, 15, 2, 8, 2, 2, 8, 8, 2, 2,
		15, 15, 6, 8, 2, 8, 15, 15, 2, 8, 2, 2, 2, 15, 15, 6,
		6, 2, 6, 8, 15, 15, 2, 2, 15, 15, 15, 15, 15, 2, 2, 15,
	}},
	3: {{
		3, 3, 15, 15, 8, 3, 15, 15, 8, 8, 6, 6, 6, 5, 3, 3,
		3, 3, 8, 15, 3, 3, 6, 10, 5, 8, 8, 6, 8, 5, 15, 15,
		8, 15, 3, 5, 6, 10, 8, 15, 15, 3, 15, 5, 15, 15, 15, 15,
		3, 15, 5, 5, 5, 8, 5, 10, 5, 10, 8, 13, 15, 12, 3, 3,
	}, {
		15, 8, 8, 3, 15, 15, 3, 8, 15, 15, 15, 15, 15, 15, 15, 8,
		15, 8, 15, 3, 15, 8, 15, 8, 3, 15, 6, 10, 15, 15, 10, 8,
		15, 3, 15, 10, 10, 8, 9, 10, 6, 15, 8, 15, 3, 6, 6, 8,
		15, 3, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 3, 15, 15, 8,
	}},
}

// decodeBC7 decodes a picture of w by h texels in BC7: its blocks, row by row.
func decodeBC7(data []byte, w, h int) (*image.NRGBA, error) {
	across, down := (w+3)/4, (h+3)/4
	if w <= 0 || h <= 0 || len(data) < across*down*16 {
		return nil, errors.New("a short BC7 picture")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	var px [16]color.NRGBA
	for by := 0; by < down; by++ {
		for bx := 0; bx < across; bx++ {
			decodeBC7Block(data[(by*across+bx)*16:], &px)
			for i, c := range px {
				if x, y := bx*4+i%4, by*4+i/4; x < w && y < h {
					img.SetNRGBA(x, y, c)
				}
			}
		}
	}
	return img, nil
}

// decodeBC7Block decodes a block of 16 bytes (above). A block of no mode, whose first byte is 0, is transparent
// black.
func decodeBC7Block(block []byte, px *[16]color.NRGBA) {
	low, high := binary.LittleEndian.Uint64(block), binary.LittleEndian.Uint64(block[8:])
	at := 0
	// read returns the block's next n bits, 8 at most.
	read := func(n int) int {
		var v uint64
		if at >= 64 {
			v = high >> (at - 64)
		} else if v = low >> at; at+n > 64 {
			v |= high << (64 - at)
		}
		at += n
		return int(v & (1<<n - 1))
	}
	mode := 0
	for mode < 8 && read(1) == 0 {
		mode++
	}
	if mode == 8 {
		*px = [16]color.NRGBA{}
		return
	}
	m := bc7Modes[mode]
	partition, rotation, choice := read(m.partition), read(m.rotation), read(m.choice)

	// the endpoints: two for each subset, each its red, green, blue and alpha
	var ends [6][4]int
	n := 2 * m.subsets
	for c := 0; c < 3; c++ {
		for e := 0; e < n; e++ {
			ends[e][c] = read(m.colour)
		}
	}
	for e := 0; e < n; e++ {
		ends[e][3] = read(m.alpha)
	}
	colour, alpha := m.colour, m.alpha
	if m.endpointBit || m.subsetBit {
		bit := 0
		for e := 0; e < n; e++ {
			// a subset's bit is the lowest bit of both of its endpoints
			if m.endpointBit || e%2 == 0 {
				bit = read(1)
			}
			for c := range ends[e] {
				ends[e][c] = ends[e][c]<<1 | bit
			}
		}
		if colour++; alpha > 0 {
			alpha++
		}
	}
	// A channel of fewer than 8 bits is made 8: its bits, and then its top bits again below them. A mode without
	// alpha is opaque.
	wide := func(v, bits int) int {
		v <<= 8 - bits
		return v | v>>bits
	}
	for e := 0; e < n; e++ {
		for c := 0; c < 3; c++ {
			ends[e][c] = wide(ends[e][c], colour)
		}
		if alpha > 0 {
			ends[e][3] = wide(ends[e][3], alpha)
		} else {
			ends[e][3] = 255
		}
	}

	// the texels' subsets and the subsets' anchors
	var subset [16]int
	anchors := [3]int{0, -1, -1}
	if m.subsets > 1 {
		for i := range subset {
			subset[i] = int(bc7Partitions[m.subsets][partition][i] - '0')
		}
		for s := 1; s < m.subsets; s++ {
			anchors[s] = int(bc7Anchors[m.subsets][s-1][partition])
		}
	}
	// the texels' indices, and their second indices in the modes that have them
	var index, index2 [16]int
	for i := range index {
		bits := m.index
		if i == anchors[subset[i]] {
			bits--
		}
		index[i] = read(bits)
	}
	if m.index2 > 0 {
		for i := range index2 {
			bits := m.index2
			if i == 0 {
				bits--
			}
			index2[i] = read(bits)
		}
	}
	for i := range px {
		a, b := ends[2*subset[i]], ends[2*subset[i]+1]
		// how far along the colour is, and the alpha
		wc := bc7Weights[m.index][index[i]]
		wa := wc
		if m.index2 > 0 {
			if wa = bc7Weights[m.index2][index2[i]]; choice == 1 {
				wc, wa = wa, wc
			}
		}
		var c [4]uint8
		for ch := range c {
			w := wc
			if ch == 3 {
				w = wa
			}
			c[ch] = uint8(((64-w)*a[ch] + w*b[ch] + 32) >> 6)
		}
		if rotation > 0 {
			c[rotation-1], c[3] = c[3], c[rotation-1]
		}
		px[i] = color.NRGBA{c[0], c[1], c[2], c[3]}
	}
}
