//go:build js

package glw

import (
	"strings"
	"syscall/js"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Context and handle tables
// ---------------------------------------------------------------------------

var (
	ctx js.Value
	// Methods are bound to the context once, so calls do not need a property lookup and a "this" each time
	methods = make(map[string]js.Value)

	// Handle 0 is reserved for "no object", like in native GL
	objects         = make(map[uint32]js.Value)
	nextObj  uint32 = 1
	uniforms        = make(map[int32]js.Value)
	nextUni  int32  = 1

	jsNull = js.Null()
)

// Init sets the WebGL 2 context all other functions work on and turns on the optional
// features the renderer wants. It returns false if the context is unusable.
func Init(context js.Value) bool {
	if !context.Truthy() {
		return false
	}
	ctx = context
	methods = make(map[string]js.Value)
	// Rendering to floating point textures
	ctx.Call("getExtension", "EXT_color_buffer_float")
	// Linear filtering of floating point textures
	ctx.Call("getExtension", "OES_texture_float_linear")
	return true
}

// HasExtension reports whether the context supports an extension (and enables it)
func HasExtension(name string) bool {
	return ctx.Call("getExtension", name).Truthy()
}

func call(name string, args ...any) js.Value {
	m, ok := methods[name]
	if !ok {
		m = ctx.Get(name).Call("bind", ctx)
		methods[name] = m
	}
	return m.Invoke(args...)
}

func newObject(v js.Value) uint32 {
	if !v.Truthy() {
		return 0
	}
	id := nextObj
	nextObj++
	objects[id] = v
	return id
}

func obj(id uint32) js.Value {
	if id == 0 {
		return jsNull
	}
	v, ok := objects[id]
	if !ok {
		return jsNull
	}
	return v
}

func release(id uint32) {
	delete(objects, id)
}

// ---------------------------------------------------------------------------
// Data transfer
// ---------------------------------------------------------------------------

// Data is copied through one reusable typed array, which avoids allocating a new one for every upload.
var (
	scratchSize int
	scratchU8   js.Value
	scratchU16  js.Value
	scratchF32  js.Value
)

func ensureScratch(n int) {
	if n <= scratchSize {
		return
	}
	size := 1 << 16
	for size < n {
		size <<= 1
	}
	buf := js.Global().Get("ArrayBuffer").New(size)
	scratchU8 = js.Global().Get("Uint8Array").New(buf)
	scratchU16 = js.Global().Get("Uint16Array").New(buf)
	scratchF32 = js.Global().Get("Float32Array").New(buf)
	scratchSize = size
}

// Copies data into the scratch array and returns it
func putBytes(b []byte) js.Value {
	ensureScratch(len(b))
	js.CopyBytesToJS(scratchU8, b)
	return scratchU8
}

// Uploads bigger than this get a one-off typed array instead of growing the scratch one forever
const maxScratchUpload = 8 << 20

// typedView returns a typed array holding a copy of b, in the element type GL expects for typ.
// The data always starts at offset 0.
func typedView(b []byte, typ uint32) js.Value {
	if len(b) <= maxScratchUpload {
		putBytes(b)
		switch typ {
		case FLOAT:
			return scratchF32
		case HALF_FLOAT, UNSIGNED_SHORT:
			return scratchU16
		}
		return scratchU8
	}
	buf := js.Global().Get("ArrayBuffer").New(len(b))
	u8 := js.Global().Get("Uint8Array").New(buf)
	js.CopyBytesToJS(u8, b)
	switch typ {
	case FLOAT:
		return js.Global().Get("Float32Array").New(buf)
	case HALF_FLOAT, UNSIGNED_SHORT:
		return js.Global().Get("Uint16Array").New(buf)
	}
	return u8
}

// F32Bytes views a float32 slice as bytes without copying
func F32Bytes(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*4)
}

// U32Bytes views a uint32 slice as bytes without copying
func U32Bytes(v []uint32) []byte {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*4)
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

func Enable(cap uint32)  { call("enable", cap) }
func Disable(cap uint32) { call("disable", cap) }

func Viewport(x, y, w, h int32) { call("viewport", x, y, w, h) }
func Scissor(x, y, w, h int32)  { call("scissor", x, y, w, h) }

