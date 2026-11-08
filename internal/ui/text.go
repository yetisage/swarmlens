package ui

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ansiSegs turns text containing ANSI colour sequences into styled
// segments, so a container's coloured output keeps its colours. Sequences
// other than colours are dropped; they must never reach the canvas.
func ansiSegs(s string, base style) []seg {
	var out []seg
	cur := base
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, seg{buf.String(), cur})
			buf.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c != 0x1b {
			buf.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[': // CSI
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				i = len(s)
				continue
			}
			if s[j] == 'm' {
				flush()
				cur = applySGR(cur, base, s[i+2:j])
			}
			i = j + 1
		case ']': // OSC, ended by BEL or ST
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			switch {
			case j >= len(s):
				i = len(s)
			case s[j] == 0x07:
				i = j + 1
			default:
				i = j + 2
			}
		default:
			i += 2
		}
	}
	flush()
	return out
}

func applySGR(cur, base style, params string) style {
	if params == "" {
		return base
	}
	codes := strings.Split(params, ";")
	num := func(i int) int {
		if i >= len(codes) {
			return 0
		}
		n, _ := strconv.Atoi(codes[i])
		return n
	}
	for i := 0; i < len(codes); i++ {
		n := num(i)
		switch {
		case n == 0:
			cur = base
		case n == 1:
			cur.attrs |= attrBold
		case n == 2:
			cur.attrs |= attrDim
		case n == 3:
			cur.attrs |= attrItalic
		case n == 4:
			cur.attrs |= attrUnderline
		case n == 7:
			cur.attrs |= attrReverse
		case n == 22:
			cur.attrs &^= attrBold | attrDim
		case n == 23:
			cur.attrs &^= attrItalic
		case n == 24:
			cur.attrs &^= attrUnderline
		case n == 27:
			cur.attrs &^= attrReverse
		case n >= 30 && n <= 37:
			cur.fg = palette(n - 30)
		case n == 39:
			cur.fg = base.fg
		case n >= 40 && n <= 47:
			cur.bg = palette(n - 40)
		case n == 49:
			cur.bg = base.bg
		case n >= 90 && n <= 97:
			cur.fg = palette(8 + n - 90)
		case n >= 100 && n <= 107:
			cur.bg = palette(8 + n - 100)
		case n == 38 || n == 48:
			var col color
			switch num(i + 1) {
			case 5:
				col = palette(num(i + 2))
				i += 2
			case 2:
				col = color(num(i+2)<<16 | num(i+3)<<8 | num(i+4))
				i += 4
			default:
				continue
			}
			if n == 38 {
				cur.fg = col
			} else {
				cur.bg = col
			}
		}
	}
	return cur
}

// yamlSegs colours one line of YAML, or of a describe report, the way
// k9s' viewer does: keys, colons and values each get their own colour.
func yamlSegs(line string, t *theme) []seg {
	key := t.s(t.yaml.key).bold()
	colon := t.s(t.yaml.colon)
	val := t.s(t.yaml.value)

	body := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(body)]
	out := []seg{{indent, t.base()}}

	if strings.HasPrefix(body, "#") {
		return append(out, seg{body, t.s(cGray)})
	}
	if strings.HasPrefix(body, "- ") || body == "-" {
		out = append(out, seg{"-", key})
		body = strings.TrimPrefix(body, "-")
		sp := len(body) - len(strings.TrimLeft(body, " "))
		out = append(out, seg{body[:sp], t.base()})
		body = body[sp:]
	}
	if i := keyEnd(body); i >= 0 {
		out = append(out, seg{body[:i], key}, seg{":", colon})
		rest := body[i+1:]
		sp := len(rest) - len(strings.TrimLeft(rest, " "))
		out = append(out, seg{rest[:sp], t.base()})
		if v := rest[sp:]; v != "" {
			out = append(out, seg{v, val})
		}
		return out
	}
	if body != "" {
		out = append(out, seg{body, val})
	}
	return out
}

// keyEnd returns the index of the colon ending a "key:" prefix, or -1.
// A colon only ends a key when followed by a space or the end of the line,
// so "http://x" or "a=b:c" are values, not keys.
func keyEnd(s string) int {
	if s == "" || strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "'") || strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		return -1
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ':' && (i+1 == len(s) || s[i+1] == ' ') {
			return i
		}
	}
	return -1
}

// highlight restyles the parts of a line that match re.
func highlight(ss []seg, re *regexp.Regexp, hl style) []seg {
	if re == nil || len(ss) == 0 {
		return ss
	}
	var full strings.Builder
	for _, s := range ss {
		full.WriteString(s.s)
	}
	text := full.String()
	locs := re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return ss
	}
	var out []seg
	pos := 0 // byte offset of the current segment
	for _, s := range ss {
		start, end := pos, pos+len(s.s)
		cut := start
		for _, l := range locs {
			if l[1] <= start || l[0] >= end || l[0] == l[1] {
				continue
			}
			a, b := max(l[0], start), min(l[1], end)
			if a > cut {
				out = append(out, seg{text[cut:a], s.st})
			}
			out = append(out, seg{text[a:b], hl})
			cut = b
		}
		if cut < end {
			out = append(out, seg{text[cut:end], s.st})
		}
		pos = end
	}
	return out
}

// compileFilter builds the case-insensitive regexp behind "/" filters. An
// invalid pattern falls back to a literal match so typing "(" does not break
// the view.
func compileFilter(q string) *regexp.Regexp {
	if q == "" {
		return nil
	}
	if re, err := regexp.Compile("(?i)" + q); err == nil {
		return re
	}
	return regexp.MustCompile("(?i)" + regexp.QuoteMeta(q))
}

// naturalLess orders strings case-insensitively with digit runs compared
// as numbers, so web.2 sorts before web.10.
func naturalLess(a, b string) bool {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if unicode.IsDigit(ra[i]) && unicode.IsDigit(rb[j]) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ra[i] != rb[j] {
			return ra[i] < rb[j]
		}
		i++
		j++
	}
	return len(ra)-i < len(rb)-j
}
