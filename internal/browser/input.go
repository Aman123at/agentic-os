package browser

import (
	"errors"
	"math"
)

// Command is one message from a viewer. The fields each Type uses are listed
// beside it; everything is checked and copied into DevTools parameters here,
// so nothing a viewer sends reaches the browser verbatim.
type Command struct {
	// navigate: URL is what the user typed (see Policy.Normalize).
	// back, forward, reload, stop: no fields.
	// resize: Width, Height (CSS pixels), Scale (devicePixelRatio).
	// visible: Visible.
	// mouse: Event (down, up, move), X, Y, Button, Buttons, Clicks, Modifiers.
	// wheel: X, Y, DeltaX, DeltaY, Modifiers.
	// key: Event (down, up), Key, Code, Text, KeyCode, Modifiers, Commands.
	// text: Text, inserted as typed (paste, input methods).
	Type      string   `json:"type"`
	URL       string   `json:"url,omitempty"`
	Width     float64  `json:"width,omitempty"`
	Height    float64  `json:"height,omitempty"`
	Scale     float64  `json:"scale,omitempty"`
	Visible   bool     `json:"visible,omitempty"`
	Event     string   `json:"event,omitempty"`
	X         float64  `json:"x,omitempty"`
	Y         float64  `json:"y,omitempty"`
	Button    string   `json:"button,omitempty"`
	Buttons   int      `json:"buttons,omitempty"`
	Clicks    int      `json:"clicks,omitempty"`
	DeltaX    float64  `json:"deltaX,omitempty"`
	DeltaY    float64  `json:"deltaY,omitempty"`
	Key       string   `json:"key,omitempty"`
	Code      string   `json:"code,omitempty"`
	Text      string   `json:"text,omitempty"`
	KeyCode   int      `json:"keyCode,omitempty"`
	Modifiers int      `json:"modifiers,omitempty"`
	Commands  []string `json:"commands,omitempty"`
}

// Viewport limits, in CSS pixels, and the largest scale streamed: a 2x frame
// of a big window is already several hundred kilobytes.
const (
	minSide  = 100
	maxSide  = 4096
	maxScale = 2
)

// editing commands a key press may carry, the ones macOS shortcuts map to:
// Chromium on Linux doesn't bind ⌘A and friends itself.
var editCommands = map[string]bool{"selectAll": true, "copy": true, "cut": true, "paste": true, "undo": true, "redo": true}

var mouseEvents = map[string]string{"down": "mousePressed", "up": "mouseReleased", "move": "mouseMoved"}

var mouseButtons = map[string]bool{"none": true, "left": true, "middle": true, "right": true, "back": true, "forward": true}

// inputCall is the DevTools call for a mouse, wheel, key or text Command.
func inputCall(c Command) (method string, params map[string]any, err error) {
	if !finite(c.X, c.Y, c.DeltaX, c.DeltaY) {
		return "", nil, errors.New("coordinates must be numbers")
	}
	mods := c.Modifiers & 0b1111 // Alt, Ctrl, Meta, Shift
	switch c.Type {
	case "mouse":
		ev, ok := mouseEvents[c.Event]
		if !ok {
			return "", nil, errors.New("unknown mouse event")
		}
		button := c.Button
		if button == "" {
			button = "none"
		}
		if !mouseButtons[button] {
			return "", nil, errors.New("unknown mouse button")
		}
		return "Input.dispatchMouseEvent", map[string]any{
			"type": ev, "x": clamp(c.X, 0, maxSide), "y": clamp(c.Y, 0, maxSide), "button": button,
			"buttons": c.Buttons & 0b11111, "clickCount": int(clamp(float64(c.Clicks), 0, 3)), "modifiers": mods,
		}, nil
	case "wheel":
		return "Input.dispatchMouseEvent", map[string]any{
			"type": "mouseWheel", "x": clamp(c.X, 0, maxSide), "y": clamp(c.Y, 0, maxSide),
			"deltaX": clamp(c.DeltaX, -10000, 10000), "deltaY": clamp(c.DeltaY, -10000, 10000), "modifiers": mods,
		}, nil
	case "key":
		p := map[string]any{"key": limit(c.Key, 32), "code": limit(c.Code, 32), "modifiers": mods,
			"windowsVirtualKeyCode": int(clamp(float64(c.KeyCode), 0, 255))}
		p["nativeVirtualKeyCode"] = p["windowsVirtualKeyCode"]
		switch c.Event {
		case "down":
			// keyDown with text types it; rawKeyDown is a key that types nothing.
			p["type"] = "rawKeyDown"
			if t := limit(c.Text, 8); t != "" {
				p["type"], p["text"], p["unmodifiedText"] = "keyDown", t, t
			}
			var cmds []string
			for _, cmd := range c.Commands {
				if editCommands[cmd] {
					cmds = append(cmds, cmd)
				}
			}
			if len(cmds) > 0 {
				p["commands"] = cmds
			}
		case "up":
			p["type"] = "keyUp"
		default:
			return "", nil, errors.New("unknown key event")
		}
		return "Input.dispatchKeyEvent", p, nil
	case "text":
		if c.Text == "" {
			return "", nil, errors.New("no text")
		}
		return "Input.insertText", map[string]any{"text": limit(c.Text, 1<<16)}, nil
	}
	return "", nil, errors.New("unknown command " + limit(c.Type, 32))
}

// viewport clamps a resize Command.
func viewport(c Command) (w, h int, scale float64, err error) {
	if !finite(c.Width, c.Height, c.Scale) {
		return 0, 0, 0, errors.New("the size must be numbers")
	}
	w = int(clamp(c.Width, minSide, maxSide))
	h = int(clamp(c.Height, minSide, maxSide))
	scale = clamp(c.Scale, 1, maxScale)
	return w, h, scale, nil
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// limit cuts s to at most n bytes, on a rune boundary.
func limit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