func Clear(mask uint32) { call("clear", mask) }

func BlendFunc(src, dst uint32)             { call("blendFunc", src, dst) }
func BlendEquation(mode uint32)             { call("blendEquation", mode) }
func DepthFunc(f uint32)                    { call("depthFunc", f) }
func DepthMask(flag bool)                   { call("depthMask", flag) }
func CullFace(mode uint32)                  { call("cullFace", mode) }
func FrontFace(mode uint32)                 { call("frontFace", mode) }
func PixelStorei(pname uint32, param int32) { call("pixelStorei", pname, param) }
func Finish()                               { call("finish") }
func GetError() uint32                      { return uint32(call("getError").Int()) }

func GetString(name uint32) string {
	v := call("getParameter", name)
	if v.Type() == js.TypeString {
		return v.String()
	}
	return ""
}

// GetIntegerv returns a single integer parameter
func GetInteger(pname uint32) int32 {
	v := call("getParameter", pname)
	if v.Type() == js.TypeNumber {
		return int32(v.Int())
	}
	return 0
}

// ---------------------------------------------------------------------------
// Buffers and vertex arrays
// ---------------------------------------------------------------------------

func GenBuffers(n int32, ids *uint32) {
	for i := int32(0); i < n; i++ {
		*(*uint32)(unsafe.Add(unsafe.Pointer(ids), uintptr(i)*4)) = newObject(call("createBuffer"))
	}
}

func DeleteBuffers(n int32, ids *uint32) {
	for i := int32(0); i < n; i++ {
		p := (*uint32)(unsafe.Add(unsafe.Pointer(ids), uintptr(i)*4))
		call("deleteBuffer", obj(*p))
		release(*p)
	}
}

func BindBuffer(target, buffer uint32) { call("bindBuffer", target, obj(buffer)) }

// BufferData allocates size bytes and uploads data into them. A nil data only allocates.
func BufferData(target uint32, size int, data []byte, usage uint32) {
	if len(data) == 0 {
		call("bufferData", target, size, usage)
		return
	}
	src := typedView(data, UNSIGNED_BYTE)
	call("bufferData", target, src, usage, 0, len(data))
}

func BufferSubData(target uint32, offset int, data []byte) {
	if len(data) == 0 {
		return
	}
	src := typedView(data, UNSIGNED_BYTE)
	call("bufferSubData", target, offset, src, 0, len(data))
}

func GenVertexArrays(n int32, ids *uint32) {
	for i := int32(0); i < n; i++ {
		*(*uint32)(unsafe.Add(unsafe.Pointer(ids), uintptr(i)*4)) = newObject(call("createVertexArray"))
	}
}

func BindVertexArray(vao uint32) { call("bindVertexArray", obj(vao)) }

func EnableVertexAttribArray(index uint32)  { call("enableVertexAttribArray", index) }
func DisableVertexAttribArray(index uint32) { call("disableVertexAttribArray", index) }

// PtrOffset turns a byte offset into the pointer argument of VertexAttribPointer
func PtrOffset(offset int) unsafe.Pointer { return unsafe.Pointer(uintptr(offset)) }

func VertexAttribPointer(index uint32, size int32, typ uint32, normalized bool, stride int32, offset unsafe.Pointer) {
	call("vertexAttribPointer", index, size, typ, normalized, stride, int(uintptr(offset)))
}

func VertexAttribPointerWithOffset(index uint32, size int32, typ uint32, normalized bool, stride int32, offset uintptr) {
	call("vertexAttribPointer", index, size, typ, normalized, stride, int(offset))
}

func VertexAttrib2f(index uint32, x, y float32)       { call("vertexAttrib2f", index, x, y) }
func VertexAttrib3f(index uint32, x, y, z float32)    { call("vertexAttrib3f", index, x, y, z) }
func VertexAttrib4f(index uint32, x, y, z, w float32) { call("vertexAttrib4f", index, x, y, z, w) }

func DrawArrays(mode uint32, first, count int32) { call("drawArrays", mode, first, count) }

func DrawElementsWithOffset(mode uint32, count int32, typ uint32, offset uintptr) {
	call("drawElements", mode, count, typ, int(offset))
}

// ---------------------------------------------------------------------------
// Shaders and programs
// ---------------------------------------------------------------------------

