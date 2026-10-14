//go:build js

package main

import (
	"fmt"
	"io"
	"os"
	"syscall/js"
)

// Platform functions for the browser build (see util_desktop.go for the native versions)

// Log writer implementation. Standard error is forwarded to the browser console.
func NewLogWriter() io.Writer {
	return os.Stderr
}

// Shows a message in the host page. The page can define a global
// ikemenShowMessage(level, title, message) function to display it in its own UI.
func jsShowMessage(level, title, message string) {
	fn := js.Global().Get("ikemenShowMessage")
	if fn.Truthy() {
		fn.Invoke(level, title, message)
		return
	}
	fmt.Fprintf(os.Stderr, "[%s] %s\n%s\n", level, title, message)
}

func ShowInfoDialog(message, title string) {
	jsShowMessage("info", title, message)
}

func ShowErrorDialog(message string) {
	jsShowMessage("error", "I.K.E.M.E.N Error", message)
}

// Returns true if the user chose to retry
func ShowRetryDialog(message string) bool {
	confirm := js.Global().Get("confirm")
	if !confirm.Truthy() {
		return false
	}
	return confirm.Invoke("I.K.E.M.E.N RetryLoadOnError\n\n" + message).Bool()
}

// TTF font loading. System fonts cannot be searched in the browser, so the font file
// must be inside the game files.
func LoadFntTtf(f *Fnt, fontfile string, filename string, height int32) {
	fileDir := SearchFile(filename, []string{fontfile, sys.motif.Def, "", "data/"}, "font/")
	if fp := FileExist(fileDir); len(fp) == 0 {
		panic(fmt.Errorf("failed to find ttf font %v", fileDir))
	}
	// Load ttf
	if height == -1 {
		height = int32(f.Size[1])
	} else {
		f.Size[1] = uint16(height)
	}
	ttf, err := gfxFont.LoadFont(fileDir, height, int(sys.gameWidth), int(sys.gameHeight))
	if err != nil {
		panic(fmt.Errorf("failed to load ttf font %v: %w", fileDir, err))
	}
	f.ttf = ttf.(Font)

	// Create Ttf dummy palettes
	f.palettes = make([][256]uint32, 1)
	for i := 0; i < 256; i++ {
		f.palettes[0][i] = 0
	}
}

// The browser build only has the WebGL 2 renderer, whatever the config says
func selectRenderer(cfgVal string) (Renderer, FontRenderer) {
	return &Renderer_WebGL2{}, &FontRenderer_WebGL2{}
}

func Logcat(s string) {
	fmt.Println(s)
}

// osPreferredLanguage returns a locale-ish tag from the browser.
func osPreferredLanguage() string {
	nav := js.Global().Get("navigator")
	if nav.Truthy() {
		if lang := nav.Get("language"); lang.Truthy() {
			return lang.String()
		}
	}
	return ""
}
