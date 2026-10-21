package keymap

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// Token normalizes a key press into the canonical token the engine matches:
// printable keys are their text ("j", "G", "?", "<"), everything else is a
// keystroke in Bubble Tea's notation ("space", "enter", "shift+tab",
// "ctrl+d", "pgdown").
func Token(msg tea.KeyPressMsg) string {
	k := msg.Key()
	mods := k.Mod &^ (tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock)
	if k.Text != "" && k.Text != " " && mods&^tea.ModShift == 0 {
		return k.Text
	}
	return canonical(k.Keystroke())
}

// canonical rewrites a "mod+mod+key" keystroke into token form: modifiers in
// ctrl, alt, shift order, shifted letters as uppercase ("shift+g" → "G",
// "alt+J" → "alt+shift+j"), ctrl letters lowercase.
func canonical(s string) string {
	parts := strings.Split(s, "+")
	key := parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	if key == "" && len(parts) > 1 { // "ctrl++"
		key = "+"
		mods = parts[:len(parts)-2]
	}
	var ctrl, alt, shift, meta, super, hyper bool
	for _, m := range mods {
		switch strings.ToLower(m) {
		case "ctrl", "c":
			ctrl = true
		case "alt", "a", "m", "option", "opt":
			alt = true
		case "shift", "s":
			shift = true
		case "meta":
			meta = true
		case "super", "cmd", "d":
			super = true
		case "hyper":
			hyper = true
		}
	}
	if r, n := utf8.DecodeRuneInString(key); n == len(key) && unicode.IsLetter(r) {
		switch {
		case ctrl:
			key = string(unicode.ToLower(r))
		case unicode.IsUpper(r):
			shift = true
			key = string(unicode.ToLower(r))
		}
		if shift && !ctrl && !alt && !meta && !super && !hyper {
			return string(unicode.ToUpper(r))
		}
	} else if utf8.RuneCountInString(key) > 1 {
		key = strings.ToLower(key)
	}
	var b strings.Builder
	for _, m := range []struct {
		on   bool
		name string
	}{{ctrl, "ctrl+"}, {alt, "alt+"}, {shift, "shift+"}, {meta, "meta+"}, {hyper, "hyper+"}, {super, "super+"}} {
		if m.on {
			b.WriteString(m.name)
		}
	}
	b.WriteString(key)
	return b.String()
}

