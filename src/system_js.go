//go:build js

package main

import (
	"errors"
	"image"
	"math"
	"sync"
	"syscall/js"
	"time"
	"unicode/utf8"
)

// Browser implementation of the window layer. The "window" is a canvas element
// (id "ikemen-canvas") provided by the host page. This file provides the same names
// as system_sdl.go.

// Largest drawing buffer dimension. Bigger canvases are rendered smaller and scaled up by the browser.
const jsMaxCanvasDim = 1920

type Window struct {
	canvas          js.Value
	gl              js.Value
	title           string
	w, h            int
	fullscreen      bool
	closeflag       bool
	focused         bool
	renderingPaused bool
	lastYield       time.Time
}

// Placeholder for the value returned by GLCreateContext. The WebGL context is kept in the Window.
type GLContext struct{}

type jsEventKind int

const (
	jsEvKeyDown jsEventKind = iota
	jsEvKeyUp
	jsEvText
	jsEvFocus
	jsEvBlur
	jsEvFullscreen
)

type jsEvent struct {
	kind jsEventKind
	key  Key
	mods ModifierKey
	text string
}

var (
	jsEventMu   sync.Mutex
	jsEvents    []jsEvent
	jsClipboard string
	// js.Func values must stay referenced for as long as the page lives
	jsFuncs []js.Func
	// requestAnimationFrame completion signal
	jsRAFCh   = make(chan struct{}, 1)
	jsRAFFunc js.Func
)

func init() {
	jsRAFFunc = js.FuncOf(func(this js.Value, args []js.Value) any {
		select {
		case jsRAFCh <- struct{}{}:
		default:
		}
		return nil
	})
}

func pushJSEvent(e jsEvent) {
	jsEventMu.Lock()
	jsEvents = append(jsEvents, e)
	jsEventMu.Unlock()
}

func jsModifiers(e js.Value) ModifierKey {
	var m ModifierKey
	if e.Get("ctrlKey").Bool() {
		m |= modMaskCtrl
	}
	if e.Get("altKey").Bool() {
		m |= modMaskAlt
	}
	if e.Get("shiftKey").Bool() {
		m |= modMaskShift
	}
	if e.Get("metaKey").Bool() {
		m |= modMaskGUI
	}
	return m
}

// Keys typed into page controls (file inputs, text boxes) must not be captured by the game
func jsIsEditableTarget(t js.Value) bool {
	if !t.Truthy() {
		return false
	}
	tag := t.Get("tagName")
	if !tag.Truthy() {
		return false
	}
	switch tag.String() {
	case "INPUT", "TEXTAREA", "SELECT":
		return true
	}
	return false
}

func jsShouldPreventDefault(code string, e js.Value) bool {
	// Keep browser shortcuts (Ctrl+R, Cmd+L, ...) working
	if e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool() {
		return false
	}
	switch code {
	case "F5", "F11", "F12":
		return false
	}
	return true
}

func (s *System) newWindow(w, h int) (*Window, error) {
	doc := js.Global().Get("document")
	canvas := doc.Call("getElementById", "ikemen-canvas")
	if !canvas.Truthy() {
		canvas = doc.Call("createElement", "canvas")
		canvas.Set("id", "ikemen-canvas")
		doc.Get("body").Call("appendChild", canvas)
	}
	if s.cfg.Config.WindowTitle != "" {
		doc.Set("title", s.cfg.Config.WindowTitle)
	}

	win := &Window{
		canvas:    canvas,
		title:     s.cfg.Config.WindowTitle,
		w:         w,
		h:         h,
		focused:   true,
		lastYield: time.Now(),
	}
	win.updateCanvasSize()

	for i := range input.controllerstate {
		input.controllerstate[i] = &ControllerState{}
	}

	win.installListeners()
	return win, nil
}

