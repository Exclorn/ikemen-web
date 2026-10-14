//go:build js

package main

import (
	"encoding/hex"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"syscall/js"
)

// Browser implementation of the input layer (keyboard and Gamepad API).
// This file provides the same names as input_sdl.go.

// ---------------------------------------------------------------------------
// Keys and modifiers
// ---------------------------------------------------------------------------

type Key int32
type ModifierKey uint16

// Modifier masks used by shared code (see ShortcutKey.Test in input.go)
const (
	modMaskCtrl ModifierKey = 1 << iota
	modMaskAlt
	modMaskShift
	modMaskGUI
)

// Keys that shared code refers to by name get fixed values. All other keys are
// numbered dynamically from keyFirstDynamic upward when the lookup tables are built.
const (
	KeyUnknown Key = iota
	KeyEscape
	KeyEnter
	KeyInsert
	KeyF5
	KeyF12
	KeyPause
	KeyScrollLock
	keyFirstDynamic
)

// Standard game controller button indices. They use the same numbering as the
// SDL game controller buttons, which the rest of the engine relies on.
const (
	ctrlButtonA = iota
	ctrlButtonB
	ctrlButtonX
	ctrlButtonY
	ctrlButtonBack
	ctrlButtonGuide
	ctrlButtonStart
	ctrlButtonLeftStick
	ctrlButtonRightStick
	ctrlButtonLeftShoulder
	ctrlButtonRightShoulder
	ctrlButtonDpadUp
	ctrlButtonDpadDown
	ctrlButtonDpadLeft
	ctrlButtonDpadRight
	ctrlButtonCount
)

// Controller axis indices (same numbering as SDL)
const (
	ctrlAxisTriggerLeft  = 4
	ctrlAxisTriggerRight = 5
)

var KeyToStringLUT map[Key]string
var StringToKeyLUT map[string]Key
var ButtonToStringLUT map[int]string
var buttonOrder []int
var StringToButtonLUT map[string]int

// Maps KeyboardEvent.code (physical key) to the engine key
var jsCodeToKey map[string]Key

type jsKeyEntry struct {
	code string // KeyboardEvent.code
	name string // name used in config files
	key  Key    // fixed value, or 0 to assign dynamically
}

func buildJSKeyTable() []jsKeyEntry {
	t := []jsKeyEntry{
		{"Enter", "RETURN", KeyEnter},
		{"Escape", "ESCAPE", KeyEscape},
		{"Backspace", "BACKSPACE", 0},
		{"Tab", "TAB", 0},
		{"Space", "SPACE", 0},
		{"Quote", "QUOTE", 0},
		{"Comma", "COMMA", 0},
		{"Minus", "MINUS", 0},
		{"Period", "PERIOD", 0},
		{"Slash", "SLASH", 0},
		{"Semicolon", "SEMICOLON", 0},
		{"Equal", "EQUALS", 0},
		{"BracketLeft", "LBRACKET", 0},
		{"Backslash", "BACKSLASH", 0},
		{"BracketRight", "RBRACKET", 0},
		{"Backquote", "BACKQUOTE", 0},
		{"CapsLock", "CAPSLOCK", 0},
		{"PrintScreen", "PRINTSCREEN", 0},
		{"ScrollLock", "SCROLLLOCK", KeyScrollLock},
		{"Pause", "PAUSE", KeyPause},
		{"Insert", "INSERT", KeyInsert},
		{"Home", "HOME", 0},
		{"PageUp", "PAGEUP", 0},
		{"Delete", "DELETE", 0},
		{"End", "END", 0},
		{"PageDown", "PAGEDOWN", 0},
		{"ArrowRight", "RIGHT", 0},
		{"ArrowLeft", "LEFT", 0},
		{"ArrowDown", "DOWN", 0},
		{"ArrowUp", "UP", 0},
		{"NumLock", "NUMLOCKCLEAR", 0},
		{"NumpadDivide", "KP_DIVIDE", 0},
		{"NumpadMultiply", "KP_MULTIPLY", 0},
		{"NumpadSubtract", "KP_MINUS", 0},
		{"NumpadAdd", "KP_PLUS", 0},
		{"NumpadEnter", "KP_ENTER", 0},
		{"NumpadDecimal", "KP_PERIOD", 0},
		{"NumpadEqual", "KP_EQUALS", 0},
		{"ContextMenu", "MENU", 0},
		{"ControlLeft", "LCTRL", 0},
		{"ShiftLeft", "LSHIFT", 0},
		{"AltLeft", "LALT", 0},
		{"MetaLeft", "LGUI", 0},
		{"ControlRight", "RCTRL", 0},
		{"ShiftRight", "RSHIFT", 0},
		{"AltRight", "RALT", 0},
		{"MetaRight", "RGUI", 0},
	}
	for c := 'a'; c <= 'z'; c++ {
		t = append(t, jsKeyEntry{"Key" + strings.ToUpper(string(c)), string(c), 0})
	}
	for d := 0; d <= 9; d++ {
		ds := strconv.Itoa(d)
		t = append(t, jsKeyEntry{"Digit" + ds, ds, 0})
		t = append(t, jsKeyEntry{"Numpad" + ds, "KP_" + ds, 0})
	}
	for f := 1; f <= 24; f++ {
		fs := strconv.Itoa(f)
		var k Key
		if f == 5 {
			k = KeyF5
		} else if f == 12 {
			k = KeyF12
		}
		t = append(t, jsKeyEntry{"F" + fs, "F" + fs, k})
	}
	return t
}

