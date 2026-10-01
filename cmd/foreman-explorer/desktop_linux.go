package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/godbus/dbus/v5"
)

// The tray icon is the foreman tile (light mark on a dark rounded square):
// Linux trays don't recolor template images, and the tile reads on dark and
// light panels alike. Rendered from docs/assets/logo/logo-tile.svg at 64px.
//
//go:embed icon-tile.png
var iconTile []byte

// init names the tray item before systray.Run registers it: the
// StatusNotifierItem Id is the title, and defaults to systray_<pid>, which
// changes every run so KDE can't remember the item's visibility.
func init() {
	systray.SetTitle("foreman")
}

func setTrayIcon() {
	systray.SetIcon(iconTile)
}

// runZenity runs a zenity dialog; a seam so tests can fake it. zenity
// (installed by default on Ubuntu, `dnf install zenity` on Fedora) exits 0
// only on the confirming button — cancel is 1, the --timeout give-up 5.
var runZenity = func(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "zenity", args...).Output()
	if errors.Is(err, exec.ErrNotFound) {
		log.Printf("explorer dialogs need zenity: %v", err)
	}
	return out, err
}

// confirmDone asks before teardown — "closing is harmless, ending is
// explicit": a menu misclick must never kill a participant (issue #72).
// Same contract as on macOS: the strings travel as arguments (#30), the
// dialog gives itself up after 60s with a Go-side backstop (#62), and
// anything but an explicit Done answers no.
func confirmDone(project, role string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	_, err := runZenity(ctx, "--question", "--title=foreman",
		"--text="+fmt.Sprintf("End %s in project %s? Its terminal closes for real.", role, project),
		"--ok-label=Done", "--cancel-label=Cancel", "--default-cancel", "--no-markup", "--timeout=60")
	return err == nil
}

// promptText asks for a line of text in a dialog, prefilled with def; ok
// labels the confirming button. Anything but an explicit ok — cancel,
// give-up, timeout, error, an emptied field — returns "" and means no.
func promptText(msg, def, ok string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	out, err := runZenity(ctx, "--entry", "--title=foreman", "--text="+msg, "--no-markup",
		"--entry-text="+def, "--ok-label="+ok, "--timeout=60")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// notifyBus is a session-bus connection of notify's own. systray's shared
// one must not carry it: some daemons (dunst) send NotificationClosed to the
// posting connection, and systray's re-registration watcher stops on the
// first signal it doesn't expect — a tray restart would then lose the icon.
var notifyBus = sync.OnceValues(func() (*dbus.Conn, error) { return dbus.ConnectSessionBus() })

// notify posts a desktop notification over the session bus. Bounded (#62):
// a bus without a notification daemon would otherwise stall the refresher
// for D-Bus's 25s activation timeout.
func notify(msg string) {
	conn, err := notifyBus()
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	call := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").CallWithContext(ctx,
		"org.freedesktop.Notifications.Notify", 0,
		"foreman", uint32(0), "", "foreman", msg, []string{}, map[string]dbus.Variant{}, int32(-1))
	if call.Err != nil {
		log.Printf("notify: %v", call.Err)
	}
}
