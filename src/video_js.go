//go:build js

package main

// Background videos are decoded with ffmpeg through cgo, which is not available in the
// browser build. This stub keeps the stage code compiling. Video layers load without
// error and stay invisible (texture is never set), so the default logo and credits
// screens, which use videos, still run.
type bgVideo struct {
	texture Texture
	muted   bool
}

var bgVideoWarned bool

func (bgv *bgVideo) Open(filename string, volume int, sm BgVideoScaleMode, sf BgVideoScaleFilter, loop bool) error {
	if !bgVideoWarned {
		bgVideoWarned = true
		LogMessage("Background videos are not supported in the browser build (first one skipped: %v)", filename)
	}
	return nil
}

func (bgv *bgVideo) Tick() error { return nil }

func (bgv *bgVideo) SetPlaying(on bool) {}

func (bgv *bgVideo) SetVisible(on bool) {}

func (bgv *bgVideo) Reset() {}

func (bgv *bgVideo) MixerCleared() {}

func (bgv *bgVideo) updateAudioVolume() {}

func (bgv *bgVideo) Close() {}