// ---------------------------------------------------------------------------
// Controllers
// ---------------------------------------------------------------------------

type ControllerState struct {
	Axes      [6]int8
	Buttons   [ctrlButtonCount]byte
	HasRumble bool
}

// A gamepad slot. The Gamepad API identifies pads by an index that stays stable
// while the pad is connected.
type jsGamepad struct {
	index int
	id    string
}

type Input struct {
	controllers     [MaxPlayerNo]*jsGamepad
	controllerstate [MaxPlayerNo]*ControllerState
}

var input Input

func initLUTs() {
	KeyToStringLUT = make(map[Key]string)
	StringToKeyLUT = make(map[string]Key)
	jsCodeToKey = make(map[string]Key)
	next := keyFirstDynamic
	for _, e := range buildJSKeyTable() {
		k := e.key
		if k == KeyUnknown {
			k = next
			next++
		}
		KeyToStringLUT[k] = e.name
		StringToKeyLUT[e.name] = k
		jsCodeToKey[e.code] = k
	}

	buttonOrder = []int{
		ctrlButtonA,
		ctrlButtonB,
		ctrlButtonX,
		ctrlButtonY,
		ctrlButtonBack,
		ctrlButtonGuide,
		ctrlButtonStart,
		ctrlButtonLeftStick,
		ctrlButtonRightStick,
		ctrlButtonLeftShoulder,
		ctrlButtonRightShoulder,
		ctrlButtonDpadUp,
		ctrlButtonDpadDown,
		ctrlButtonDpadLeft,
		ctrlButtonDpadRight,
	}

	ButtonToStringLUT = map[int]string{
		ctrlButtonA:             "A",
		ctrlButtonB:             "B",
		ctrlButtonX:             "X",
		ctrlButtonY:             "Y",
		ctrlButtonBack:          "BACK",
		ctrlButtonGuide:         "HOME",
		ctrlButtonStart:         "START",
		ctrlButtonLeftStick:     "LS",
		ctrlButtonRightStick:    "RS",
		ctrlButtonLeftShoulder:  "LB",
		ctrlButtonRightShoulder: "RB",
		ctrlButtonDpadUp:        "DP_U",
		ctrlButtonDpadDown:      "DP_D",
		ctrlButtonDpadLeft:      "DP_L",
		ctrlButtonDpadRight:     "DP_R",
		15:                      "LS_Y-",
		16:                      "LS_X-",
		17:                      "LS_X+",
		18:                      "LS_Y+",
		19:                      "LT",
		20:                      "RT",
		21:                      "RS_Y-",
		22:                      "RS_X-",
		23:                      "RS_X+",
		24:                      "RS_Y+",
		25:                      "Not used",
	}

	StringToButtonLUT = make(map[string]int)
	for k, v := range ButtonToStringLUT {
		StringToButtonLUT[v] = k
	}

	input = Input{}
}