func CreateShader(typ uint32) uint32 { return newObject(call("createShader", typ)) }

// ShaderSource sets the source of a shader. WebGL rejects NUL characters, which native code
// adds as string terminators, so they are removed.
func ShaderSource(shader uint32, src string) {
	src = strings.ReplaceAll(src, "\x00", "")
	call("shaderSource", obj(shader), src)
}

func CompileShader(shader uint32) { call("compileShader", obj(shader)) }

func GetShaderCompileStatus(shader uint32) bool {
	return call("getShaderParameter", obj(shader), 0x8B81).Bool()
}

func GetShaderInfoLog(shader uint32) string {
	v := call("getShaderInfoLog", obj(shader))
	if v.Type() == js.TypeString {
		return v.String()
	}
	return ""
}

func DeleteShader(shader uint32) {
	call("deleteShader", obj(shader))
	release(shader)
}

func CreateProgram() uint32               { return newObject(call("createProgram")) }
func AttachShader(program, shader uint32) { call("attachShader", obj(program), obj(shader)) }
func DetachShader(program, shader uint32) { call("detachShader", obj(program), obj(shader)) }
func LinkProgram(program uint32)          { call("linkProgram", obj(program)) }
func UseProgram(program uint32)           { call("useProgram", obj(program)) }

func GetProgramLinkStatus(program uint32) bool {
	return call("getProgramParameter", obj(program), 0x8B82).Bool()
}

func GetProgramInfoLog(program uint32) string {
	v := call("getProgramInfoLog", obj(program))
	if v.Type() == js.TypeString {
		return v.String()
	}
	return ""
}

func DeleteProgram(program uint32) {
	call("deleteProgram", obj(program))
	release(program)
}

func GetAttribLocation(program uint32, name string) int32 {
	return int32(call("getAttribLocation", obj(program), name).Int())
}

// GetUniformLocation returns -1 if the program has no such uniform
func GetUniformLocation(program uint32, name string) int32 {
	v := call("getUniformLocation", obj(program), name)
	if !v.Truthy() {
		return -1
	}
	id := nextUni
	nextUni++
	uniforms[id] = v
	return id
}

func uni(loc int32) (js.Value, bool) {
	v, ok := uniforms[loc]
	return v, ok
}

func Uniform1i(loc int32, v int32) {
	if u, ok := uni(loc); ok {
		call("uniform1i", u, v)
	}
}

func Uniform1f(loc int32, v float32) {
	if u, ok := uni(loc); ok {
		call("uniform1f", u, v)
	}
}

func Uniform2f(loc int32, x, y float32) {
	if u, ok := uni(loc); ok {
		call("uniform2f", u, x, y)
	}
}

func Uniform3f(loc int32, x, y, z float32) {
	if u, ok := uni(loc); ok {
		call("uniform3f", u, x, y, z)
	}
}

func Uniform4f(loc int32, x, y, z, w float32) {
	if u, ok := uni(loc); ok {
		call("uniform4f", u, x, y, z, w)
	}
}

func Uniform1fv(loc int32, v []float32) {
	if u, ok := uni(loc); ok && len(v) > 0 {
		putBytes(F32Bytes(v))
		call("uniform1fv", u, scratchF32, 0, len(v))
	}
}

func Uniform4fv(loc int32, v []float32) {
	if u, ok := uni(loc); ok && len(v) >= 4 {
		putBytes(F32Bytes(v))
		call("uniform4fv", u, scratchF32, 0, len(v))
	}
}

func UniformMatrix3fv(loc int32, v []float32) {
	if u, ok := uni(loc); ok && len(v) >= 9 {
		putBytes(F32Bytes(v))
		call("uniformMatrix3fv", u, false, scratchF32, 0, 9)
	}
}

func UniformMatrix4fv(loc int32, v []float32) {
	if u, ok := uni(loc); ok && len(v) >= 16 {
		putBytes(F32Bytes(v))
		call("uniformMatrix4fv", u, false, scratchF32, 0, 16)
	}
}

// ---------------------------------------------------------------------------
// Textures
// ---------------------------------------------------------------------------

