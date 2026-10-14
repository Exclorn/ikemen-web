package main

import "github.com/ikemen-engine/beep/v2"

// AudioSink is the final output stage of the mixer. It is implemented by the
// SDL speaker on native platforms and by a Web Audio speaker in the browser build.
type AudioSink interface {
	Init(sr beep.SampleRate, bufferSize int) error
	Play(s beep.Streamer)
	Lock()
	Unlock()
	Close()
	FillAudio()
}

var speaker AudioSink