func StringToKey(s string) Key {
	if key, ok := StringToKeyLUT[s]; ok {
		return key
	}
	return KeyUnknown
}

func KeyToString(k Key) string {
	if s, ok := KeyToStringLUT[k]; ok {
		return s
	}
	return ""
}

func NewModifierKey(ctrl, alt, shift bool) (mod ModifierKey) {
	if ctrl {
		mod |= modMaskCtrl
	}
	if alt {
		mod |= modMaskAlt
	}
	if shift {
		mod |= modMaskShift
	}
	return
}

// Custom gamepad mapping files are an SDL feature. The browser already
// normalizes controllers to the "standard" layout when it can.
func (input *Input) UpdateGamepadMappings(path string) {}

func (input *Input) GetMaxJoystickCount() int {
	return len(input.controllers)
}

func (input *Input) IsJoystickPresent(joy int) bool {
	if joy < 0 || joy >= len(input.controllers) {
		return false
	}
	return input.controllers[joy] != nil
}

func (input *Input) GetJoystickName(joy int) string {
	if joy < 0 || joy >= len(input.controllers) || input.controllers[joy] == nil {
		return ""
	}
	return input.controllers[joy].id
}

func (input *Input) GetJoystickAxes(joy int) [6]float32 {
	if joy < 0 || joy >= len(input.controllerstate) || input.controllerstate[joy] == nil {
		return [6]float32{0, 0, 0, 0, 0, 0}
	}
	axes := NormalizeAxes(&input.controllerstate[joy].Axes)
	return axes
}

func (input *Input) GetJoystickButtons(joy int) []byte {
	if joy < 0 || joy >= len(input.controllerstate) || input.controllerstate[joy] == nil {
		return []byte{}
	}
	buttons := make([]byte, len(buttonOrder))
	for i, button := range buttonOrder {
		buttons[i] = input.controllerstate[joy].Buttons[button]
	}
	return buttons
}

func (input *Input) GetJoystickPath(joy int) string {
	return ""
}

// The browser does not expose SDL-style GUIDs, so derive a stable one from the gamepad id string.
func (input *Input) GetJoystickGUID(joy int) string {
	if joy < 0 || joy >= len(input.controllers) || input.controllers[joy] == nil {
		return ""
	}
	h := fnv.New64a()
	h.Write([]byte(input.controllers[joy].id))
	guid := make([]byte, 16)
	sum := h.Sum64()
	for i := 0; i < 8; i++ {
		guid[i] = byte(sum >> (8 * uint(i)))
	}
	guid[0] = 0x03
	return hex.EncodeToString(guid)
}

