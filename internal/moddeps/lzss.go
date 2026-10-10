// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import "errors"

// decompressLZSS decodes a PBO "Cprs" entry into size bytes. The stream is
// groups of one flag byte followed by eight items (flag bits read LSB
// first): bit 1 = one literal byte; bit 0 = a two-byte back reference
// b1,b2 with distance = b1 | (b2&0xF0)<<4 (bytes back from the end of the
// output so far, 1 = the last byte, so a copy may overlap itself) and
// length = (b2&0x0F)+3. A distance beyond the start of the output reads
// spaces. A 4-byte additive checksum trails the stream; it is ignored.
// Checked against about 4900 PBOs of real workshop mods (the distance is not
// a position in a 4096-byte ring, as the format notes suggest).
//
// A negative size decodes up to the trailing checksum instead, for entries
// that hold LZSS data without saying so (see ExtractPatchesFromPBO).
func decompressLZSS(in []byte, size int) ([]byte, error) {
	out := make([]byte, 0, max(size, 0))
	i := 0
	more := func() bool {
		if size < 0 {
			return i < len(in)-4
		}
		return len(out) < size
	}
	for more() {
		if i >= len(in) {
			return nil, errors.New("truncated LZSS data")
		}
		flags := in[i]
		i++
		for bit := 0; bit < 8 && more(); bit++ {
			if flags&1 != 0 {
				if i >= len(in) {
					return nil, errors.New("truncated LZSS data")
				}
				out = append(out, in[i])
				i++
			} else {
				if i+2 > len(in) {
					return nil, errors.New("truncated LZSS data")
				}
				b1, b2 := int(in[i]), int(in[i+1])
				i += 2
				dist := b1 | (b2&0xF0)<<4
				n := b2&0x0F + 3
				if dist == 0 {
					return nil, errors.New("invalid LZSS back reference")
				}
				src := len(out) - dist
				for ; n > 0 && (size < 0 || len(out) < size); n-- {
					if src < 0 {
						out = append(out, ' ')
					} else {
						out = append(out, out[src])
					}
					src++
				}
			}
			flags >>= 1
		}
	}
	return out, nil
}
