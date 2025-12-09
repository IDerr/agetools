// Package lzss provides LZSS compression compatible with Eushully AGE engine.
// Based on the Allegro LZSS implementation by Haruhiko Okumura and Shawn Hargreaves.
package lzss

const (
	N         = 4096 // Ring buffer size
	F         = 18   // Max match length
	Threshold = 2    // Minimum match length to encode as reference
	NMask     = N - 1
)

// Compress compresses data using LZSS algorithm compatible with Eushully engine.
func Compress(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}

	// Initialize ring buffer with zeros
	textBuf := make([]byte, N+F-1)

	// Binary search trees
	lson := make([]int, N+1)
	rson := make([]int, N+257)
	dad := make([]int, N+1)

	// Initialize trees
	for i := N + 1; i <= N+256; i++ {
		rson[i] = N
	}
	for i := 0; i < N; i++ {
		dad[i] = N
	}

	var result []byte
	codeBuf := make([]byte, 17)
	codeBuf[0] = 0
	codeBufPtr := 1
	var mask byte = 1

	s := 0
	r := N - F
	srcPos := 0

	// Read initial F bytes
	var matchLen, matchPos int
	length := 0
	for length < F && srcPos < len(src) {
		textBuf[r+length] = src[srcPos]
		srcPos++
		length++
	}

	if length == 0 {
		return nil
	}

	// Insert initial strings
	for i := 1; i <= F; i++ {
		insertNode(r-i, textBuf, lson, rson, dad, &matchPos, &matchLen)
	}
	insertNode(r, textBuf, lson, rson, dad, &matchPos, &matchLen)

	for length > 0 {
		if matchLen > length {
			matchLen = length
		}

		if matchLen <= Threshold {
			// Send literal byte
			matchLen = 1
			codeBuf[0] |= mask
			codeBuf[codeBufPtr] = textBuf[r]
			codeBufPtr++
		} else {
			// Send position and length pair
			codeBuf[codeBufPtr] = byte(matchPos & 0xFF)
			codeBufPtr++
			codeBuf[codeBufPtr] = byte(((matchPos >> 4) & 0xF0) | ((matchLen - (Threshold + 1)) & 0x0F))
			codeBufPtr++
		}

		mask <<= 1
		if mask == 0 {
			// Flush code buffer
			for i := 0; i < codeBufPtr; i++ {
				result = append(result, codeBuf[i])
			}
			codeBuf[0] = 0
			codeBufPtr = 1
			mask = 1
		}

		lastMatchLen := matchLen

		var i int
		for i = 0; i < lastMatchLen && srcPos < len(src); i++ {
			c := src[srcPos]
			srcPos++

			deleteNode(s, dad, lson, rson)
			textBuf[s] = c
			if s < F-1 {
				textBuf[s+N] = c
			}
			s = (s + 1) & NMask
			r = (r + 1) & NMask
			insertNode(r, textBuf, lson, rson, dad, &matchPos, &matchLen)
		}

		for i < lastMatchLen {
			i++
			deleteNode(s, dad, lson, rson)
			s = (s + 1) & NMask
			r = (r + 1) & NMask
			length--
			if length > 0 {
				insertNode(r, textBuf, lson, rson, dad, &matchPos, &matchLen)
			}
		}
	}

	// Flush remaining code
	if codeBufPtr > 1 {
		for i := 0; i < codeBufPtr; i++ {
			result = append(result, codeBuf[i])
		}
	}

	return result
}

// insertNode inserts a string into the binary search tree.
func insertNode(r int, textBuf []byte, lson, rson, dad []int, matchPos, matchLen *int) {
	cmp := 1
	key := textBuf[r:]
	p := N + 1 + int(key[0])
	rson[r] = N
	lson[r] = N
	*matchLen = 0

	for {
		if cmp >= 0 {
			if rson[p] != N {
				p = rson[p]
			} else {
				rson[p] = r
				dad[r] = p
				return
			}
		} else {
			if lson[p] != N {
				p = lson[p]
			} else {
				lson[p] = r
				dad[r] = p
				return
			}
		}

		var i int
		for i = 1; i < F; i++ {
			cmp = int(key[i]) - int(textBuf[p+i])
			if cmp != 0 {
				break
			}
		}

		if i > *matchLen {
			*matchPos = p
			*matchLen = i
			if i >= F {
				break
			}
		}
	}

	dad[r] = dad[p]
	lson[r] = lson[p]
	rson[r] = rson[p]
	dad[lson[p]] = r
	dad[rson[p]] = r

	if rson[dad[p]] == p {
		rson[dad[p]] = r
	} else {
		lson[dad[p]] = r
	}
	dad[p] = N
}

// deleteNode removes a node from the binary search tree.
func deleteNode(p int, dad, lson, rson []int) {
	if dad[p] == N {
		return
	}

	var q int
	if rson[p] == N {
		q = lson[p]
	} else if lson[p] == N {
		q = rson[p]
	} else {
		q = lson[p]
		if rson[q] != N {
			for rson[q] != N {
				q = rson[q]
			}
			rson[dad[q]] = lson[q]
			dad[lson[q]] = dad[q]
			lson[q] = lson[p]
			dad[lson[p]] = q
		}
		rson[q] = rson[p]
		dad[rson[p]] = q
	}

	dad[q] = dad[p]
	if rson[dad[p]] == p {
		rson[dad[p]] = q
	} else {
		lson[dad[p]] = q
	}
	dad[p] = N
}

// Decompress decompresses LZSS data compatible with Eushully engine.
func Decompress(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}

	textBuf := make([]byte, N+F-1)
	// Initialize buffer with zeros (already done by make)

	var result []byte
	r := N - F
	var flags uint

	srcPos := 0
	for srcPos < len(src) {
		flags >>= 1
		if (flags & 256) == 0 {
			if srcPos >= len(src) {
				break
			}
			c := src[srcPos]
			srcPos++
			flags = uint(c) | 0xFF00
		}

		if (flags & 1) != 0 {
			// Literal byte
			if srcPos >= len(src) {
				break
			}
			c := src[srcPos]
			srcPos++
			textBuf[r] = c
			r = (r + 1) & NMask
			result = append(result, c)
		} else {
			// Back reference
			if srcPos+1 >= len(src) {
				break
			}
			i := int(src[srcPos])
			srcPos++
			j := int(src[srcPos])
			srcPos++

			i |= (j & 0xF0) << 4
			j = (j & 0x0F) + Threshold

			for k := 0; k <= j; k++ {
				c := textBuf[(i+k)&NMask]
				textBuf[r] = c
				r = (r + 1) & NMask
				result = append(result, c)
			}
		}
	}

	return result
}