func (input *Input) RumbleController(joy int, lo, hi uint16, ticks uint32) {
	if joy < 0 || joy >= len(input.controllers) || joy >= len(sys.joystickConfig) {
		return
	}
	pad := input.controllers[joy]
	st := input.controllerstate[joy]
	if pad == nil || st == nil || !st.HasRumble || !sys.joystickConfig[joy].rumbleOn {
		return
	}
	gls := sys.gameLogicSpeed()
	if gls <= 0 || sys.turbo <= 0 {
		return
	}
	nav := js.Global().Get("navigator")
	if !nav.Truthy() || !nav.Get("getGamepads").Truthy() {
		return
	}
	pads := nav.Call("getGamepads")
	if pad.index >= pads.Length() {
		return
	}
	p := pads.Index(pad.index)
	if !p.Truthy() {
		return
	}
	act := p.Get("vibrationActuator")
	if !act.Truthy() || !act.Get("playEffect").Truthy() {
		return
	}
	if ticks == 0 {
		if act.Get("reset").Truthy() {
			act.Call("reset")
		}
		return
	}
	framerateMs := math.Ceil(1.0 / float64(gls) * float64(sys.turbo) * 1000.0)
	bufferMs := math.Floor(framerateMs / 2)
	params := map[string]interface{}{
		"startDelay":      0,
		"duration":        float64(ticks)*framerateMs + bufferMs,
		"weakMagnitude":   float64(hi) / 65535.0,
		"strongMagnitude": float64(lo) / 65535.0,
	}
	act.Call("playEffect", "dual-rumble", params)
}

func CheckAxisForDpad(axes *[6]float32, base int) string {
	var s string = ""

	// Left stick
	if (*axes)[0] > sys.cfg.Input.ControllerStickSensitivity { // right (LS)
		s = ButtonToStringLUT[2+base]
	} else if -(*axes)[0] > sys.cfg.Input.ControllerStickSensitivity { // left (LS)
		s = ButtonToStringLUT[1+base]
	}
	if (*axes)[1] > sys.cfg.Input.ControllerStickSensitivity { // down (LS)
		s = ButtonToStringLUT[3+base]
	} else if -(*axes)[1] > sys.cfg.Input.ControllerStickSensitivity { // up (LS)
		s = ButtonToStringLUT[base]
	}

	// Right stick
	if (*axes)[2] > sys.cfg.Input.ControllerStickSensitivity { // right (RS)
		s = ButtonToStringLUT[8+base]
	} else if -(*axes)[2] > sys.cfg.Input.ControllerStickSensitivity { // left (RS)
		s = ButtonToStringLUT[7+base]
	}
	if (*axes)[3] > sys.cfg.Input.ControllerStickSensitivity { // down (RS)
		s = ButtonToStringLUT[9+base]
	} else if -(*axes)[3] > sys.cfg.Input.ControllerStickSensitivity { // up (RS)
		s = ButtonToStringLUT[6+base]
	}

	return s
}

func CheckAxisForTrigger(axes *[6]float32) string {
	var s string = ""
	axesList := [2]int{ctrlAxisTriggerLeft, ctrlAxisTriggerRight}
	for _, i := range axesList {
		if (*axes)[i] > 0 {
			s = ButtonToStringLUT[15+i]
			break
		}
	}
	return s
}

// returns the first active button/axis token string and the joystick index.
func getJoystickKey(controllerIdx int) (string, int) {
	var s string
	min, max := 0, input.GetMaxJoystickCount()

	if controllerIdx >= 0 && controllerIdx < max {
		min, max = controllerIdx, controllerIdx+1
	}

	for joy := min; joy < max; joy++ {
		if !input.IsJoystickPresent(joy) {
			continue
		}

		axes := input.GetJoystickAxes(joy)
		btns := input.GetJoystickButtons(joy)

		// Prefer stick directions (LS_*/RS_* / DP_*).
		s = CheckAxisForDpad(&axes, len(btns))
		if s != "" {
			return s, joy
		}

		// Then triggers (LT / RT).
		s = CheckAxisForTrigger(&axes)
		if s != "" {
			return s, joy
		}

		// Finally, digital buttons (A/B/X/Y/etc.).
		for i := range btns {
			if btns[i] > 0 {
				s = ButtonToStringLUT[i]
			}
		}
		if s != "" && strings.ToLower(s) != "not used" {
			return s, joy
		}
	}

	return "", -1
}

// ---------------------------------------------------------------------------
// Gamepad polling
// ---------------------------------------------------------------------------

