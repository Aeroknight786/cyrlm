package openai

import "unicode/utf8"

// utf8Carry holds back a trailing incomplete UTF-8 sequence of a fragment
// until the next fragment of the same channel completes it. Fragments split
// at byte (token) boundaries may cut a multi-byte rune in two; encoding the
// halves separately as JSON strings would replace each with U+FFFD.
type utf8Carry struct{ pending string }

// take returns the longest prefix of pending+s that does not end inside a
// rune, keeping the remainder pending.
func (c *utf8Carry) take(s string) string {
	s = c.pending + s
	c.pending = ""
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax+1; i-- {
		if !utf8.RuneStart(s[i]) {
			continue
		}
		if !utf8.FullRuneInString(s[i:]) {
			c.pending = s[i:]
			return s[:i]
		}
		break
	}
	return s
}

// flush returns and clears whatever is pending (an unterminated sequence).
func (c *utf8Carry) flush() string {
	s := c.pending
	c.pending = ""
	return s
}
