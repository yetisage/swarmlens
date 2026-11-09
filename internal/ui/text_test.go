package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/muesli/termenv"
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

// ---- container output is untrusted ----

var sgrOnly = regexp.MustCompile(`^\x1b\[[0-9;]*m`)

// renderLine pushes a log line through everything that stands between a
// container's output and the terminal, and returns what would be written.
func renderLine(in string) string {
	th := stockTheme()
	base := th.s(th.logs.fg)
	segs := ansiSegs(in, base)
	c := newCanvas(80, 6, th.base(), termenv.TrueColor)
	p := c.painter()
	p.segs(0, 0, highlight(segs, regexp.MustCompile("(?i)a|é|."), style{fg: cBlack, bg: th.table.cursorBg}))
	for i, row := range wrapSegs(segs, 17) {
		p.segs(0, 1+i%4, row)
	}
	p.segs(0, 5, cutSegs(segs, 3, 11))
	p.text(0, 5, ellipsize(in, 25), base)
	return c.String()
}

// onlyStyling fails the test if the output holds anything but SGR sequences.
func onlyStyling(t *testing.T, in, out string) {
	t.Helper()
	for i := 0; i < len(out); i++ {
		switch ch := out[i]; {
		case ch == 0x1b:
			if !sgrOnly.MatchString(out[i:]) {
				t.Fatalf("a non-SGR escape reached the terminal for %q: %q", in, out[i:min(i+24, len(out))])
			}
		case ch == '\n':
		case ch < 0x20 || ch == 0x7f:
			t.Fatalf("control byte %#x reached the terminal for %q", ch, in)
		}
	}
	if strings.ContainsAny(out, "\u0080\u0090\u009b\u009c\u009d\u009f") {
		t.Fatalf("a C1 control reached the terminal for %q", in)
	}
}

var hostileLines = []string{
	// colours outside what a terminal can show: these used to panic the renderer
	"\x1b[38;2;-5;0;0m negative red",
	"\x1b[48;2;0;0;-9m negative blue background",
	"\x1b[38;2;99999999;0;0m huge red",
	"\x1b[38;2;99999999999999999999;0;0m not even an int",
	"\x1b[38;2;256;256;256m just over",
	"\x1b[38;5;99999m huge palette index",
	"\x1b[38;5;-3m negative palette index",
	"\x1b[38;2;1;2m truncated truecolour",
	"\x1b[38;5m truncated palette",
	"\x1b[38m \x1b[48m bare",
	"\x1b[38:2::-5:0:0m colon form",
	"\x1b[38:2:1:2:3m colon form without a colour space",
	"\x1b[38:5:999m colon form index",
	"\x1b[" + strings.Repeat("9;", 5000) + "m very long",
	"\x1b[;;;m empty parameters",
	"\x1b[abc;1m not numbers",
	// sequences that are not styling must never get through
	"\x1b]52;c;SGVsbG8=\x07 clipboard",
	"\x1b]0;title\x07 retitle",
	"\x1b[21t \x1b[6n \x1b[?1049h \x1b[2J \x1b[H",
	"\x1bP+q544e\x1b\\ dcs",
	"\x1b_Gf=100;AAAA\x1b\\ apc",
	"\xc2\x9b31m c1 csi \xc2\x9d0;x\x07 c1 osc",
	"a\rb\x08c\x7fd\x00e\x0bf\x0cg",
	string(rune(0x202e)) + " rtl override " + string(rune(0x2066)) + " isolate",
	// truncated escapes
	"\x1b", "\x1b[", "\x1b]", "\x1b[31", "\x1b]0;never ends",
	"\xff\xfe not utf-8 \xc3",
}

func TestHostileLogLinesNeverBreakTheRenderer(t *testing.T) {
	for _, in := range hostileLines {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("rendering %q panicked: %v", in, r)
				}
			}()
			onlyStyling(t, in, renderLine(in))
		}()
	}
}

func TestANSIColourParametersAreClamped(t *testing.T) {
	base := style{fg: cWhite, bg: noColor}
	cases := []struct {
		in   string
		want color
	}{
		{"\x1b[38;2;256;0;0mx", color(0xff0000)},
		{"\x1b[38;2;-5;0;300mx", color(0x0000ff)},
		{"\x1b[38;5;999mx", palette(255)},
		{"\x1b[38;5;-3mx", palette(0)},
		{"\x1b[38:2::1:2:3mx", color(0x010203)},
		{"\x1b[38:2:1:2:3mx", color(0x010203)},
		{"\x1b[38:5:208mx", palette(208)},
	}
	for _, c := range cases {
		ss := ansiSegs(c.in, base)
		if len(ss) != 1 || ss[0].st.fg != c.want {
			t.Errorf("%q: got %+v, want fg %#x", c.in, ss, int(c.want))
		}
	}
	// an incomplete colour is skipped, it must not leave a half-made one behind
	if ss := ansiSegs("\x1b[38;2;1;2mx", base); len(ss) != 1 || ss[0].st.fg != base.fg {
		t.Errorf("a truncated truecolour must be ignored, got %+v", ss)
	}
	// garbage parameters are ignored instead of resetting the style
	if ss := ansiSegs("\x1b[31m\x1b[abcmx", base); len(ss) != 1 || ss[0].st.fg != palette(1) {
		t.Errorf("an unparsable parameter must not reset the colour, got %+v", ss)
	}
	// an empty parameter is a 0, as in ECMA-48
	if ss := ansiSegs("\x1b[31m\x1b[mx", base); len(ss) != 1 || ss[0].st != base {
		t.Errorf("ESC[m must reset, got %+v", ss)
	}
}

func TestANSIUnderlineSubParameter(t *testing.T) {
	ss := ansiSegs("\x1b[4:3mcurly\x1b[4:0mplain", style{})
	if len(ss) != 2 || ss[0].st.attrs&attrUnderline == 0 || ss[1].st.attrs&attrUnderline != 0 {
		t.Errorf("segs = %+v", ss)
	}
}

// FuzzANSIRender adds random inputs to the table above. Alone it does not
// find the colour bug quickly, so the table is the guard and this is the net.
func FuzzANSIRender(f *testing.F) {
	for _, s := range hostileLines {
		f.Add(s)
	}
	f.Add("\x1b[31mred\x1b[0m plain 日本語 \x1b[1;4mbold")
	f.Fuzz(func(t *testing.T, in string) { onlyStyling(t, in, renderLine(in)) })
}
