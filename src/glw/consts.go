//go:build js

// Package glw is a thin WebGL 2 binding that follows the shape of the Go OpenGL
// bindings used by the native renderers, so that renderer code can be ported
// with few changes. Objects are identified by uint32 handles, like in native GL,
// and each handle maps to a JavaScript object.
package glw

// Constants have their standard OpenGL/WebGL values.
const (
	ARRAY_BUFFER         = 0x8892
	ELEMENT_ARRAY_BUFFER = 0x8893
	PIXEL_PACK_BUFFER    = 0x88EB
	STATIC_DRAW          = 0x88E4
	DYNAMIC_DRAW         = 0x88E8
	STREAM_READ          = 0x88E1

	BACK       = 0x0405
	BLEND      = 0x0BE2
	CCW        = 0x0901
	CW         = 0x0900
	CULL_FACE  = 0x0B44
	DEPTH_TEST = 0x0B71
	// SCISSOR_TEST enables the scissor rectangle
	SCISSOR_TEST = 0x0C11

	CLAMP_TO_EDGE   = 0x812F
	MIRRORED_REPEAT = 0x8370
	REPEAT          = 0x2901

	COLOR_ATTACHMENT0    = 0x8CE0
	DEPTH_ATTACHMENT     = 0x8D00
	COLOR_BUFFER_BIT     = 0x4000
	DEPTH_BUFFER_BIT     = 0x0100
	FRAMEBUFFER          = 0x8D40
	READ_FRAMEBUFFER     = 0x8CA8
	DRAW_FRAMEBUFFER     = 0x8CA9
	RENDERBUFFER         = 0x8D41
	FRAMEBUFFER_COMPLETE = 0x8CD5

	DEPTH_COMPONENT   = 0x1902
	DEPTH_COMPONENT16 = 0x81A5
	DEPTH_COMPONENT24 = 0x81A6

	ZERO                  = 0
	ONE                   = 1
	SRC_ALPHA             = 0x0302
	ONE_MINUS_SRC_ALPHA   = 0x0303
	DST_COLOR             = 0x0306
	ONE_MINUS_DST_COLOR   = 0x0307
	FUNC_ADD              = 0x8006
	FUNC_REVERSE_SUBTRACT = 0x800B

	BYTE           = 0x1400
	UNSIGNED_BYTE  = 0x1401
	SHORT          = 0x1402
	UNSIGNED_SHORT = 0x1403
	INT            = 0x1404
	UNSIGNED_INT   = 0x1405
	FLOAT          = 0x1406
	HALF_FLOAT     = 0x140B

	FRAGMENT_SHADER = 0x8B30
	VERTEX_SHADER   = 0x8B31

	LESS = 0x0201

	NEAREST                = 0x2600
	LINEAR                 = 0x2601
	NEAREST_MIPMAP_NEAREST = 0x2700
	LINEAR_MIPMAP_NEAREST  = 0x2701
	NEAREST_MIPMAP_LINEAR  = 0x2702
	LINEAR_MIPMAP_LINEAR   = 0x2703

	LINES          = 0x0001
	LINE_LOOP      = 0x0002
	LINE_STRIP     = 0x0003
	TRIANGLES      = 0x0004
	TRIANGLE_STRIP = 0x0005
	TRIANGLE_FAN   = 0x0006

	NONE = 0

	R8          = 0x8229
	RGB8        = 0x8051
	RGBA8       = 0x8058
	RGB32F      = 0x8815
	RGBA32F     = 0x8814
	RGBA16F     = 0x881A
	RGBA8_SNORM = 0x8F97

	RED  = 0x1903
	RGB  = 0x1907
	RGBA = 0x1908

	TEXTURE0                    = 0x84C0
	TEXTURE_2D                  = 0x0DE1
	TEXTURE_CUBE_MAP            = 0x8513
	TEXTURE_CUBE_MAP_POSITIVE_X = 0x8515
	TEXTURE_MAG_FILTER          = 0x2800
	TEXTURE_MIN_FILTER          = 0x2801
	TEXTURE_WRAP_S              = 0x2802
	TEXTURE_WRAP_T              = 0x2803
	TEXTURE_WRAP_R              = 0x8072

	UNPACK_ALIGNMENT  = 0x0CF5
	UNPACK_ROW_LENGTH = 0x0CF2

	VERSION                 = 0x1F02
	RENDERER                = 0x1F01
	MAX_TEXTURE_IMAGE_UNITS = 0x8872
	MAX_TEXTURE_SIZE        = 0x0D33
)
