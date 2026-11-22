package main

// The framing of WebTransport datagrams is the same as in bo1-zombies/src/qcommon/net_webtransport.h. The web
// client's side (bo1-zombies/web/src/web_net.cpp) uses that file. A WebTransport datagram carries one UDP packet when
// the packet fits, and a fragment of a packet otherwise:
//
//	whole packet:  0x00, packet
//	fragment:      0x01, id (16 bits, little endian), index, count, piece   (pieces of fragmentSize, the last shorter)
//
// No datagram is longer than maxDatagram. That fits a QUIC path at its minimum packet size (1200 bytes), with room
// for QUIC's own headers. A netchan packet takes one datagram, because the game makes netchan packets to fit
// (MAX_PACKETLEN in bo1-zombies/src/qcommon/net_chan_mp.h). Out-of-band packets can be up to 64 KB, as over UDP.
const (
	maxDatagram    = 1100
	frameWhole     = 0x00
	frameFragment  = 0x01
	fragmentHeader = 5
	fragmentSize   = maxDatagram - fragmentHeader
	maxFragments   = 64 // 64 * fragmentSize covers the largest packet
	maxPacket      = 0x10000
)

// framePacket splits a packet into datagrams and passes each to emit, in order. It stops at emit's first error and
// returns it. nextID numbers one sender's fragmented packets. A packet larger than maxPacket is dropped.
func framePacket(packet []byte, nextID *uint16, emit func(datagram []byte) error) error {
	var datagram [maxDatagram]byte
	if len(packet) > maxPacket {
		return nil
	}
	count := datagrams(len(packet))
	if count == 1 {
		datagram[0] = frameWhole
		n := copy(datagram[1:], packet)
		return emit(datagram[:1+n])
	}
	id := *nextID
	*nextID++
	for index := 0; index < count; index++ {
		piece := packet[index*fragmentSize : min((index+1)*fragmentSize, len(packet))]
		datagram[0] = frameFragment
		datagram[1] = byte(id)
		datagram[2] = byte(id >> 8)
		datagram[3] = byte(index)
		datagram[4] = byte(count)
		n := copy(datagram[fragmentHeader:], piece)
		if err := emit(datagram[:fragmentHeader+n]); err != nil {
			return err
		}
	}
	return nil
}

// datagrams returns how many datagrams framePacket makes of a packet of length bytes.
func datagrams(length int) int {
	if length+1 <= maxDatagram {
		return 1
	}
	return (length + fragmentSize - 1) / fragmentSize
}

// partialPacket is a packet that is being put back together from its fragments.
type partialPacket struct {
	data     []byte
	received uint64 // a bit per fragment index
	length   int    // the bytes received so far
	id       uint16
	count    int
	inUse    bool
	age      uint32 // the reassembly's clock when the packet started; the oldest packet is replaced first
}

// reassembly puts one sender's fragmented packets back together. It holds two packets at a time, so that a fragment
// that arrives late (datagrams can be reordered) does not discard the packet after it.
type reassembly struct {
	partial [2]partialPacket
	clock   uint32
}

// receive takes one datagram and returns the packet it completes. It returns nil when the datagram is a fragment of a
// packet that still misses pieces, or is malformed. A whole packet is returned as a slice of the datagram.
func (r *reassembly) receive(datagram []byte) []byte {
	if len(datagram) < 1 {
		return nil
	}
	if datagram[0] == frameWhole {
		return datagram[1:]
	}
	if datagram[0] != frameFragment || len(datagram) <= fragmentHeader {
		return nil
	}
	id := uint16(datagram[1]) | uint16(datagram[2])<<8
	index := int(datagram[3])
	count := int(datagram[4])
	piece := datagram[fragmentHeader:]
	if count < 2 || count > maxFragments || index >= count {
		return nil
	}
	// every piece but the last is full
	if index < count-1 && len(piece) != fragmentSize || index == count-1 && len(piece) > fragmentSize {
		return nil
	}

	var p *partialPacket
	for i := range r.partial {
		if r.partial[i].inUse && r.partial[i].id == id {
			p = &r.partial[i]
		}
	}
	if p != nil && p.count != count {
		return nil
	}
	if p == nil {
		// A new packet takes a free slot, or else the slot of the packet that started first.
		switch {
		case !r.partial[0].inUse:
			p = &r.partial[0]
		case !r.partial[1].inUse:
			p = &r.partial[1]
		case int32(r.partial[0].age-r.partial[1].age) <= 0:
			p = &r.partial[0]
		default:
			p = &r.partial[1]
		}
		*p = partialPacket{data: make([]byte, count*fragmentSize), id: id, count: count, inUse: true, age: r.clock}
		r.clock++
	}

	bit := uint64(1) << index
	if p.received&bit != 0 {
		return nil // a duplicate
	}
	p.received |= bit
	copy(p.data[index*fragmentSize:], piece)
	p.length += len(piece)
	all := ^uint64(0)
	if count < 64 {
		all = 1<<count - 1
	}
	if p.received != all {
		return nil
	}
	packet := p.data[:p.length]
	*p = partialPacket{}
	if len(packet) > maxPacket {
		return nil
	}
	return packet
}