func (w *Window) installListeners() {
	win := js.Global()
	doc := win.Get("document")
	add := func(target js.Value, name string, f func(this js.Value, args []js.Value) any) {
		fn := js.FuncOf(f)
		jsFuncs = append(jsFuncs, fn)
		target.Call("addEventListener", name, fn)
	}

	add(win, "keydown", func(this js.Value, args []js.Value) any {
		e := args[0]
		if jsIsEditableTarget(e.Get("target")) {
			return nil
		}
		code := e.Get("code").String()
		mods := jsModifiers(e)
		if key, ok := jsCodeToKey[code]; ok {
			pushJSEvent(jsEvent{kind: jsEvKeyDown, key: key, mods: mods})
		}
		// Printable characters feed text entry fields in the menus
		if mods&(modMaskCtrl|modMaskGUI) == 0 {
			text := e.Get("key").String()
			if utf8.RuneCountInString(text) == 1 {
				pushJSEvent(jsEvent{kind: jsEvText, text: text})
			}
		}
		if jsShouldPreventDefault(code, e) {
			e.Call("preventDefault")
		}
		return nil
	})
	add(win, "keyup", func(this js.Value, args []js.Value) any {
		e := args[0]
		if jsIsEditableTarget(e.Get("target")) {
			return nil
		}
		code := e.Get("code").String()
		if key, ok := jsCodeToKey[code]; ok {
			pushJSEvent(jsEvent{kind: jsEvKeyUp, key: key, mods: jsModifiers(e)})
		}
		if jsShouldPreventDefault(code, e) {
			e.Call("preventDefault")
		}
		return nil
	})
	add(win, "focus", func(this js.Value, args []js.Value) any {
		pushJSEvent(jsEvent{kind: jsEvFocus})
		return nil
	})
	add(win, "blur", func(this js.Value, args []js.Value) any {
		pushJSEvent(jsEvent{kind: jsEvBlur})
		return nil
	})
	add(win, "paste", func(this js.Value, args []js.Value) any {
		e := args[0]
		if jsIsEditableTarget(e.Get("target")) {
			return nil
		}
		data := e.Get("clipboardData")
		if data.Truthy() {
			txt := data.Call("getData", "text").String()
			jsEventMu.Lock()
			jsClipboard = txt
			jsEventMu.Unlock()
		}
		return nil
	})
	add(doc, "fullscreenchange", func(this js.Value, args []js.Value) any {
		pushJSEvent(jsEvent{kind: jsEvFullscreen})
		return nil
	})
}

// Matches the drawing buffer to the size the canvas is displayed at.
func (w *Window) updateCanvasSize() {
	dpr := js.Global().Get("devicePixelRatio").Float()
	if math.IsNaN(dpr) || dpr < 1 {
		dpr = 1
	}
	cw := w.canvas.Get("clientWidth").Float()
	ch := w.canvas.Get("clientHeight").Float()
	pw, ph := int(math.Round(cw*dpr)), int(math.Round(ch*dpr))
	if pw <= 0 || ph <= 0 {
		// Canvas is not laid out yet
		pw, ph = w.w, w.h
		if pw <= 0 || ph <= 0 {
			pw, ph = 1280, 720
		}
	}
	if pw > jsMaxCanvasDim || ph > jsMaxCanvasDim {
		scale := float64(jsMaxCanvasDim) / math.Max(float64(pw), float64(ph))
		pw = int(math.Max(1, math.Round(float64(pw)*scale)))
		ph = int(math.Max(1, math.Round(float64(ph)*scale)))
	}
	if w.canvas.Get("width").Int() != pw {
		w.canvas.Set("width", pw)
	}
	if w.canvas.Get("height").Int() != ph {
		w.canvas.Set("height", ph)
	}
}

// Creates the WebGL2 context on the canvas
func (w *Window) GLCreateContext() (GLContext, error) {
	attrs := js.Global().Get("Object").New()
	attrs.Set("alpha", false)
	attrs.Set("depth", true)
	attrs.Set("stencil", false)
	attrs.Set("antialias", false)
	attrs.Set("premultipliedAlpha", false)
	attrs.Set("preserveDrawingBuffer", false)
	attrs.Set("powerPreference", "high-performance")
	gl := w.canvas.Call("getContext", "webgl2", attrs)
	if !gl.Truthy() {
		return GLContext{}, errors.New("WebGL 2 is not available in this browser")
	}
	w.gl = gl
	return GLContext{}, nil
}

func (w *Window) GLMakeCurrent(ctx GLContext) error {
	return nil
}

// Waits for the next animation frame. This is how the page gets to composite the canvas,
// run input and audio callbacks, and how the frame loop is paced like VSync.
func (w *Window) waitAnimationFrame() {
	select {
	case <-jsRAFCh:
	default:
	}
	js.Global().Call("requestAnimationFrame", jsRAFFunc)
	select {
	case <-jsRAFCh:
	case <-time.After(100 * time.Millisecond):
		// Animation frames do not run in hidden tabs. Keep the game going slowly instead of hanging.
	}
	w.lastYield = time.Now()
}

// Gives the browser a chance to run when a long stretch of work did not already yield.
// Without this, long loading screens would freeze the page.
func (w *Window) maybeYield() {
	if time.Since(w.lastYield) > 8*time.Millisecond {
		time.Sleep(time.Millisecond)
		w.lastYield = time.Now()
	}
}

func (w *Window) SwapBuffers() {
	// WebGL presents the drawing buffer when control returns to the browser
	w.waitAnimationFrame()
}

