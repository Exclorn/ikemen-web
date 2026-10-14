//go:build js

package main

import (
	"errors"
	"math"
	"sync"
	"syscall/js"
	"time"

	"github.com/ikemen-engine/beep/v2"
)

// Web Audio implementation of AudioSink (see audio_sdl.go for the native one).
//
// A goroutine mixes a short chunk of audio at a time and schedules it on the audio
// context's clock, always keeping a little audio queued ahead of the playback position.

const (
	// Number of frames mixed per scheduled chunk
	webAudioChunkFrames = 1024
	// How far ahead of the playback position audio is kept scheduled, in seconds
	webAudioLeadSeconds = 0.08
	// Delay applied when playback has to (re)start, in seconds
	webAudioStartDelay = 0.04
)

type WebSpeaker struct {
	mixer      *beep.Mixer
	mu         sync.Mutex
	sampleRate beep.SampleRate
	buf        [][2]float64
	frames     int

	ctx      js.Value
	ownsCtx  bool
	nextTime float64
	closed   bool

	// Reused JS typed arrays and their Go side byte buffers (little endian float32)
	f32L, f32R js.Value
	u8L, u8R   js.Value
	bytesL     []byte
	bytesR     []byte
}

func newAudioSpeaker() AudioSink {
	return &WebSpeaker{}
}

func (s *WebSpeaker) Init(sampleRate beep.SampleRate, bufferSize int) error {
	s.sampleRate = sampleRate
	s.mixer = &beep.Mixer{}
	s.frames = webAudioChunkFrames
	s.buf = make([][2]float64, s.frames)

	// The host page may already have created and unlocked a context during a user gesture
	ctx := js.Global().Get("ikemenAudioCtx")
	if !ctx.Truthy() {
		ctor := js.Global().Get("AudioContext")
		if !ctor.Truthy() {
			ctor = js.Global().Get("webkitAudioContext")
		}
		if !ctor.Truthy() {
			return errors.New("Web Audio is not available in this browser")
		}
		opts := js.Global().Get("Object").New()
		opts.Set("latencyHint", "interactive")
		ctx = ctor.New(opts)
		s.ownsCtx = true
	}
	s.ctx = ctx

	// Browsers keep audio contexts suspended until a user gesture
	s.installResumeHandlers()

	f32 := js.Global().Get("Float32Array")
	u8 := js.Global().Get("Uint8Array")
	s.f32L = f32.New(s.frames)
	s.f32R = f32.New(s.frames)
	s.u8L = u8.New(s.f32L.Get("buffer"))
	s.u8R = u8.New(s.f32R.Get("buffer"))
	s.bytesL = make([]byte, s.frames*4)
	s.bytesR = make([]byte, s.frames*4)

	// Start the audio pushing goroutine
	SafeGo(func() {
		for {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			s.FillAudio()
			time.Sleep(time.Millisecond * 15)
		}
	})

	return nil
}

func (s *WebSpeaker) installResumeHandlers() {
	if s.ctx.Get("state").String() == "running" {
		return
	}
	doc := js.Global().Get("document")
	var fn js.Func
	events := []string{"keydown", "pointerdown", "touchend", "click"}
	fn = js.FuncOf(func(this js.Value, args []js.Value) any {
		if s.ctx.Get("state").String() != "running" {
			s.ctx.Call("resume")
		}
		// Remove the handlers once the context is running
		if s.ctx.Get("state").String() == "running" {
			for _, ev := range events {
				doc.Call("removeEventListener", ev, fn)
			}
		}
		return nil
	})
	jsFuncs = append(jsFuncs, fn)
	for _, ev := range events {
		doc.Call("addEventListener", ev, fn)
	}
}

func floatBitsLE(dst []byte, i int, v float32) {
	b := math.Float32bits(v)
	dst[i*4] = byte(b)
	dst[i*4+1] = byte(b >> 8)
	dst[i*4+2] = byte(b >> 16)
	dst[i*4+3] = byte(b >> 24)
}

func clampSample(v float64) float32 {
	if v > 1 {
		return 1
	} else if v < -1 {
		return -1
	}
	return float32(v)
}

// Mixes and schedules chunks until enough audio is queued ahead of the playback position
func (s *WebSpeaker) FillAudio() {
	if !s.ctx.Truthy() || s.mixer == nil {
		return
	}
	// A suspended context has a frozen clock, so scheduling would just pile up audio
	if s.ctx.Get("state").String() != "running" {
		return
	}

	now := s.ctx.Get("currentTime").Float()
	if s.nextTime < now+0.005 {
		// First chunk, or the queue ran dry: restart with a small delay
		s.nextTime = now + webAudioStartDelay
	}

	rate := float64(s.sampleRate)
	for s.nextTime-now < webAudioLeadSeconds {
		s.mu.Lock()
		n, _ := s.mixer.Stream(s.buf[:s.frames])
		s.mu.Unlock()
		if n == 0 {
			return
		}

		for i := 0; i < n; i++ {
			floatBitsLE(s.bytesL, i, clampSample(s.buf[i][0]))
			floatBitsLE(s.bytesR, i, clampSample(s.buf[i][1]))
		}
		// Pad the rest of a short read with silence
		for i := n; i < s.frames; i++ {
			floatBitsLE(s.bytesL, i, 0)
			floatBitsLE(s.bytesR, i, 0)
		}
		js.CopyBytesToJS(s.u8L, s.bytesL)
		js.CopyBytesToJS(s.u8R, s.bytesR)

		abuf := s.ctx.Call("createBuffer", 2, n, rate)
		abuf.Call("copyToChannel", s.f32L.Call("subarray", 0, n), 0)
		abuf.Call("copyToChannel", s.f32R.Call("subarray", 0, n), 1)

		src := s.ctx.Call("createBufferSource")
		src.Set("buffer", abuf)
		src.Call("connect", s.ctx.Get("destination"))
		src.Call("start", s.nextTime)

		s.nextTime += float64(n) / rate
	}
}

func (s *WebSpeaker) Play(st beep.Streamer) {
	s.mu.Lock()
	s.mixer.Add(st)
	s.mu.Unlock()
}

func (s *WebSpeaker) Lock()   { s.mu.Lock() }
func (s *WebSpeaker) Unlock() { s.mu.Unlock() }

func (s *WebSpeaker) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.ownsCtx && s.ctx.Truthy() {
		s.ctx.Call("close")
	}
}
