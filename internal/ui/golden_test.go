package ui

import "testing"

// The golden screens pin the layout this redesign is about: the header, the
// framed tables, dialogs, prompts. Review the diff when one changes, and
// refresh them with `go test ./internal/ui -run Golden -update`.
func TestGoldenScreens(t *testing.T) {
	for _, g := range []struct {
		name string
		w, h int
		do   func(a *app)
	}{
		{"services", 140, 38, func(a *app) {}},
		{"services-wide", 200, 30, func(a *app) { a.press("ctrl+w") }},
		{"services-small", 80, 24, func(a *app) {}},
		{"services-stack", 140, 24, func(a *app) { a.press("3") }},
		{"services-sorted-faults", 140, 24, func(a *app) { a.press("R", "R"); a.press("ctrl+z") }},
		{"nodes", 140, 24, func(a *app) { a.command("nodes") }},
		{"tasks-failing-service", 140, 24, func(a *app) { a.command("svc /shop_worker"); a.press("enter") }},
		{"stacks", 140, 24, func(a *app) { a.command("stacks") }},
		{"networks", 140, 24, func(a *app) { a.command("net") }},
		{"describe-service", 140, 30, func(a *app) { a.command("svc /shop_web"); a.press("d") }},
		{"yaml-node", 140, 30, func(a *app) { a.command("nodes"); a.press("y") }},
		{"logs-service", 140, 24, func(a *app) { a.command("svc /shop_web"); a.press("l") }},
		{"logs-ansi-filtered", 140, 24, func(a *app) {
			a.command("svc /shop_worker")
			a.press("l")
			a.press("/")
			a.typeText("ERROR")
			a.press("enter")
		}},
		{"help", 140, 38, func(a *app) { a.press("?") }},
		{"aliases", 140, 24, func(a *app) { a.press("ctrl+a") }},
		{"prompt-command", 140, 24, func(a *app) { a.press(":"); a.typeText("no") }},
		{"prompt-filter", 140, 24, func(a *app) { a.press("/"); a.typeText("shop_") }},
		{"dialog-delete", 140, 24, func(a *app) { a.command("svc /shop_db"); a.press("ctrl+d") }},
		{"dialog-scale", 140, 24, func(a *app) { a.command("svc /shop_web"); a.press("s") }},
		{"dialog-error", 140, 38, func(a *app) { a.command("nope") }},
		{"dialog-error-short", 100, 20, func(a *app) { a.command("nope") }},
		{"readonly", 140, 24, func(a *app) {}},
		{"contexts", 140, 24, func(a *app) { a.command("ctx") }},
	} {
		t.Run(g.name, func(t *testing.T) {
			opt := testOpts()
			opt.ReadOnly = g.name == "readonly"
			a := newTestAppOpts(t, opt, g.w, g.h)
			g.do(a)
			golden(t, g.name, a.screenText())
		})
	}
}
