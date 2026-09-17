package audio

import (
	"encoding/binary"
	"fmt"
	"math"
)

type Format struct {
	Rate, Channels, Bits, Align int
	Float                       bool
}

func ParseFormat(b []byte) (Format, error) {
	var f Format
	if len(b) < 18 {
		return f, fmt.Errorf("invalid WASAPI format header")
	}
	kind := binary.LittleEndian.Uint16(b)
	f.Channels = int(binary.LittleEndian.Uint16(b[2:]))
	f.Rate = int(binary.LittleEndian.Uint32(b[4:]))
	f.Align = int(binary.LittleEndian.Uint16(b[12:]))
	f.Bits = int(binary.LittleEndian.Uint16(b[14:]))
	if kind == 0xfffe {
		if len(b) < 40 || binary.LittleEndian.Uint16(b[16:]) < 22 {
			return f, fmt.Errorf("invalid extensible audio format")
		}
		// KSDATAFORMAT_SUBTYPE_PCM / IEEE_FLOAT, including the common GUID suffix.
		suffix := []byte{0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}
		for i, v := range suffix {
			if b[28+i] != v {
				return f, fmt.Errorf("unsupported audio subformat")
			}
		}
		kind = uint16(binary.LittleEndian.Uint32(b[24:]))
	}
	f.Float = kind == 3
	if f.Rate < 8000 || f.Rate > 192000 || f.Channels < 1 || f.Channels > 8 || f.Align != f.Channels*f.Bits/8 || !(kind == 1 && (f.Bits == 16 || f.Bits == 24 || f.Bits == 32) || kind == 3 && f.Bits == 32) {
		return f, fmt.Errorf("unsupported WASAPI PCM format")
	}
	return f, nil
}
func MonoPCM(raw []byte, frames int, f Format, silent bool) ([]int16, error) {
	if frames < 0 || frames > f.Rate*2 || f.Channels < 1 || f.Channels > 8 || f.Align < 1 || f.Rate < 8000 || f.Rate > 192000 {
		return nil, fmt.Errorf("invalid audio packet dimensions")
	}
	if !silent && len(raw) != frames*f.Align {
		return nil, fmt.Errorf("invalid audio packet length")
	}
	out := make([]int16, frames)
	if silent {
		return out, nil
	}
	for i := range out {
		sum := 0.0
		for ch := 0; ch < f.Channels; ch++ {
			p := raw[i*f.Align+ch*f.Bits/8:]
			var v float64
			switch {
			case f.Float && f.Bits == 32:
				v = float64(math.Float32frombits(binary.LittleEndian.Uint32(p)))
			case f.Bits == 16:
				v = float64(int16(binary.LittleEndian.Uint16(p))) / 32768
			case f.Bits == 24:
				n := int32(p[0]) | int32(p[1])<<8 | int32(p[2])<<16
				n = n << 8 >> 8
				v = float64(n) / 8388608
			case f.Bits == 32:
				v = float64(int32(binary.LittleEndian.Uint32(p))) / 2147483648
			default:
				return nil, fmt.Errorf("unsupported PCM sample")
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				v = 0
			}
			sum += v
		}
		value := sum / float64(f.Channels)
		if value > 1 {
			value = 1
		}
		if value < -1 {
			value = -1
		}
		out[i] = int16(math.Round(value * 32767))
	}
	return out, nil
}
func WAV(samples []int16, rate int) []byte {
	b := make([]byte, 44+2*len(samples))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(samples)))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(s))
	}
	return b
}