func GenTextures(n int32, ids *uint32) {
	for i := int32(0); i < n; i++ {
		*(*uint32)(unsafe.Add(unsafe.Pointer(ids), uintptr(i)*4)) = newObject(call("createTexture"))
	}
}

func DeleteTextures(n int32, ids *uint32) {
	for i := int32(0); i < n; i++ {
		p := (*uint32)(unsafe.Add(unsafe.Pointer(ids), uintptr(i)*4))
		call("deleteTexture", obj(*p))
		release(*p)
	}
}

func ActiveTexture(texture uint32)       { call("activeTexture", texture) }
func BindTexture(target, texture uint32) { call("bindTexture", target, obj(texture)) }

func TexParameteri(target, pname uint32, param int32) { call("texParameteri", target, pname, param) }
func GenerateMipmap(target uint32)                    { call("generateMipmap", target) }

// TexImage2D uploads pixels, whose layout must match format and typ. A nil pixels slice only allocates.
func TexImage2D(target uint32, level int32, internalFormat int32, width, height, border int32, format, typ uint32, pixels []byte) {
	if len(pixels) == 0 {
		call("texImage2D", target, level, internalFormat, width, height, border, format, typ, jsNull)
		return
	}
	call("texImage2D", target, level, internalFormat, width, height, border, format, typ, typedView(pixels, typ), 0)
}

func TexSubImage2D(target uint32, level int32, x, y, width, height int32, format, typ uint32, pixels []byte) {
	if len(pixels) == 0 {
		return
	}
	call("texSubImage2D", target, level, x, y, width, height, format, typ, typedView(pixels, typ), 0)
}

func CopyTexSubImage2D(target uint32, level int32, xoffset, yoffset, x, y, width, height int32) {
	call("copyTexSubImage2D", target, level, xoffset, yoffset, x, y, width, height)
}

// ---------------------------------------------------------------------------
// Framebuffers
// ---------------------------------------------------------------------------

func GenFramebuffers(n int32, ids *uint32) {
	for i := int32(0); i < n; i++ {
		*(*uint32)(unsafe.Add(unsafe.Pointer(ids), uintptr(i)*4)) = newObject(call("createFramebuffer"))
	}
}

func DeleteFramebuffers(n int32, ids *uint32) {
	for i := int32(0); i < n; i++ {
		p := (*uint32)(unsafe.Add(unsafe.Pointer(ids), uintptr(i)*4))
		call("deleteFramebuffer", obj(*p))
		release(*p)
	}
}

func BindFramebuffer(target, fb uint32) { call("bindFramebuffer", target, obj(fb)) }

func FramebufferTexture2D(target, attachment, textarget, texture uint32, level int32) {
	call("framebufferTexture2D", target, attachment, textarget, obj(texture), level)
}

func FramebufferRenderbuffer(target, attachment, rbTarget, rb uint32) {
	call("framebufferRenderbuffer", target, attachment, rbTarget, obj(rb))
}

func CheckFramebufferStatus(target uint32) uint32 {
	return uint32(call("checkFramebufferStatus", target).Int())
}

func BlitFramebuffer(sx0, sy0, sx1, sy1, dx0, dy0, dx1, dy1 int32, mask, filter uint32) {
	call("blitFramebuffer", sx0, sy0, sx1, sy1, dx0, dy0, dx1, dy1, mask, filter)
}

func ReadBuffer(src uint32) { call("readBuffer", src) }

func GenRenderbuffers(n int32, ids *uint32) {
	for i := int32(0); i < n; i++ {
		*(*uint32)(unsafe.Add(unsafe.Pointer(ids), uintptr(i)*4)) = newObject(call("createRenderbuffer"))
	}
}

func BindRenderbuffer(target, rb uint32) { call("bindRenderbuffer", target, obj(rb)) }

func RenderbufferStorage(target, internalFormat uint32, width, height int32) {
	call("renderbufferStorage", target, internalFormat, width, height)
}

// ReadPixels reads from the bound read framebuffer into data
func ReadPixels(x, y, width, height int32, format, typ uint32, data []byte) {
	n := int(width) * int(height) * 4
	if len(data) < n {
		return
	}
	ensureScratch(n)
	call("readPixels", x, y, width, height, format, typ, scratchU8)
	js.CopyBytesToGo(data[:n], scratchU8)
}
