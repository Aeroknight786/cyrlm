// Package tok provides a deterministic, tokenizer-free token estimator and a
// chat-template-like renderer.
//
// The estimator splits text into BPE-like pieces (short letter runs, 1–3
// digit groups, single punctuation, newline runs). It is prefix-stable:
// appending text only changes the last piece, so the longest common prefix
// of two renderings approximates the KV-cache reuse a real runtime would get.
// Absolute counts are corrected at runtime with an exponential moving average
// of the upstream's reported usage (see Calibrator).
package tok

import (
	"hash/fnv"
	"unicode"
	"unicode/utf8"
)

// maxLetters is the longest letter run kept in one piece.
const maxLetters = 6

// Pieces splits s into estimated tokens.
func Pieces(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		start := i
		switch {
		case r == '\n' || r == '\r':
			for i < len(s) && (s[i] == '\n' || s[i] == '\r') {
				i++
			}
		case r == ' ' || r == '\t':
			// Spaces attach to the following word; runs of indentation form
			// their own piece (minus the space that attaches).
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			if j-i > 1 {
				i = j - 1
				if j < len(s) && !isWordStart(s[j:]) {
					i = j
				}
				break
			}
			i += size
			if i < len(s) {
				i = consumeWord(s, i)
			}
		case unicode.IsLetter(r) && r < 0x2E80:
			i = consumeWord(s, i)
		case unicode.IsDigit(r):
			i = consumeDigits(s, i)
		default:
			i += size
		}
		if i == start {
			i += size
		}
		out = append(out, s[start:i])
	}
	return out
}

func isWordStart(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return (unicode.IsLetter(r) && r < 0x2E80) || unicode.IsDigit(r)
}

// consumeWord consumes one letter or digit piece starting at i.
func consumeWord(s string, i int) int {
	r, size := utf8.DecodeRuneInString(s[i:])
	if unicode.IsDigit(r) {
		return consumeDigits(s, i)
	}
	if !unicode.IsLetter(r) || r >= 0x2E80 {
		return i + size
	}
	n := 0
	prevLower := false
	for i < len(s) {
		r, size = utf8.DecodeRuneInString(s[i:])
		if !unicode.IsLetter(r) || r >= 0x2E80 {
			break
		}
		// camelCase boundary.
		if n > 0 && prevLower && unicode.IsUpper(r) {
			break
		}
		if n >= maxLetters {
			break
		}
		prevLower = unicode.IsLower(r)
		i += size
		n++
	}
	return i
}

func consumeDigits(s string, i int) int {
	n := 0
	for i < len(s) && n < 3 && s[i] >= '0' && s[i] <= '9' {
		i++
		n++
	}
	if n == 0 {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return i
}

// Count returns the estimated token count of s.
func Count(s string) int { return len(Pieces(s)) }

// Hash returns a stable 64-bit hash of a piece.
func Hash(p string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(p))
	return h.Sum64()
}

// Seq is a token sequence with the piece text kept for explanations.
type Seq struct {
	IDs    []uint64
	Pieces []string
}

// Len returns the number of tokens.
func (s *Seq) Len() int { return len(s.IDs) }

// Append adds the pieces of text.
func (s *Seq) Append(text string) {
	for _, p := range Pieces(text) {
		s.IDs = append(s.IDs, Hash(p))
		s.Pieces = append(s.Pieces, p)
	}
}

// AppendSpecial adds one special token (role marker).
func (s *Seq) AppendSpecial(marker string) {
	s.IDs = append(s.IDs, Hash("\x00"+marker))
	s.Pieces = append(s.Pieces, marker)
}

// Clone returns a copy.
func (s *Seq) Clone() *Seq {
	return &Seq{IDs: append([]uint64(nil), s.IDs...), Pieces: append([]string(nil), s.Pieces...)}
}

// Truncate keeps the first n tokens.
func (s *Seq) Truncate(n int) {
	if n < len(s.IDs) {
		s.IDs = s.IDs[:n]
		s.Pieces = s.Pieces[:n]
	}
}

// LCP returns the length of the longest common prefix of a and b.
func LCP(a, b *Seq) int {
	if a == nil || b == nil {
		return 0
	}
	n := len(a.IDs)
	if len(b.IDs) < n {
		n = len(b.IDs)
	}
	for i := 0; i < n; i++ {
		if a.IDs[i] != b.IDs[i] {
			return i
		}
	}
	return n
}

// Context returns the text of up to width pieces before and after token i,
// for explaining where a prompt diverged.
func (s *Seq) Context(i, width int) (before, at string) {
	lo := i - width
	if lo < 0 {
		lo = 0
	}
	hi := i + width
	if hi > len(s.Pieces) {
		hi = len(s.Pieces)
	}
	for k := lo; k < i && k < len(s.Pieces); k++ {
		before += s.Pieces[k]
	}
	for k := i; k < hi; k++ {
		at += s.Pieces[k]
	}
	return before, at
}

// Calibrator tracks the ratio between real (upstream-reported) and estimated
// token counts with an exponential moving average.
type Calibrator struct {
	Ratio   float64 // real / estimated
	Samples int
	alpha   float64
}

// NewCalibrator starts at ratio r0.
func NewCalibrator(r0 float64) *Calibrator {
	return &Calibrator{Ratio: r0, alpha: 0.3}
}

// Observe records one (real, estimated) pair. Tiny samples are ignored.
func (c *Calibrator) Observe(real, estimated int) {
	if real <= 0 || estimated < 16 {
		return
	}
	r := float64(real) / float64(estimated)
	if r < 0.2 || r > 5 {
		return
	}
	if c.Samples == 0 {
		c.Ratio = r
	} else {
		c.Ratio = c.alpha*r + (1-c.alpha)*c.Ratio
	}
	c.Samples++
}

// Real converts an estimated count to the calibrated real count.
func (c *Calibrator) Real(estimated int) int {
	return int(float64(estimated)*c.Ratio + 0.5)
}
