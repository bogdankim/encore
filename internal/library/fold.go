package library

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Fold returns s in the form encore matches and sorts on: lowercase, diacritics
// removed from Latin, Greek and Cyrillic letters, compatibility forms
// normalized (ﬁ → fi, full-width → ASCII), typographic quotes and dashes
// mapped to ASCII, control and format characters dropped, and whitespace runs
// collapsed to one space with no leading or trailing space.
//
// Search folds queries with the same function, so a folded field and a folded
// query always compare byte for byte.
func Fold(s string) string {
	if isFolded(s) {
		return s
	}
	f := folder{}
	f.b.Grow(len(s))
	f.run(s)
	return f.b.String()
}

// FoldMap is Fold that also reports, for every byte of the folded string, the
// index of the rune in s it came from. Search uses it to turn match offsets in
// folded text into rune positions in the displayed text.
func FoldMap(s string) (string, []int) {
	f := folder{mapping: true}
	f.b.Grow(len(s))
	f.m = make([]int, 0, len(s))
	f.run(s)
	return f.b.String(), f.m
}

// isFolded reports whether Fold(s) == s, checking only the ASCII cases.
func isFolded(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= utf8.RuneSelf, c < ' ', c == 0x7f, 'A' <= c && c <= 'Z':
			return false
		case c == ' ' && (i == 0 || i == len(s)-1 || s[i+1] == ' '):
			return false
		}
	}
	return true
}

type folder struct {
	b       strings.Builder
	m       []int
	mapping bool

	src      int  // rune index in the source of the rune being folded
	space    bool // a space is pending; written before the next visible rune
	spaceSrc int
	scratch  [32]byte
}

func (f *folder) run(s string) {
	f.src = 0
	for i := 0; i < len(s); f.src++ {
		c := s[i]
		if c < utf8.RuneSelf {
			i++
			switch {
			case 'A' <= c && c <= 'Z':
				f.byte(c + 'a' - 'A')
			case c <= ' ' || c == 0x7f:
				f.pendSpace()
			default:
				f.byte(c)
			}
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		f.rune(r)
	}
}

func (f *folder) pendSpace() {
	if f.b.Len() > 0 && !f.space {
		f.space = true
		f.spaceSrc = f.src
	}
}

func (f *folder) flushSpace() {
	if f.space {
		f.space = false
		f.b.WriteByte(' ')
		if f.mapping {
			f.m = append(f.m, f.spaceSrc)
		}
	}
}

func (f *folder) byte(c byte) {
	f.flushSpace()
	f.b.WriteByte(c)
	if f.mapping {
		f.m = append(f.m, f.src)
	}
}

func (f *folder) str(s string) {
	if s == "" {
		return
	}
	f.flushSpace()
	f.b.WriteString(s)
	if f.mapping {
		for range len(s) {
			f.m = append(f.m, f.src)
		}
	}
}

// emit writes a rune that is already folded.
func (f *folder) emit(r rune) {
	if r < utf8.RuneSelf {
		f.byte(byte(r))
		return
	}
	f.flushSpace()
	n, _ := f.b.WriteRune(r)
	if f.mapping {
		for range n {
			f.m = append(f.m, f.src)
		}
	}
}

// rune folds one non-ASCII rune.
func (f *folder) rune(r rune) {
	if r == utf8.RuneError || unicode.IsSpace(r) || unicode.Is(unicode.Cc, r) {
		f.pendSpace()
		return
	}
	if unicode.Is(unicode.Cf, r) {
		return
	}
	lr := unicode.ToLower(r)
	if rep, ok := specialFolds[lr]; ok {
		f.str(rep)
		return
	}
	var src [utf8.UTFMax]byte
	n := utf8.EncodeRune(src[:], r)
	d := norm.NFKD.Append(f.scratch[:0], src[:n]...)
	base, _ := utf8.DecodeRune(d)
	if !strippable(base) {
		// Scripts where combining marks carry meaning (kana voicing, Indic
		// vowel signs): keep the composed compatibility form.
		d = norm.NFKC.Append(f.scratch[:0], src[:n]...)
	}
	for len(d) > 0 {
		dr, dn := utf8.DecodeRune(d)
		d = d[dn:]
		if strippable(base) && unicode.Is(unicode.Mn, dr) {
			continue
		}
		dr = unicode.ToLower(dr)
		if dr < utf8.RuneSelf {
			switch {
			case dr <= ' ' || dr == 0x7f:
				f.pendSpace()
			default:
				f.byte(byte(dr))
			}
			continue
		}
		if rep, ok := specialFolds[dr]; ok {
			f.str(rep)
			continue
		}
		if unicode.IsSpace(dr) {
			f.pendSpace()
			continue
		}
		f.emit(dr)
	}
}

// strippable reports whether combining marks on r are decoration that can be
// dropped: Latin, Greek, Cyrillic and Armenian, plus Latin Extended Additional
// and Greek Extended.
func strippable(r rune) bool {
	return r < 0x0590 || (0x1E00 <= r && r <= 0x1FFF)
}

// specialFolds maps lowercase runes that NFKD leaves alone, or maps to something
// unhelpful, onto what a person would type.
var specialFolds = map[rune]string{
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'ł': "l", 'đ': "d", 'ð': "d",
	'þ': "th", 'ı': "i", 'ħ': "h", 'ŧ': "t", 'ŋ': "n", 'ĸ': "k", 'ſ': "s",
	'ς': "σ",
	'‘': "'", '’': "'", '‚': "'", '‛': "'", '′': "'", 'ʼ': "'", '´': "'",
	'“': `"`, '”': `"`, '„': `"`, '‟': `"`, '″': `"`, '«': `"`, '»': `"`,
	'‐': "-", '‑': "-", '‒': "-", '–': "-", '—': "-", '―': "-", '−': "-",
	'×': "x",
}

// sortKey turns a folded name into a sort key: Apple Music ignores a leading
// "The" when sorting.
func sortKey(folded string) string {
	if len(folded) > 4 && strings.HasPrefix(folded, "the ") {
		return folded[4:]
	}
	return folded
}

// CompareKeys orders folded sort keys the way Apple Music lists them: keys that
// start with a letter come before keys that start with anything else, empty
// keys last, and runs of digits compare by value so "track 2" precedes
// "track 10".
func CompareKeys(a, b string) int {
	if ca, cb := keyClass(a), keyClass(b); ca != cb {
		return ca - cb
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		x, y := a[i], b[j]
		if isDigit(x) && isDigit(y) {
			si, sj := i, j
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na := strings.TrimLeft(a[si:i], "0")
			nb := strings.TrimLeft(b[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) - len(nb)
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			// Same value: fewer leading zeros first.
			if c := (i - si) - (j - sj); c != 0 {
				return c
			}
			continue
		}
		if x != y {
			return int(x) - int(y)
		}
		i++
		j++
	}
	return (len(a) - i) - (len(b) - j)
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func keyClass(s string) int {
	if s == "" {
		return 2
	}
	c := s[0]
	if c < utf8.RuneSelf {
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' {
			return 0
		}
		return 1
	}
	r, _ := utf8.DecodeRuneInString(s)
	if unicode.IsLetter(r) {
		return 0
	}
	return 1
}
