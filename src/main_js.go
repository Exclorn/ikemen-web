//go:build js

package main

// Android and iOS specific initialization does not apply to the browser build.
// realMain only calls these when runtime.GOOS matches, so they are never reached.
func initAndroidPlatform() bool { return true }
func initIOSPlatform() bool     { return true }
func initAndroidSubsystems()    {}
