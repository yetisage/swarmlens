package ui

// X11 colour values used by the stock look. They are 24-bit values rather
// than terminal palette entries, so the UI looks the same everywhere.
const (
	cBlack        color = 0x000000
	cWhite        color = 0xffffff
	cAqua         color = 0x00ffff
	cOrange       color = 0xffa500
	cDodgerBlue   color = 0x1e90ff
	cLightSkyBlue color = 0x87cefa
	cFuchsia      color = 0xff00ff
	cPapayaWhip   color = 0xffefd5
	cSeaGreen     color = 0x2e8b57
	cSteelBlue    color = 0x4682b4
	cCadetBlue    color = 0x5f9ea0
	cOrangeRed    color = 0xff4500
	cGreenYellow  color = 0xadff2f
	cMediumPurple color = 0x9370db
	cGray         color = 0x808080
	cPaleGreen    color = 0x98fb98
	cLimeGreen    color = 0x32cd32
	cGreen        color = 0x008000
	cFlashInfo    color = 0xffd7af
	cFlashWarn    color = 0xffaf00
	cFlashErr     color = 0xff5f00
)

// theme holds every colour the UI uses. Its shape follows a k9s skin, and
// stockTheme reproduces k9s' stock skin.
type theme struct {
	body struct{ fg, bg, logo color }

	prompt struct {
		fg, bg, suggest         color
		borderCmd, borderFilter color
	}

	info struct{ label, value, warn color }

	dialog struct {
		fg, bg                       color
		buttonFg, buttonBg           color
		buttonFocusFg, buttonFocusBg color
		labelFg, fieldFg             color
		errorFg                      color
	}

	frame struct {
		border, focus color
		menu          struct{ fg, key, numKey color }
		crumbs        struct{ fg, bg, active color }
		status        struct{ add, modify, err, pending, highlight, kill, completed color }
		title         struct{ fg, highlight, counter, filter color }
	}

	table struct {
		fg, cursorFg, cursorBg, mark color
		header                       struct{ fg, sorted, sorter color }
		alias                        struct{ fg, cursorBg color }
	}

	help struct{ section, key, desc color }

	yaml struct{ key, colon, value color }

	logs struct {
		fg                 color
		indicator, on, off color
		prefix             []color
	}

	flash struct{ info, warn, err color }
}

// stockTheme is the look of k9s out of the box: black screen, aqua rows,
// dodger blue frames, orange chrome.
func stockTheme() *theme {
	t := &theme{}
	t.body.fg, t.body.bg, t.body.logo = cCadetBlue, cBlack, cOrange

	t.prompt.fg, t.prompt.bg, t.prompt.suggest = cCadetBlue, cBlack, cDodgerBlue
	t.prompt.borderCmd, t.prompt.borderFilter = cAqua, cSeaGreen

	t.info.label, t.info.value, t.info.warn = cOrange, cWhite, cFlashErr

	t.dialog.fg, t.dialog.bg = cCadetBlue, cBlack
	t.dialog.buttonFg, t.dialog.buttonBg = cCadetBlue, cBlack
	t.dialog.buttonFocusFg, t.dialog.buttonFocusBg = cBlack, cDodgerBlue
	t.dialog.labelFg, t.dialog.fieldFg = cWhite, cWhite
	t.dialog.errorFg = cFlashErr

	t.frame.border, t.frame.focus = cDodgerBlue, cLightSkyBlue
	t.frame.menu.fg, t.frame.menu.key, t.frame.menu.numKey = cWhite, cDodgerBlue, cFuchsia
	t.frame.crumbs.fg, t.frame.crumbs.bg, t.frame.crumbs.active = cBlack, cAqua, cOrange
	t.frame.status.add = cDodgerBlue
	t.frame.status.modify = cGreenYellow
	t.frame.status.err = cOrangeRed
	t.frame.status.pending = cOrange
	t.frame.status.highlight = cAqua
	t.frame.status.kill = cMediumPurple
	t.frame.status.completed = cGray
	t.frame.title.fg, t.frame.title.highlight = cAqua, cFuchsia
	t.frame.title.counter, t.frame.title.filter = cPapayaWhip, cSeaGreen

	t.table.fg, t.table.cursorFg, t.table.cursorBg, t.table.mark = cAqua, cBlack, cAqua, cPaleGreen
	t.table.header.fg, t.table.header.sorted, t.table.header.sorter = cWhite, cLightSkyBlue, cAqua
	t.table.alias.fg, t.table.alias.cursorBg = cLightSkyBlue, cLightSkyBlue

	t.help.section, t.help.key, t.help.desc = cGreen, cDodgerBlue, cCadetBlue

	t.yaml.key, t.yaml.colon, t.yaml.value = cSteelBlue, cWhite, cPapayaWhip

	t.logs.fg = cWhite
	t.logs.indicator, t.logs.on, t.logs.off = cDodgerBlue, cLimeGreen, cGray
	// Colours that tell the tasks of a service apart in aggregated logs.
	t.logs.prefix = []color{
		0x00bfff, 0xffa500, 0x7fff00, 0xff69b4, 0x00ffff, 0xffd700,
		0xba55d3, 0x98fb98, 0xff7f50, 0x87cefa, 0xf0e68c, 0xdda0dd,
	}

	t.flash.info, t.flash.warn, t.flash.err = cFlashInfo, cFlashWarn, cFlashErr
	return t
}

// s is "this colour on the body background".
func (t *theme) s(fg color) style { return style{fg: fg, bg: t.body.bg} }

// base is the style of blank space and plain text.
func (t *theme) base() style { return t.s(t.body.fg) }

// role says how a row should be coloured, mirroring k9s' row colourers.
type role uint8

const (
	roleStd role = iota
	roleAdd
	roleMod
	roleErr
	rolePending
	roleHighlight
	roleKill
	roleCompleted
)

func (t *theme) roleColor(r role) color {
	switch r {
	case roleAdd:
		return t.frame.status.add
	case roleMod:
		return t.frame.status.modify
	case roleErr:
		return t.frame.status.err
	case rolePending:
		return t.frame.status.pending
	case roleHighlight:
		return t.frame.status.highlight
	case roleKill:
		return t.frame.status.kill
	case roleCompleted:
		return t.frame.status.completed
	}
	return t.table.fg
}

// flashLevel is the severity of a flash message.
type flashLevel uint8

const (
	flashInfo flashLevel = iota
	flashWarn
	flashErr
)