// namedKeys maps names accepted in config (bare or inside <…>, any case) to
// tokens.
var namedKeys = map[string]string{
	"space":     "space",
	"spc":       "space",
	"enter":     "enter",
	"return":    "enter",
	"cr":        "enter",
	"esc":       "esc",
	"escape":    "esc",
	"tab":       "tab",
	"backspace": "backspace",
	"bs":        "backspace",
	"delete":    "delete",
	"del":       "delete",
	"insert":    "insert",
	"up":        "up",
	"down":      "down",
	"left":      "left",
	"right":     "right",
	"home":      "home",
	"end":       "end",
	"pgup":      "pgup",
	"pageup":    "pgup",
	"pgdown":    "pgdown",
	"pgdn":      "pgdown",
	"pagedown":  "pgdown",
	"lt":        "<",
	"gt":        ">",
	"bar":       "|",
	"bslash":    `\`,
	"minus":     "-",
	"plus":      "+",
}

func namedKey(name string) (string, bool) {
	n := strings.ToLower(name)
	if t, ok := namedKeys[n]; ok {
		return t, true
	}
	if len(n) >= 2 && n[0] == 'f' {
		if num := n[1:]; strings.Trim(num, "0123456789") == "" && len(num) <= 2 {
			return n, true
		}
	}
	return "", false
}

const leaderToken = "\x00leader"

// ParseSequence parses a key sequence written in config notation into
// tokens. leader is the token "<leader>" stands for.
//
// Accepted forms, freely mixed: plain characters ("gg" is g then g),
// whitespace-separated keys ("g g", "space s s"), vim brackets ("<leader>ss",
// "<C-d>", "<S-Tab>", "<CR>", "<space>", "<lt>" for a literal "<"), and Bubble
// Tea keystrokes as a whole word ("ctrl+d", "shift+tab", "pgdown", "esc").
func ParseSequence(s, leader string) ([]string, error) {
	var out []string
	for _, word := range strings.Fields(s) {
		toks, err := parseWord(word)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", s, err)
		}
		out = append(out, toks...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%q: empty key sequence", s)
	}
	for i, t := range out {
		if t == leaderToken {
			if leader == "" {
				return nil, fmt.Errorf("%q: <leader> used but no leader is set", s)
			}
			out[i] = leader
		}
	}
	return out, nil
}

// bareNames are the key names accepted as a whole word without brackets.
// Short vim aliases ("bs", "cr", "lt") stay bracket-only so "bs" can still
// mean b then s.
var bareNames = map[string]bool{
	"space": true, "enter": true, "return": true, "esc": true, "escape": true,
	"tab": true, "backspace": true, "delete": true, "insert": true,
	"up": true, "down": true, "left": true, "right": true, "home": true, "end": true,
	"pgup": true, "pgdown": true, "pageup": true, "pagedown": true,
}

func parseWord(w string) ([]string, error) {
	if lw := strings.ToLower(w); bareNames[lw] || (len(lw) > 1 && lw[0] == 'f' && strings.Trim(lw[1:], "0123456789") == "") {
		if t, ok := namedKey(lw); ok {
			return []string{t}, nil
		}
	}
	if t, ok := keystroke(w); ok {
		return []string{t}, nil
	}
	var out []string
	for len(w) > 0 {
		if w[0] == '<' {
			if end := strings.IndexByte(w[1:], '>'); end > 0 {
				t, err := bracket(w[1 : 1+end])
				if err != nil {
					return nil, err
				}
				out = append(out, t)
				w = w[end+2:]
				continue
			}
		}
		r, n := utf8.DecodeRuneInString(w)
		out = append(out, string(r))
		w = w[n:]
	}
	return out, nil
}

// keystroke parses a whole word like "ctrl+d" or "shift+tab".
func keystroke(w string) (string, bool) {
	i := strings.LastIndexByte(strings.TrimSuffix(w, "+"), '+')
	if i <= 0 {
		return "", false
	}
	for _, m := range strings.Split(w[:i], "+") {
		switch strings.ToLower(m) {
		case "ctrl", "alt", "shift", "meta", "super", "hyper", "cmd", "option", "opt":
		default:
			return "", false
		}
	}
	key := w[i+1:]
	if t, ok := namedKey(key); ok && utf8.RuneCountInString(key) > 1 {
		key = t
	} else if utf8.RuneCountInString(key) != 1 {
		return "", false
	}
	return canonical(w[:i+1] + key), true
}

// bracket parses the inside of a vim-style <…> key.
func bracket(inner string) (string, error) {
	if strings.EqualFold(inner, "leader") {
		return leaderToken, nil
	}
	if t, ok := namedKey(inner); ok {
		return t, nil
	}
	parts := strings.Split(inner, "-")
	key := parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	if key == "" && len(parts) > 1 { // <C-->
		key = "-"
		mods = parts[:len(parts)-2]
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("unknown key <%s>", inner)
	}
	for _, m := range mods {
		switch strings.ToLower(m) {
		case "c", "a", "m", "s", "d":
		default:
			return "", fmt.Errorf("unknown modifier %q in <%s>", m, inner)
		}
	}
	if t, ok := namedKey(key); ok {
		key = t
	} else if utf8.RuneCountInString(key) != 1 {
		return "", fmt.Errorf("unknown key <%s>", inner)
	}
	return canonical(strings.Join(mods, "+") + "+" + key), nil
}

var keyGlyphs = map[string]string{
	"space":  "space",
	"up":     "↑",
	"down":   "↓",
	"left":   "←",
	"right":  "→",
	"pgup":   "PgUp",
	"pgdown": "PgDn",
	"home":   "Home",
	"end":    "End",
}

// DisplayToken renders one token for hints and help: "space" stays "space",
// "down" → "↓", "pgdown" → "PgDn"; others are unchanged.
func DisplayToken(tok string) string {
	if g, ok := keyGlyphs[tok]; ok {
		return g
	}
	return tok
}

// Display renders a token sequence for humans: "space s s", "g g", "ctrl+d".
func Display(tokens []string) string {
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		parts[i] = DisplayToken(t)
	}
	return strings.Join(parts, " ")
}
