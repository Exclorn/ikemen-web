//go:build js

package main

import (
	"errors"
	"io"

	"github.com/ikemen-engine/beep/v2"
)

// Tracker music (XM/MOD/IT/S3M) needs libxmp, which is a C library and cannot be
// used in the browser build.
func xmpDecode(f io.ReadSeekCloser) (beep.StreamSeekCloser, beep.Format, error) {
	return nil, beep.Format{}, errors.New("tracker music formats (xm/mod/it/s3m) are not supported in the browser build")
}
