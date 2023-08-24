package trigram

import "strings"

// How many null characters to capture in trigram conversion.
// Either 0, 1, or 2. For example, with 2, "a" would yield ("__a", "_a_", "a__").
var IncludeNulls = 0

type Trigram uint64

func (t Trigram) String() string {
	return string(t.Runes())
}

func (t Trigram) Runes() []rune {
	const mask = uint64(1<<21 - 1)
	var c0, c1, c2 int32
	v := uint64(t)
	c0 = int32(v >> 42)
	c1 = int32(v >> 21 & mask)
	c2 = int32(v & mask)
	return []rune{c0, c1, c2}
}

func (t Trigram) LargestRune() rune {
	var ret rune
	for _, r := range t.Runes() {
		if r > ret {
			ret = r
		}
	}
	return ret
}

func FromTrigrams(in []Trigram) string {
	var b strings.Builder
	last := len(in) - 1 - IncludeNulls
	for i := IncludeNulls; i <= last; i++ {
		r := in[i].Runes()
		b.WriteRune(r[0])
		if i == last {
			b.WriteRune(r[1])
			b.WriteRune(r[2])
		}
	}
	return b.String()
}

func ToTrigram(str string) []Trigram {
	if len(str) == 0 {
		return nil
	}

	var runes []rune
	for _, r := range str {
		runes = append(runes, r)
	}

	var result []Trigram
	// The first will be "\x00{first 2 chars}" and the last will be
	// "{last 2 chars}\x00".
	for i := -1 * IncludeNulls; i < len(runes)+IncludeNulls-2; i++ {
		c := []uint64{0, 0, 0}
		for j := range c {
			pos := i + j
			if pos >= 0 && pos < len(runes) {
				c[j] = uint64(runes[pos])
			}
		}
		result = append(result, Trigram(c[0]<<42|c[1]<<21|c[2]))
	}

	return result
}
