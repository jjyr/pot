package desktopview

import (
	"encoding/binary"
	"errors"
	"sort"
)

const rfbLimit = 4 * 1024 * 1024

type rfbGate struct {
	pending   []byte
	handshake int
	keys      map[uint32]bool
	qkeys     map[uint32][]byte
	pointer   []byte
}

func (g *rfbGate) push(data []byte, control, clipboard bool) ([]byte, error) {
	if len(g.pending)+len(data) > rfbLimit {
		return nil, errors.New("RFB buffer limit")
	}
	g.pending = append(g.pending, data...)
	var out []byte
	for len(g.pending) > 0 {
		p := g.pending
		n := 0
		if g.handshake == 0 {
			n = 12
		} else if g.handshake < 3 {
			n = 1
		} else {
			switch p[0] {
			case 0:
				n = 20
			case 3:
				n = 10
			case 4:
				n = 8
			case 5:
				n = 6
			case 150:
				n = 10
			case 255:
				if len(p) < 2 {
					return out, nil
				}
				if p[1] != 0 {
					return nil, errors.New("unknown QEMU message")
				}
				n = 12
			case 248:
				if len(p) < 9 {
					return out, nil
				}
				n = 9 + int(p[8])
			case 2:
				if len(p) < 4 {
					return out, nil
				}
				n = 4 + 4*int(binary.BigEndian.Uint16(p[2:4]))
			case 6:
				if len(p) < 8 {
					return out, nil
				}
				size := int64(int32(binary.BigEndian.Uint32(p[4:8])))
				if size < 0 {
					size = -size
				}
				if size > rfbLimit {
					return nil, errors.New("RFB clipboard limit")
				}
				n = 8 + int(size)
			default:
				return nil, errors.New("unsupported RFB client message")
			}
		}
		if n > rfbLimit {
			return nil, errors.New("RFB message limit")
		}
		if len(p) < n {
			return out, nil
		}
		if g.handshake == 0 && string(p[:n]) != "RFB 003.008\n" {
			return nil, errors.New("RFB 3.8 required")
		}
		if g.handshake == 1 && p[0] != 1 {
			return nil, errors.New("private RFB None authentication required")
		}
		caps := g.handshake == 3 && p[0] == 6 && n >= 12 && int32(binary.BigEndian.Uint32(p[4:8])) < 0 && binary.BigEndian.Uint32(p[8:12])&0x1f000000 == 0x01000000
		input := p[0] == 4 || p[0] == 5 || p[0] == 6 || p[0] == 255
		if g.handshake < 3 || !input || caps || (control && (p[0] != 6 || clipboard)) {
			if g.handshake == 3 {
				switch p[0] {
				case 4:
					if g.keys == nil {
						g.keys = map[uint32]bool{}
					}
					key := binary.BigEndian.Uint32(p[4:8])
					if p[1] != 0 {
						g.keys[key] = true
					} else {
						delete(g.keys, key)
					}
				case 5:
					g.pointer = append(g.pointer[:0], p[2:6]...)
				case 255:
					if g.qkeys == nil {
						g.qkeys = map[uint32][]byte{}
					}
					code := binary.BigEndian.Uint32(p[8:12])
					if p[2] != 0 || p[3] != 0 {
						g.qkeys[code] = append([]byte{}, p[:n]...)
					} else {
						delete(g.qkeys, code)
					}
				}
			}
			out = append(out, p[:n]...)
		}
		g.pending = g.pending[n:]
		if g.handshake < 3 {
			g.handshake++
		}
	}
	return out, nil
}
func (g *rfbGate) changed() error {
	if len(g.pending) > 0 {
		return errors.New("control changed during partial RFB packet")
	}
	return nil
}
func (g *rfbGate) release() []byte {
	var out []byte
	keys := make([]uint32, 0, len(g.keys))
	for key := range g.keys {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, key := range keys {
		out = append(out, 4, 0, 0, 0)
		out = binary.BigEndian.AppendUint32(out, key)
	}
	g.keys = nil
	for _, packet := range g.qkeys {
		packet[2], packet[3] = 0, 0
		out = append(out, packet...)
	}
	g.qkeys = nil
	if len(g.pointer) > 0 {
		out = append(out, 5, 0)
		out = append(out, g.pointer...)
		g.pointer = nil
	}
	return out
}
