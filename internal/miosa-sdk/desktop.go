package miosa

import (
	"context"
	"fmt"
)

// ─── Screenshot ───────────────────────────────────────────────────────────────

// Screenshot captures the current desktop and returns the raw PNG bytes.
func (c *Computer) Screenshot(ctx context.Context) ([]byte, error) {
	data, contentType, err := c.client.getRaw(ctx, fmt.Sprintf("/computers/%s/desktop/screenshot", c.ID))
	if err != nil {
		return nil, err
	}
	_ = contentType // callers inspect if needed
	return data, nil
}

// ─── Mouse ────────────────────────────────────────────────────────────────────

// Click sends a mouse click at (x, y). Button defaults to left.
func (c *Computer) Click(ctx context.Context, x, y int, button ...MouseButton) error {
	input := ClickInput{X: x, Y: y}
	if len(button) > 0 {
		input.Button = button[0]
	}
	return c.desktopPost(ctx, "click", input)
}

// DoubleClick sends a double-click at (x, y).
func (c *Computer) DoubleClick(ctx context.Context, x, y int) error {
	return c.desktopPost(ctx, "double-click", DoubleClickInput{X: x, Y: y})
}

// RightClick is a convenience wrapper around Click with ButtonRight.
func (c *Computer) RightClick(ctx context.Context, x, y int) error {
	return c.Click(ctx, x, y, ButtonRight)
}

// Drag moves the mouse from one position to another while holding the button.
func (c *Computer) Drag(ctx context.Context, fromX, fromY, toX, toY int) error {
	return c.desktopPost(ctx, "drag", DragInput{
		FromX: fromX, FromY: fromY,
		ToX: toX, ToY: toY,
	})
}

// Scroll scrolls in the given direction by the given number of clicks.
// Clicks defaults to 3 when zero.
func (c *Computer) Scroll(ctx context.Context, direction ScrollDirection, clicks ...int) error {
	input := ScrollInput{Direction: direction}
	if len(clicks) > 0 && clicks[0] > 0 {
		input.Clicks = clicks[0]
	}
	return c.desktopPost(ctx, "scroll", input)
}

// ScrollAt scrolls at a specific (x, y) position.
func (c *Computer) ScrollAt(ctx context.Context, x, y int, direction ScrollDirection, clicks int) error {
	input := ScrollInput{
		X:         &x,
		Y:         &y,
		Direction: direction,
		Clicks:    clicks,
	}
	return c.desktopPost(ctx, "scroll", input)
}

// ─── Keyboard ────────────────────────────────────────────────────────────────

// Type sends the given text to the focused element.
// Optional delay sets milliseconds between keystrokes (default: API decides).
func (c *Computer) Type(ctx context.Context, text string, delayMs ...int) error {
	input := TypeInput{Text: text}
	if len(delayMs) > 0 && delayMs[0] > 0 {
		input.Delay = delayMs[0]
	}
	return c.desktopPost(ctx, "type", input)
}

// Key sends a single key or key combination, e.g. "Enter", "ctrl+c".
func (c *Computer) Key(ctx context.Context, key string) error {
	return c.desktopPost(ctx, "key", KeyInput{Key: key})
}

// ─── Desktop state ────────────────────────────────────────────────────────────

// Wait pauses execution on the remote computer for the given duration.
// This is a server-side sleep, not a local one.
func (c *Computer) DesktopWait(ctx context.Context, seconds float64) error {
	return c.desktopPost(ctx, "wait", WaitInput{Seconds: seconds})
}

// Windows returns the list of open windows on the desktop.
func (c *Computer) Windows(ctx context.Context) ([]WindowInfo, error) {
	var out []WindowInfo
	if err := c.client.getJSON(ctx, fmt.Sprintf("/computers/%s/desktop/windows", c.ID), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Cursor returns the current cursor position.
func (c *Computer) Cursor(ctx context.Context) (*CursorInfo, error) {
	var out CursorInfo
	if err := c.client.getJSON(ctx, fmt.Sprintf("/computers/%s/desktop/cursor", c.ID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FocusWindow brings the given window to the foreground.
func (c *Computer) FocusWindow(ctx context.Context, windowID string) error {
	return c.desktopPost(ctx, "window/focus", WindowFocusInput{WindowID: windowID})
}

// Launch opens an application by name.
func (c *Computer) Launch(ctx context.Context, appName string) error {
	return c.desktopPost(ctx, "launch", LaunchInput{AppName: appName})
}

// ─── Internal helper ─────────────────────────────────────────────────────────

// desktopPost is a convenience wrapper for POST /computers/{id}/desktop/{action}.
func (c *Computer) desktopPost(ctx context.Context, action string, body interface{}) error {
	var out DesktopActionResult
	path := fmt.Sprintf("/computers/%s/desktop/%s", c.ID, action)
	if err := c.client.postJSON(ctx, path, body, &out); err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("desktop action %q returned success=false", action)
	}
	return nil
}
