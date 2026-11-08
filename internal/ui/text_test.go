package ui

import (
	"regexp"
	"strings"
	"testing"
)

func plain(ss []seg) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString(s.s)
	}
	return b.String()
}

func TestANSISegsKeepsColoursAndDropsOtherSequences(t *testing.T) {
	base := style{fg: cWhite, bg: noColor}
	ss := ansiSegs("\x1b[31mred\x1b[0m plain \x1b[1;38;2;1;2;3mrgb\x1b[39m!\x1b[2J\x1b]0;title\x07end", base)
	if got := plain(ss); got != "red plain rgb!end" {
		t.Fatalf("text = %q", got)
	}
	if ss[0].s != "red" || ss[0].st.fg != palette(1) {
		t.Errorf("first seg = %+v", ss[0])
	}
	if ss[1].s != " plain " || ss[1].st != base {
		t.Errorf("reset must go back to the base style, got %+v", ss[1])
	}
	if rgb := ss[2]; rgb.s != "rgb" || rgb.st.fg != color(0x010203) || rgb.st.attrs&attrBold == 0 {
		t.Errorf("truecolour seg = %+v", rgb)
	}
	if after := ss[3]; after.st.fg != base.fg {
		t.Errorf("39 must restore the default foreground: %+v", after)
	}
}

func TestANSISegsHandlesTruncatedInput(t *testing.T) {
	for _, in := range []string{"\x1b", "abc\x1b[", "abc\x1b[3", "\x1b]0;never ends", "ok\x1b"} {
		_ = ansiSegs(in, style{}) // must not panic or loop
	}
	if got := plain(ansiSegs("héllo wörld ✓", style{})); got != "héllo wörld ✓" {
		t.Errorf("multi-byte text mangled: %q", got)
	}
}

func TestANSI256AndBrightColours(t *testing.T) {
	ss := ansiSegs("\x1b[38;5;208mo\x1b[92mg\x1b[104mb", style{})
	if ss[0].st.fg != palette(208) || ss[1].st.fg != palette(10) || ss[2].st.bg != palette(12) {
		t.Errorf("segs = %+v", ss)
	}
}

func TestYAMLSegsColoursKeysColonsAndValues(t *testing.T) {
	th := stockTheme()
	cases := []struct {
		line string
		want []string // text of the coloured parts, in order
	}{
		{"name: web", []string{"name", ":", "web"}},
		{"  image: nginx:1.25", []string{"image", ":", "nginx:1.25"}},
		{"- name: nginx", []string{"-", "name", ":", "nginx"}},
		{"Update Config:", []string{"Update Config", ":"}},
		{"  - plain item", []string{"-", "plain item"}},
		{"url: http://example.com", []string{"url", ":", "http://example.com"}},
		{"  - http://example.com", []string{"-", "http://example.com"}},
		{"# comment", []string{"# comment"}},
	}
	for _, c := range cases {
		var got []string
		for _, s := range yamlSegs(c.line, th) {
			if strings.TrimSpace(s.s) != "" {
				got = append(got, strings.TrimSpace(s.s))
			}
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q → %q, want %q", c.line, got, c.want)
		}
		if p := plain(yamlSegs(c.line, th)); p != c.line {
			t.Errorf("%q was not preserved verbatim, got %q", c.line, p)
		}
	}
	segs := yamlSegs("key: value", th)
	var key, val seg
	for _, s := range segs {
		switch s.s {
		case "key":
			key = s
		case "value":
			val = s
		}
	}
	if key.st.fg != cSteelBlue || key.st.attrs&attrBold == 0 || val.st.fg != cPapayaWhip {
		t.Errorf("colours: key=%+v value=%+v (want steelblue bold / papayawhip, as in k9s)", key.st, val.st)
	}
}

func TestHighlightSplitsSegments(t *testing.T) {
	hl := style{fg: cBlack, bg: cAqua}
	base := style{fg: cWhite}
	out := highlight([]seg{{"hello wor", base}, {"ld", style{fg: cGray}}}, regexp.MustCompile("o w|ld"), hl)
	if plain(out) != "hello world" {
		t.Fatalf("text changed: %q", plain(out))
	}
	var marked []string
	for _, s := range out {
		if s.st == hl {
			marked = append(marked, s.s)
		}
	}
	if strings.Join(marked, "|") != "o w|ld" {
		t.Errorf("highlighted %q, want [o w, ld] (matches may span nothing but must cover segments)", marked)
	}
	if got := highlight([]seg{{"abc", base}}, nil, hl); len(got) != 1 || got[0].st != base {
		t.Errorf("nil regexp must leave the segments alone")
	}
}

func TestCompileFilterIsCaseInsensitiveAndForgiving(t *testing.T) {
	if re := compileFilter("SHOP"); !re.MatchString("a shop_web") {
		t.Error("filters are case-insensitive")
	}
	if re := compileFilter("web$"); !re.MatchString("shop_web") || re.MatchString("web_shop") {
		t.Error("regular expressions are honoured")
	}
	re := compileFilter("(")
	if re == nil || !re.MatchString("a (paren") {
		t.Error("an invalid regexp must fall back to a literal match instead of breaking the view")
	}
	if compileFilter("") != nil {
		t.Error("the empty filter matches everything")
	}
}

func TestNaturalLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"web.2", "web.10", true},
		{"web.10", "web.2", false},
		{"Alpha", "beta", true},
		{"a01", "a1", false},
		{"a1", "a01", false},
		{"node-9", "node-10", true},
		{"abc", "abcd", true},
		{"", "a", true},
		{"same", "same", false},
	} {
		if got := naturalLess(c.a, c.b); got != c.less {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}

func TestWrapAndCutSegs(t *testing.T) {
	ss := []seg{{"abcd", style{fg: cAqua}}, {"efgh", style{fg: cWhite}}}
	rows := wrapSegs(ss, 3)
	var got []string
	for _, r := range rows {
		got = append(got, plain(r))
	}
	if strings.Join(got, "|") != "abc|def|gh" {
		t.Errorf("wrapSegs = %q", got)
	}
	if got := plain(cutSegs(ss, 2, 4)); got != "cdef" {
		t.Errorf("cutSegs = %q", got)
	}
	if got := plain(cutSegs(ss, 6, 10)); got != "gh" {
		t.Errorf("cutSegs tail = %q", got)
	}
}

func TestKeyLabels(t *testing.T) {
	for k, want := range map[string]string{
		"ctrl+d": "<ctrl-d>", "enter": "<enter>", "N": "<shift-n>", "d": "<d>", " ": "<space>",
		"ctrl+@": "<ctrl-space>", "shift+left": "<shift-left>", "?": "<?>", "0": "<0>", "esc": "<esc>",
	} {
		if got := keyLabel(k); got != want {
			t.Errorf("keyLabel(%q) = %q, want %q", k, got, want)
		}
	}
}

func TestExpandPluginVariables(t *testing.T) {
	vars := map[string]string{"NAME": "web.1", "NAMESPACE": "shop", "COL-NODE": "worker-1", "COL": "x"}
	got := expand("logs $NAME in ${NAMESPACE} on $COL-NODE ($COL)", vars)
	if got != "logs web.1 in shop on worker-1 (x)" {
		t.Errorf("expand = %q", got)
	}
}