func (w *Window) SetIcon(icon []image.Image) {}

func (w *Window) SetSwapInterval(interval int) {
	gfx.SetVSync(interval)
}

func (w *Window) GetSize() (int, int) {
	return w.canvas.Get("width").Int(), w.canvas.Get("height").Int()
}

// Actual pixel size of the drawing buffer
func (w *Window) GetDrawablePixelSize() (int32, int32) {
	return int32(w.canvas.Get("width").Int()), int32(w.canvas.Get("height").Int())
}

// Calculates a position and size for the viewport to fill the window while centered
// Returns x, y, width, height respectively
func (w *Window) GetScaledViewportSize() (int32, int32, int32, int32) {
	winWidth, winHeight := w.GetDrawablePixelSize()

	// If aspect ratio should not be kept, just return full window
	if !sys.cfg.Video.KeepAspect {
		return 0, 0, winWidth, winHeight
	}

	var x, y, resizedWidth, resizedHeight int32 = 0, 0, winWidth, winHeight

	// Select stage or default aspect ratio
	aspectGame := sys.getCurrentAspect()
	aspectWindow := float32(winWidth) / float32(winHeight)

	// Keep aspect ratio
	if aspectWindow > aspectGame {
		// Window is wider: black bars on sides
		resizedHeight = winHeight
		resizedWidth = int32(float32(resizedHeight) * aspectGame)
		x = (winWidth - resizedWidth) / 2
		y = 0
	} else {
		// Window is taller: black bars on top and bottom
		resizedWidth = winWidth
		resizedHeight = int32(float32(resizedWidth) / aspectGame)
		x = 0
		y = (winHeight - resizedHeight) / 2
	}

	return x, y, resizedWidth, resizedHeight
}

// Browsers only expose the clipboard to paste events, so this returns the last pasted text
func (w *Window) GetClipboardString() string {
	jsEventMu.Lock()
	defer jsEventMu.Unlock()
	return jsClipboard
}

func (w *Window) toggleFullscreen() {
	doc := js.Global().Get("document")
	if doc.Get("fullscreenElement").Truthy() {
		doc.Call("exitFullscreen")
	} else {
		el := w.canvas.Get("parentElement")
		if !el.Truthy() || !el.Get("requestFullscreen").Truthy() {
			el = w.canvas
		}
		if el.Get("requestFullscreen").Truthy() {
			el.Call("requestFullscreen")
		}
	}
	// The real state is synced from the fullscreenchange event, but scripts expect the flag to flip right away
	w.fullscreen = !w.fullscreen
}

func (w *Window) UpdateDebugFPS() {
	// Capture current time in nanoseconds
	now := uint64(time.Now().UnixNano())
	const freq = 1e9

	// The first call has no previous time, so just remember "now" and wait
	if sys.gameFPSprevcount == 0 {
		sys.gameFPSprevcount = now
		return
	}

	// Time since we last updated the FPS
	dt := float64(now-sys.gameFPSprevcount) / freq
	sys.gameFPSprevcount = now
	if dt <= 0 {
		return
	}

	// What this one interval was worth, with no smoothing
	instant := float32(1.0 / dt)

	// Smooth the value over about the last second
	const tau = 1.0 / 3.0
	a := float32(1 - math.Exp(-dt/tau))

	// The average starts from the instant value
	if sys.gameFPS <= 0 {
		sys.gameFPS = instant
		return
	}

	// Mix the new reading into the displayed average
	sys.gameFPS += (instant - sys.gameFPS) * a
}

func (w *Window) pollEvents() {
	w.updateCanvasSize()

	jsEventMu.Lock()
	events := jsEvents
	jsEvents = nil
	jsEventMu.Unlock()

	for _, e := range events {
		switch e.kind {
		case jsEvKeyDown:
			OnKeyPressed(e.key, e.mods)
		case jsEvKeyUp:
			OnKeyReleased(e.key, e.mods)
		case jsEvText:
			OnTextEntered(e.text)
		case jsEvFocus:
			w.focused = true
		case jsEvBlur:
			w.focused = false
			// Key release events are not delivered while unfocused, so release everything
			for k, down := range sys.keyState {
				if down {
					OnKeyReleased(k, 0)
				}
			}
		case jsEvFullscreen:
			w.fullscreen = js.Global().Get("document").Get("fullscreenElement").Truthy()
		}
	}

	pollGamepads()
	w.maybeYield()
}

func (w *Window) shouldClose() bool {
	return w.closeflag
}

func (w *Window) Focused() bool {
	return w.focused
}

func (w *Window) Close() {
	w.closeflag = true
}