// Browser button index (standard mapping) for each engine controller button
var jsStandardButtonIndex = [ctrlButtonCount]int{
	ctrlButtonA:             0,
	ctrlButtonB:             1,
	ctrlButtonX:             2,
	ctrlButtonY:             3,
	ctrlButtonBack:          8,
	ctrlButtonGuide:         16,
	ctrlButtonStart:         9,
	ctrlButtonLeftStick:     10,
	ctrlButtonRightStick:    11,
	ctrlButtonLeftShoulder:  4,
	ctrlButtonRightShoulder: 5,
	ctrlButtonDpadUp:        12,
	ctrlButtonDpadDown:      13,
	ctrlButtonDpadLeft:      14,
	ctrlButtonDpadRight:     15,
}

func jsFloatToAxis(v float64) int8 {
	if v > 1 {
		v = 1
	} else if v < -1 {
		v = -1
	}
	if v < 0 {
		return int8(v * 128)
	}
	return int8(v * 127)
}

func resetControllerState(idx int) {
	if idx < 0 || idx >= len(input.controllerstate) {
		return
	}
	input.controllerstate[idx] = &ControllerState{}
}

func findGamepadSlot(index int) int {
	for i, c := range input.controllers {
		if c != nil && c.index == index {
			return i
		}
	}
	return -1
}

func findFreeControllerSlot() int {
	for i, c := range input.controllers {
		if c == nil {
			return i
		}
	}
	return -1
}

// Reads the current state of all gamepads. The Gamepad API is polled, there are no
// per-button events.
func pollGamepads() {
	nav := js.Global().Get("navigator")
	if !nav.Truthy() || !nav.Get("getGamepads").Truthy() {
		return
	}
	if _, nojoy := sys.cmdFlags["-nojoy"]; nojoy {
		return
	}
	pads := nav.Call("getGamepads")
	n := pads.Length()

	seen := make(map[int]bool)
	for i := 0; i < n; i++ {
		p := pads.Index(i)
		if !p.Truthy() || !p.Get("connected").Bool() {
			continue
		}
		index := p.Get("index").Int()
		seen[index] = true

		slot := findGamepadSlot(index)
		if slot == -1 {
			slot = findFreeControllerSlot()
			if slot == -1 {
				continue
			}
			input.controllers[slot] = &jsGamepad{index: index, id: p.Get("id").String()}
			resetControllerState(slot)
			act := p.Get("vibrationActuator")
			input.controllerstate[slot].HasRumble = act.Truthy() && act.Get("playEffect").Truthy()
		}
		st := input.controllerstate[slot]
		if st == nil {
			resetControllerState(slot)
			st = input.controllerstate[slot]
		}

		// Buttons
		buttons := p.Get("buttons")
		nb := buttons.Length()
		for b := 0; b < ctrlButtonCount; b++ {
			bi := jsStandardButtonIndex[b]
			var pressed byte
			if bi < nb && buttons.Index(bi).Get("pressed").Bool() {
				pressed = 1
			}
			st.Buttons[b] = pressed
		}

		// Sticks
		axes := p.Get("axes")
		na := axes.Length()
		for a := 0; a < 4; a++ {
			if a < na {
				st.Axes[a] = jsFloatToAxis(axes.Index(a).Float())
			} else {
				st.Axes[a] = 0
			}
		}

		// Triggers are analog buttons 6 and 7 on the standard layout
		for t, bi := range [2]int{6, 7} {
			var v int8
			if bi < nb {
				val := buttons.Index(bi).Get("value").Float()
				if val < 0 {
					val = 0
				}
				v = jsFloatToAxis(val)
			}
			st.Axes[ctrlAxisTriggerLeft+t] = v
		}
	}

	// Drop pads that went away
	for i, c := range input.controllers {
		if c != nil && !seen[c.index] {
			input.controllers[i] = nil
			resetControllerState(i)
		}
	}
}
