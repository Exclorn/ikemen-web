//go:build js

// WebGL 2 renderer for the browser build. It is a port of the OpenGL ES renderer
// (render_gles32.go) on top of the glw package.
//
// 3D models and model shadows are not supported in this renderer. They need geometry
// shaders and cube map arrays, which WebGL 2 does not have. IsModelEnabled and
// IsShadowEnabled always return false, so the engine never calls the model functions,
// which are empty here.

package main

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"time"

	mgl "github.com/go-gl/mathgl/mgl32"
	gl "github.com/ikemen-engine/Ikemen-GO/src/glw"
)

// ------------------------------------------------------------------
// ShaderProgram_WebGL2

type ShaderProgram_WebGL2 struct {
	program       uint32           // GL handle
	attributes    map[string]int32 // Attribute name to location
	uniforms      map[string]int32 // Uniform name to location
	textures      map[string]int   // Sampler name to texture unit
	name          string           // For debugging
	needsGrabPass bool
}

func (r *Renderer_WebGL2) newShaderProgram(vert, frag, geo, name string, crashWhenFail bool) (s *ShaderProgram_WebGL2, err error) {
	if len(geo) > 0 {
		err = fmt.Errorf("geometry shaders are not supported by WebGL 2 (%s)", name)
		chkEX(err, "Shader compilation error on "+name+"\n", crashWhenFail)
		return nil, err
	}

	vertObj, err := r.compileShader(gl.VERTEX_SHADER, vert)
	if chkEX(err, "Shader compilation error on "+name+"\n", crashWhenFail) {
		return nil, err
	}
	fragObj, err := r.compileShader(gl.FRAGMENT_SHADER, frag)
	if chkEX(err, "Shader compilation error on "+name+"\n", crashWhenFail) {
		gl.DeleteShader(vertObj)
		return nil, err
	}
	prog, err := r.linkProgram(vertObj, fragObj)
	if chkEX(err, "Link program error on "+name+"\n", crashWhenFail) {
		return nil, err
	}

	s = &ShaderProgram_WebGL2{program: prog, name: name}
	s.attributes = make(map[string]int32)
	s.uniforms = make(map[string]int32)
	s.textures = make(map[string]int)
	return s, nil
}

func (s *ShaderProgram_WebGL2) RegisterAttributes(names ...string) {
	for _, name := range names {
		s.attributes[name] = gl.GetAttribLocation(s.program, name)
	}
}

func (s *ShaderProgram_WebGL2) RegisterUniforms(names ...string) {
	for _, name := range names {
		s.uniforms[name] = gl.GetUniformLocation(s.program, name)
	}
}

func (s *ShaderProgram_WebGL2) RegisterTextures(names ...string) {
	for _, name := range names {
		s.uniforms[name] = gl.GetUniformLocation(s.program, name)
		s.textures[name] = len(s.textures)
	}
}

func (r *Renderer_WebGL2) compileShader(shaderType uint32, src string) (uint32, error) {
	// WebGL 2 uses GLSL ES 3.00. The version line has to come first
	fullSrc := src
	if !strings.HasPrefix(strings.TrimSpace(src), "#version") {
		fullSrc = "#version 300 es\n" + src
	}

	typeName := "vertex"
	if shaderType == gl.FRAGMENT_SHADER {
		typeName = "fragment"
	}

	shader := gl.CreateShader(shaderType)
	gl.ShaderSource(shader, fullSrc)
	gl.CompileShader(shader)
	if !gl.GetShaderCompileStatus(shader) {
		log := gl.GetShaderInfoLog(shader)
		gl.DeleteShader(shader)
		return 0, fmt.Errorf("WebGL %s shader error: %s", typeName, log)
	}
	return shader, nil
}

func (r *Renderer_WebGL2) linkProgram(shaders ...uint32) (uint32, error) {
	program := gl.CreateProgram()
	for _, sh := range shaders {
		gl.AttachShader(program, sh)
	}
	gl.LinkProgram(program)
	// Shaders are deleted once the program no longer needs them
	for _, sh := range shaders {
		gl.DetachShader(program, sh)
		gl.DeleteShader(sh)
	}
	if !gl.GetProgramLinkStatus(program) {
		log := gl.GetProgramInfoLog(program)
		gl.DeleteProgram(program)
		return 0, fmt.Errorf("link error: %s", log)
	}
	return program, nil
}

// ------------------------------------------------------------------
// Texture_WebGL2

// Tracks what's actually bound to a unit right now
// tex is used for wrapped textures (compared by identity); handle/target alone for raw GL handles
type boundTexture struct {
	tex    *Texture_WebGL2
	handle uint32
	target uint32
}

// Binds only if it isn't already bound there, per the shared table
func (r *Renderer_WebGL2) bindTextureToUnit(unit int32, target uint32, tex *Texture_WebGL2, handle uint32) bool {
	if tex != nil {
		handle = tex.handle
	}

	cur := &r.boundTex[unit]
	if cur.tex == tex && cur.handle == handle && cur.target == target {
		return false
	}

	gl.ActiveTexture(gl.TEXTURE0 + uint32(unit))
	gl.BindTexture(target, handle)

	cur.tex = tex
	cur.handle = handle
	cur.target = target
	return true
}

// Always rebinds. Used before any call that edits texture data since those act on whatever's active
func (r *Renderer_WebGL2) bindTextureToUnitForced(unit int32, target uint32, tex *Texture_WebGL2, handle uint32) {
	if tex != nil {
		handle = tex.handle
	}

	gl.ActiveTexture(gl.TEXTURE0 + uint32(unit))
	gl.BindTexture(target, handle)

	cur := &r.boundTex[unit]
	cur.tex = tex
	cur.handle = handle
	cur.target = target
}

type Texture_WebGL2 struct {
	width  int32
	height int32
	depth  int32
	filter bool
	handle uint32 // GL side handle
}

func (r *Renderer_WebGL2) generateTexture(width, height, depth int32, filter bool) *Texture_WebGL2 {
	var h uint32
	gl.GenTextures(1, &h)

	tex := &Texture_WebGL2{
		width:  width,
		height: height,
		depth:  depth,
		filter: filter,
		handle: h,
	}

	runtime.SetFinalizer(tex, func(t *Texture_WebGL2) {
		sys.mainThreadTask <- func() {
			gl.DeleteTextures(1, &t.handle)
		}
	})

	return tex
}

// Creates a generic texture
func (r *Renderer_WebGL2) newTexture(width, height, depth int32, filter bool) (Texture, error) {
	return r.generateTexture(width, height, depth, filter), nil
}

func (r *Renderer_WebGL2) newPaletteTexture() Texture {
	t, _ := r.newTexture(256, 1, 32, false)
	return t
}

func (r *Renderer_WebGL2) newModelTexture(width, height, depth int32, filter bool) (Texture, error) {
	return r.newTexture(width, height, depth, filter)
}

func (r *Renderer_WebGL2) newDataTexture(width, height int32) (Texture, error) {
	t := r.generateTexture(width, height, 32, false)

	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, t, 0)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	return t, nil
}

func (r *Renderer_WebGL2) newHDRTexture(width, height int32) (Texture, error) {
	t := r.generateTexture(width, height, 24, false)

	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, t, 0)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.MIRRORED_REPEAT)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.MIRRORED_REPEAT)
	return t, nil
}

func (r *Renderer_WebGL2) newCubeMapTexture(widthHeight int32, mipmap bool, lowestMipLevel int32) (Texture, error) {
	t := r.generateTexture(widthHeight, widthHeight, 24, false)

	r.bindTextureToUnitForced(0, gl.TEXTURE_CUBE_MAP, t, 0)
	for i := 0; i < 6; i++ {
		gl.TexImage2D(uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+i), 0, gl.RGBA16F, widthHeight, widthHeight, 0, gl.RGBA, gl.HALF_FLOAT, nil)
	}

	if mipmap {
		gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
		gl.GenerateMipmap(gl.TEXTURE_CUBE_MAP)
	} else {
		gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	}

	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	return t, nil
}

// Bind a texture and upload texel data to it
func (t *Texture_WebGL2) SetData(data []byte) {
	var interp int32 = gl.NEAREST
	if t.filter {
		interp = gl.LINEAR
	}

	bits := Max(t.depth, 8)
	internalFormat := t.MapSizedInternalFormat(bits)
	uploadFormat := t.MapUploadFormat(bits)
	uploadType := t.MapUploadType(bits)

	r := gfx.(*Renderer_WebGL2)
	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, t, 0)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, 0)

	// A nil slice allocates storage without uploading anything
	gl.TexImage2D(gl.TEXTURE_2D, 0, int32(internalFormat), t.width, t.height, 0, uploadFormat, uploadType, data)

	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, interp)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, interp)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
}

func (t *Texture_WebGL2) SetSubData(data []byte, x, y, width, height, stride int32) {
	if data == nil {
		return
	}

	var interp int32 = gl.NEAREST
	if t.filter {
		interp = gl.LINEAR
	}

	r := gfx.(*Renderer_WebGL2)
	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, t, 0)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)

	bits := Max(t.depth, 8)
	uploadFormat := t.MapUploadFormat(bits)
	uploadType := t.MapUploadType(bits)
	bytesPerPixel := t.depth / 8
	if bytesPerPixel < 1 {
		bytesPerPixel = 1
	}

	var rowLength int32 = 0
	if stride != width*bytesPerPixel {
		rowLength = stride / bytesPerPixel
	}

	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, rowLength)
	gl.TexSubImage2D(gl.TEXTURE_2D, 0, x, y, width, height, uploadFormat, uploadType, data)
	if rowLength != 0 {
		gl.PixelStorei(gl.UNPACK_ROW_LENGTH, 0)
	}

	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, interp)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, interp)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
}

func (t *Texture_WebGL2) SetDataG(data []byte, mag, min, ws, wt TextureSamplingParam) {
	bits := Max(t.depth, 8)
	internalFormat := t.MapSizedInternalFormat(bits)
	uploadFormat := t.MapUploadFormat(bits)
	uploadType := t.MapUploadType(bits)

	r := gfx.(*Renderer_WebGL2)
	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, t, 0)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, 0)
	gl.TexImage2D(gl.TEXTURE_2D, 0, int32(internalFormat), t.width, t.height, 0, uploadFormat, uploadType, data)
	gl.GenerateMipmap(gl.TEXTURE_2D)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, t.MapTextureSamplingParam(mag))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, t.MapTextureSamplingParam(min))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, t.MapTextureSamplingParam(ws))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, t.MapTextureSamplingParam(wt))
}

func (t *Texture_WebGL2) SetPixelData(data []float32) {
	r := gfx.(*Renderer_WebGL2)
	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, t, 0)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, 0)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA32F, t.width, t.height, 0, gl.RGBA, gl.FLOAT, gl.F32Bytes(data))
}

func (t Texture_WebGL2) CopyData(src *Texture) {
	r := gfx.(*Renderer_WebGL2)

	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, nil, 0) // Unbind whatever is currently bound
	srcTex := (*src).(*Texture_WebGL2)
	var fbo uint32
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.READ_FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, srcTex.handle, 0)

	// t is a value receiver here, not the real *Texture_WebGL2, so track by handle rather than identity
	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, nil, t.handle)

	// Copy the old texture data into the top-left of the new, larger texture
	gl.CopyTexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, 0, 0, srcTex.width, srcTex.height)

	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, 0)
	gl.DeleteFramebuffers(1, &fbo)
}

// Return whether texture has a valid handle
func (t *Texture_WebGL2) IsValid() bool {
	return t.width != 0 && t.height != 0 && t.handle != 0
}

func (t *Texture_WebGL2) MarkNonSwappable() {
	// No-op: this renderer does not have VRAM swap-out
}

func (t *Texture_WebGL2) GetWidth() int32 {
	return t.width
}

func (t *Texture_WebGL2) GetHeight() int32 {
	return t.height
}

func (t *Texture_WebGL2) MapUploadType(i int32) uint32 {
	switch i {
	case 96, 128:
		return gl.FLOAT
	default:
		return gl.UNSIGNED_BYTE
	}
}

func (t *Texture_WebGL2) MapUploadFormat(i int32) uint32 {
	switch i {
	case 8:
		return gl.RED
	case 24:
		return gl.RGB
	case 32:
		return gl.RGBA
	case 96:
		return gl.RGB
	case 128:
		return gl.RGBA
	default:
		return gl.RGBA
	}
}

func (t *Texture_WebGL2) MapSizedInternalFormat(i int32) uint32 {
	switch i {
	case 8:
		return gl.R8
	case 24:
		return gl.RGB8
	case 32:
		return gl.RGBA8
	case 96:
		return gl.RGB32F
	case 128:
		return gl.RGBA32F
	default:
		return gl.RGBA8
	}
}

func (t *Texture_WebGL2) MapTextureSamplingParam(i TextureSamplingParam) int32 {
	switch i {
	case TextureSamplingFilterNearest:
		return gl.NEAREST
	case TextureSamplingFilterLinear:
		return gl.LINEAR
	case TextureSamplingFilterNearestMipMapNearest:
		return gl.NEAREST_MIPMAP_NEAREST
	case TextureSamplingFilterLinearMipMapNearest:
		return gl.LINEAR_MIPMAP_NEAREST
	case TextureSamplingFilterNearestMipMapLinear:
		return gl.NEAREST_MIPMAP_LINEAR
	case TextureSamplingFilterLinearMipMapLinear:
		return gl.LINEAR_MIPMAP_LINEAR
	case TextureSamplingWrapClampToEdge:
		return gl.CLAMP_TO_EDGE
	case TextureSamplingWrapMirroredRepeat:
		return gl.MIRRORED_REPEAT
	case TextureSamplingWrapRepeat:
		return gl.REPEAT
	}
	return 0
}

// ------------------------------------------------------------------
// Renderer_WebGL2

type Renderer_WebGL2 struct {
	fbo         uint32
	fbo_texture uint32
	rbo_depth   uint32
	// Post-processing FBOs
	fbo_pp         []uint32
	fbo_pp_texture []uint32
	// Post-processing shaders
	postVertBuffer   uint32
	postShaderSelect []*ShaderProgram_WebGL2
	// Custom shaders
	customShaders   map[uint32]*ShaderProgram_WebGL2
	customShaderMap map[string]uint32
	nextShaderID    uint32
	currentProgram  *ShaderProgram_WebGL2
	grabTexture     *Texture_WebGL2
	// Shader and vertex data for primitive rendering
	spriteShader   *ShaderProgram_WebGL2
	vertexBuffer   uint32
	spriteVAO      uint32
	postVAO        uint32
	capturePixels  []byte
	capturePending bool

	WebGL2State
}

type WebGL2State struct {
	program          uint32
	depthTest        bool
	depthMask        bool
	invertFrontFace  bool
	doubleSided      bool
	blendEnabled     bool
	blendEquation    BlendEquation
	blendSrc         BlendFunc
	blendDst         BlendFunc
	scissorRect      [4]int32
	scissorEnabled   bool
	boundTex         []boundTexture // Caches texture units to what's actually bound there. Sized per GPU
	texCacheLastUsed []uint64       // Sprite-only recency, for LRU eviction. Sized per GPU
	texCacheTimer    uint64         // Increments on every sprite texture access
	uniformICache    map[uint32]int32
	uniformF1Cache   map[uint32]float32
	uniformF2Cache   map[uint32][2]float32
	uniformF3Cache   map[uint32][3]float32
	uniformF4Cache   map[uint32][4]float32
	boundBuffers     map[uint32]uint32
	currentVAO       uint32
}

func (r *Renderer_WebGL2) GetName() string {
	// Must start with "OpenGL" so the engine treats this as a GL-style backend
	return "OpenGL ES 3.0 (WebGL 2)"
}

func (r *Renderer_WebGL2) DebugInfo() string {
	return ""
}

// Render initialization.
// Creates the default shaders and the framebuffers.
func (r *Renderer_WebGL2) Init() {
	if !gl.Init(sys.window.gl) {
		panic("WebGL 2 context is not available")
	}
	LogMessage("Using %v (%v)", gl.GetString(gl.VERSION), gl.GetString(gl.RENDERER))

	// Multisampling is not used by this renderer
	sys.msaa = 0

	r.InitStateCache()

	r.customShaders = make(map[uint32]*ShaderProgram_WebGL2)
	r.customShaderMap = make(map[string]uint32)
	r.nextShaderID = 1
	r.currentProgram = nil

	// Models are not supported
	sys.cfg.Video.EnableModel = false
	sys.cfg.Video.EnableModelShadow = false

	// Generate VAO's
	gl.GenVertexArrays(1, &r.spriteVAO)
	gl.GenVertexArrays(1, &r.postVAO)

	// Generate buffers
	gl.GenBuffers(1, &r.vertexBuffer)
	gl.GenBuffers(1, &r.postVertBuffer)

	// Pre-size to 64 bytes (one quad) so later uploads of the same size stay cheap
	r.bindBuffer(gl.ARRAY_BUFFER, r.vertexBuffer)
	gl.BufferData(gl.ARRAY_BUFFER, 64, nil, gl.DYNAMIC_DRAW)

	// Initialize post-processing vertex buffer (a full screen quad)
	r.bindBuffer(gl.ARRAY_BUFFER, r.postVertBuffer)
	postVertData := []float32{-1, -1, 1, -1, -1, 1, 1, 1}
	gl.BufferData(gl.ARRAY_BUFFER, len(postVertData)*4, gl.F32Bytes(postVertData), gl.STATIC_DRAW)

	// Unbind for safety
	r.bindBuffer(gl.ARRAY_BUFFER, 0)

	// Sprite shader
	r.spriteShader, _ = r.newShaderProgram(vertShader, fragShader, "", "Main Shader", true)
	r.spriteShader.RegisterAttributes("position", "uv")
	r.spriteShader.RegisterUniforms("modelview", "projection", "x1x2x4x3",
		"alpha", "tint", "mask", "neg", "gray", "add", "mult", "isFlat", "isRgba", "isTrapez", "hue")
	r.spriteShader.RegisterTextures("pal", "tex")

	// Configure spriteVAO
	r.bindVertexArray(r.spriteVAO)
	r.bindBuffer(gl.ARRAY_BUFFER, r.vertexBuffer)

	locPos := r.spriteShader.attributes["position"]
	gl.EnableVertexAttribArray(uint32(locPos))
	gl.VertexAttribPointerWithOffset(uint32(locPos), 2, gl.FLOAT, false, 16, 0)

	locUV := r.spriteShader.attributes["uv"]
	gl.EnableVertexAttribArray(uint32(locUV))
	gl.VertexAttribPointerWithOffset(uint32(locUV), 2, gl.FLOAT, false, 16, 8)

	// Unbind for safety
	r.bindVertexArray(0)

	// Compile post-processing shaders
	// Pre-allocate the shader slice to accommodate all external shaders plus the identity shader
	r.postShaderSelect = make([]*ShaderProgram_WebGL2, len(sys.cfg.Video.ExternalShaders)+1)

	// Configure postVAO
	r.bindVertexArray(r.postVAO)
	r.bindBuffer(gl.ARRAY_BUFFER, r.postVertBuffer)

	// External shaders
	for i := 0; i < len(sys.cfg.Video.ExternalShaders); i++ {
		r.postShaderSelect[i], _ = r.newShaderProgram(string(sys.externalShaders[0][i]), string(sys.externalShaders[1][i]),
			"", fmt.Sprintf("Postprocess Shader #%v", i), true)
		r.postShaderSelect[i].RegisterAttributes("VertCoord")
		r.postShaderSelect[i].RegisterUniforms("Texture", "TextureSize", "CurrentTime")

		// Configure postVAO for this specific shader's attribute location
		if loc, ok := r.postShaderSelect[i].attributes["VertCoord"]; ok && loc >= 0 {
			gl.EnableVertexAttribArray(uint32(loc))
			gl.VertexAttribPointerWithOffset(uint32(loc), 2, gl.FLOAT, false, 0, 0)
		}
	}

	// Identity shader (no post-processing). This should be the last one
	identShader, _ := r.newShaderProgram(identVertShader, identFragShader, "", "Identity Postprocess", true)
	identShader.RegisterAttributes("VertCoord")
	if loc, ok := identShader.attributes["VertCoord"]; ok && loc >= 0 {
		gl.EnableVertexAttribArray(uint32(loc))
		gl.VertexAttribPointerWithOffset(uint32(loc), 2, gl.FLOAT, false, 0, 0)
	}
	r.postShaderSelect[len(r.postShaderSelect)-1] = identShader

	// Unbind for safety
	r.bindVertexArray(0)
	r.bindBuffer(gl.ARRAY_BUFFER, 0)

	grabTex, _ := r.newTexture(sys.scrrect[2], sys.scrrect[3], 32, true)
	r.grabTexture = grabTex.(*Texture_WebGL2)
	r.grabTexture.SetData(nil)

	// Create a texture for r.fbo
	gl.GenTextures(1, &r.fbo_texture)
	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, nil, r.fbo_texture)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)

	// Don't change this from RGBA8. It breaks mixing between subtractive and additive.
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, sys.scrrect[2], sys.scrrect[3], 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)

	// Shaders might use negative values, so signed pixels are used when the browser can render to them
	ppFormat := int32(gl.RGBA8)
	if gl.HasExtension("EXT_render_snorm") {
		ppFormat = gl.RGBA8_SNORM
	}

	r.fbo_pp = make([]uint32, 2)
	r.fbo_pp_texture = make([]uint32, 2)
	for i := 0; i < 2; i++ {
		gl.GenTextures(1, &(r.fbo_pp_texture[i]))

		r.bindTextureToUnitForced(0, gl.TEXTURE_2D, nil, r.fbo_pp_texture[i])
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
		gl.TexImage2D(gl.TEXTURE_2D, 0, ppFormat, sys.scrrect[2], sys.scrrect[3], 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	}

	// Done with the textures, unbind
	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, nil, 0)

	gl.GenRenderbuffers(1, &r.rbo_depth)
	gl.BindRenderbuffer(gl.RENDERBUFFER, r.rbo_depth)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.DEPTH_COMPONENT16, sys.scrrect[2], sys.scrrect[3])
	gl.BindRenderbuffer(gl.RENDERBUFFER, 0)

	// Create an FBO for the main render target
	gl.GenFramebuffers(1, &r.fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, r.fbo_texture, 0)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.RENDERBUFFER, r.rbo_depth)
	if status := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); status != gl.FRAMEBUFFER_COMPLETE {
		LogMessage("Framebuffer creation failed: 0x%x", status)
	}

	// Create two FBOs for post-processing
	for i := 0; i < 2; i++ {
		gl.GenFramebuffers(1, &(r.fbo_pp[i]))
		gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo_pp[i])
		gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, r.fbo_pp_texture[i], 0)
	}

	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
}

func (r *Renderer_WebGL2) Close() {
}

func (r *Renderer_WebGL2) InitStateCache() {
	// Match standard OpenGL hardware defaults
	r.program = 0
	r.depthTest = false
	r.depthMask = true
	r.doubleSided = true
	r.invertFrontFace = false
	r.blendEnabled = false
	r.scissorEnabled = false

	// Force hardware synchronization
	gl.UseProgram(0)
	gl.Disable(gl.DEPTH_TEST)
	gl.DepthMask(true)
	gl.Disable(gl.CULL_FACE)
	gl.FrontFace(gl.CCW)
	gl.Disable(gl.BLEND)
	gl.Disable(gl.SCISSOR_TEST)

	// Check hardware texture limit
	maxTex := gl.GetInteger(gl.MAX_TEXTURE_IMAGE_UNITS)
	if maxTex < 8 {
		maxTex = 8
	}

	// Initialize texture cache
	r.boundTex = make([]boundTexture, maxTex)
	r.texCacheLastUsed = make([]uint64, maxTex)

	// Initialize uniform cache
	r.uniformICache = make(map[uint32]int32, 32)
	r.uniformF1Cache = make(map[uint32]float32, 32)
	r.uniformF2Cache = make(map[uint32][2]float32, 32)
	r.uniformF3Cache = make(map[uint32][3]float32, 32)
	r.uniformF4Cache = make(map[uint32][4]float32, 32)

	// Initialize bound buffer cache
	r.boundBuffers = make(map[uint32]uint32)
}

func (r *Renderer_WebGL2) EnableDebug() {}

// 3D models and their shadows are not supported by this renderer
func (r *Renderer_WebGL2) IsModelEnabled() bool  { return false }
func (r *Renderer_WebGL2) IsShadowEnabled() bool { return false }

func (r *Renderer_WebGL2) BeginFrame(clearColor bool) {
	gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo)
	gl.Viewport(0, 0, sys.scrrect[2], sys.scrrect[3])
	if clearColor {
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	} else {
		gl.Clear(gl.DEPTH_BUFFER_BIT)
	}
}

func (r *Renderer_WebGL2) EndFrame() {
	if len(r.fbo_pp) == 0 {
		return
	}

	x, y, width, height := int32(0), int32(0), int32(sys.scrrect[2]), int32(sys.scrrect[3])
	frameTime := uint64(time.Now().UnixNano()) // consistent time across all shaders

	var scaleMode int32 // GL enum
	if sys.cfg.Video.WindowScaleMode {
		scaleMode = gl.LINEAR
	} else {
		scaleMode = gl.NEAREST
	}

	// set the viewport to the unscaled bounds for post-processing
	gl.Viewport(x, y, width, height)
	// clear both of our post-processing FBOs to make sure
	// nothing's there. the output is set later
	for i := 0; i < 2; i++ {
		gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo_pp[i])
		gl.Clear(gl.COLOR_BUFFER_BIT)
	}
	fbo_texture := r.fbo_texture

	// Reset global state
	r.DisableScissor()
	r.DisableBlending()
	r.SetDepthTest(false)
	r.SetDepthMask(false)

	for i := 0; i < len(r.postShaderSelect); i++ {
		postShader := r.postShaderSelect[i]

		// tell GL we want to use our shader program
		r.ChangeProgram(postShader.program)

		// tell GL to use our vertex array object
		// this'll be where our quad is stored
		r.bindVertexArray(r.postVAO)

		// this is here because it is undefined
		// behavior to write to the same FBO
		if i%2 == 0 {
			// ping! our first post-processing FBO is the output
			gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo_pp[0])
			if i == 0 {
				// first pass, use fbo_texture
				r.bindTextureToUnit(0, gl.TEXTURE_2D, nil, fbo_texture)
			} else {
				// not the first pass, use the second post-processing FBO
				r.bindTextureToUnit(0, gl.TEXTURE_2D, nil, r.fbo_pp_texture[1])
			}
		} else {
			// pong! our second post-processing FBO is the output
			gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo_pp[1])
			// our first post-processing FBO is the input
			r.bindTextureToUnit(0, gl.TEXTURE_2D, nil, r.fbo_pp_texture[0])
		}

		if i >= len(r.postShaderSelect)-1 {
			// this is the last shader,
			// so we ask GL to scale it and output it
			// to the default frame buffer that the user sees
			x, y, width, height := sys.window.GetScaledViewportSize()
			gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
			// clear the whole canvas first, so the bars around the picture are black
			canvasW, canvasH := sys.window.GetDrawablePixelSize()
			gl.Viewport(0, 0, canvasW, canvasH)
			gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
			gl.Viewport(x, y, width, height)
		}

		// set post-processing parameters
		if loc, ok := postShader.uniforms["Texture"]; ok && loc >= 0 {
			r.SetUniformISub(loc, 0)
		}
		if loc, ok := postShader.uniforms["TextureSize"]; ok && loc >= 0 {
			r.SetUniformFSub(loc, float32(width), float32(height))
		}
		if loc, ok := postShader.uniforms["CurrentTime"]; ok && loc >= 0 {
			r.SetUniformFSub(loc, float32(frameTime))
		}

		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, scaleMode)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, scaleMode)

		// construct the quad and draw it
		gl.DrawArrays(gl.TRIANGLE_STRIP, 0, 4)
	}
}

func (r *Renderer_WebGL2) Await() {}

func (r *Renderer_WebGL2) MapBlendEquation(i BlendEquation) uint32 {
	switch i {
	case BlendReverseSubtract:
		return gl.FUNC_REVERSE_SUBTRACT
	}
	return gl.FUNC_ADD
}

func (r *Renderer_WebGL2) MapBlendFunction(i BlendFunc) uint32 {
	switch i {
	case BlendOne:
		return gl.ONE
	case BlendZero:
		return gl.ZERO
	case BlendSrcAlpha:
		return gl.SRC_ALPHA
	case BlendOneMinusSrcAlpha:
		return gl.ONE_MINUS_SRC_ALPHA
	case BlendDstColor:
		return gl.DST_COLOR
	case BlendOneMinusDstColor:
		return gl.ONE_MINUS_DST_COLOR
	}
	return gl.ZERO
}

func (r *Renderer_WebGL2) MapPrimitiveMode(i PrimitiveMode) uint32 {
	switch i {
	case LINES:
		return gl.LINES
	case LINE_LOOP:
		return gl.LINE_LOOP
	case LINE_STRIP:
		return gl.LINE_STRIP
	case TRIANGLES:
		return gl.TRIANGLES
	case TRIANGLE_STRIP:
		return gl.TRIANGLE_STRIP
	case TRIANGLE_FAN:
		return gl.TRIANGLE_FAN
	}
	return gl.TRIANGLES
}

func (r *Renderer_WebGL2) SetDepthTest(depthTest bool) {
	if depthTest != r.depthTest {
		r.depthTest = depthTest
		if depthTest {
			gl.Enable(gl.DEPTH_TEST)
			gl.DepthFunc(gl.LESS)
		} else {
			gl.Disable(gl.DEPTH_TEST)
		}
	}
}

// Note: This one defaults to enable so we must sync the cache early
func (r *Renderer_WebGL2) SetDepthMask(depthMask bool) {
	if depthMask != r.depthMask {
		r.depthMask = depthMask
		gl.DepthMask(depthMask)
	}
}

// This should be called instead of gl.UseProgram()
func (r *Renderer_WebGL2) ChangeProgram(prog uint32) {
	// Program already in use
	if r.program == prog {
		return
	}

	// Lazy release of sprite pipeline
	// We can't tell if the next thing we will draw is also a sprite, so this prevents releasing the pipeline after every single sprite
	if r.spriteShader != nil && r.program == r.spriteShader.program {
		r.ReleasePipeline()
	}

	// Same for TTF fonts
	if fr, ok := gfxFont.(*FontRenderer_WebGL2); ok && fr.shaderProgram != nil && r.program == fr.shaderProgram.program {
		fr.ReleaseFontPipeline()
	}

	// Switch program
	gl.UseProgram(prog)
	r.program = prog

	// Reset the texture cache
	for i := range r.boundTex {
		r.boundTex[i] = boundTexture{}
		r.texCacheLastUsed[i] = 0
	}
	r.texCacheTimer = 1
}

func (r *Renderer_WebGL2) EnableBlending(eq BlendEquation, src, dst BlendFunc) {
	if !r.blendEnabled {
		gl.Enable(gl.BLEND)
		r.blendEnabled = true
	}

	if eq != r.blendEquation {
		r.blendEquation = eq
		gl.BlendEquation(r.MapBlendEquation(eq))
	}

	if src != r.blendSrc || dst != r.blendDst {
		r.blendSrc = src
		r.blendDst = dst
		gl.BlendFunc(r.MapBlendFunction(src), r.MapBlendFunction(dst))
	}
}

func (r *Renderer_WebGL2) DisableBlending() {
	if r.blendEnabled {
		gl.Disable(gl.BLEND)
		r.blendEnabled = false
		// Do not update blend equation cache because the hardware doesn't
	}
}

func (r *Renderer_WebGL2) SetPipeline() {
	// Do nothing if we were already using the sprite shader
	if r.program == r.spriteShader.program {
		return
	}

	r.ChangeProgram(r.spriteShader.program)

	r.bindVertexArray(r.spriteVAO)
}

func (r *Renderer_WebGL2) ReleasePipeline() {
	r.bindVertexArray(0)
}

// ------------------------------------------------------------------
// 3D models and shadows are not supported. The engine does not call these because
// IsModelEnabled and IsShadowEnabled are false.

func (r *Renderer_WebGL2) prepareShadowMapPipeline(bufferIndex uint32) {}

func (r *Renderer_WebGL2) setShadowMapPipeline(doubleSided, invertFrontFace, useUV, useNormal, useTangent, useVertColor, useJoint0, useJoint1 bool, numVertices, vertAttrOffset uint32) {
}

func (r *Renderer_WebGL2) ReleaseShadowPipeline() {}

func (r *Renderer_WebGL2) prepareModelPipeline(bufferIndex uint32, env *Environment) {}

func (r *Renderer_WebGL2) SetModelPipeline(eq BlendEquation, src, dst BlendFunc, depthTest, depthMask, doubleSided, invertFrontFace,
	useUV, useNormal, useTangent, useVertColor, useJoint0, useJoint1, useOutlineAttribute bool, numVertices, vertAttrOffset uint32) {
}

func (r *Renderer_WebGL2) SetMeshOutlinePipeline(invertFrontFace bool, meshOutline float32) {}

func (r *Renderer_WebGL2) ReleaseModelPipeline() {}

func (r *Renderer_WebGL2) SetModelUniformI(name string, val int)                   {}
func (r *Renderer_WebGL2) SetModelUniformF(name string, values ...float32)         {}
func (r *Renderer_WebGL2) SetModelUniformFv(name string, values []float32)         {}
func (r *Renderer_WebGL2) SetModelUniformMatrix(name string, value []float32)      {}
func (r *Renderer_WebGL2) SetModelUniformMatrix3(name string, value []float32)     {}
func (r *Renderer_WebGL2) SetShadowMapUniformI(name string, val int)               {}
func (r *Renderer_WebGL2) SetShadowMapUniformF(name string, values ...float32)     {}
func (r *Renderer_WebGL2) SetShadowMapUniformFv(name string, values []float32)     {}
func (r *Renderer_WebGL2) SetShadowMapUniformMatrix(name string, value []float32)  {}
func (r *Renderer_WebGL2) SetShadowMapUniformMatrix3(name string, value []float32) {}
func (r *Renderer_WebGL2) SetModelTexture(name string, t Texture)                  {}
func (r *Renderer_WebGL2) SetShadowMapTexture(name string, t Texture)              {}
func (r *Renderer_WebGL2) SetShadowFrameTexture(i uint32)                          {}
func (r *Renderer_WebGL2) SetShadowFrameCubeTexture(i uint32)                      {}
func (r *Renderer_WebGL2) SetModelVertexData(bufferIndex uint32, values []byte)    {}
func (r *Renderer_WebGL2) SetModelIndexData(bufferIndex uint32, values ...uint32)  {}
func (r *Renderer_WebGL2) RenderElements(mode PrimitiveMode, count, offset int)    {}
func (r *Renderer_WebGL2) RenderShadowMapElements(mode PrimitiveMode, count, offset int) {
}
func (r *Renderer_WebGL2) RenderCubeMap(envTexture Texture, cubeTexture Texture) {}
func (r *Renderer_WebGL2) RenderFilteredCubeMap(distribution int32, cubeTexture Texture, filteredTexture Texture, mipmapLevel, sampleCount int32, roughness float32) {
}
func (r *Renderer_WebGL2) RenderLUT(distribution int32, cubeTexture Texture, lutTexture Texture, sampleCount int32) {
}

// ------------------------------------------------------------------
// Screenshots

func (r *Renderer_WebGL2) ReadPixels(data []uint8, width, height int) {
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, 0)
	gl.ReadPixels(0, 0, int32(width), int32(height), gl.RGBA, gl.UNSIGNED_BYTE, data)
}

// WebGL has no asynchronous pixel readback with a mapped buffer, so the read is done right away
func (r *Renderer_WebGL2) BeginScreenshot(width, height int) {
	r.capturePixels = make([]byte, 4*width*height)
	r.ReadPixels(r.capturePixels, width, height)
	r.capturePending = true
}

func (r *Renderer_WebGL2) FinishScreenshot(data []uint8, width, height int) bool {
	if !r.capturePending || len(data) < 4*width*height || len(r.capturePixels) < 4*width*height {
		return false
	}
	copy(data, r.capturePixels[:4*width*height])
	r.capturePixels = nil
	r.capturePending = false
	return true
}

func (r *Renderer_WebGL2) EnableScissor(x, y, width, height int32) {
	// Flip Y to OpenGL convention
	realY := sys.scrrect[3] - (y + height)

	if r.scissorEnabled &&
		r.scissorRect[0] == x && r.scissorRect[1] == realY &&
		r.scissorRect[2] == width && r.scissorRect[3] == height {
		return
	}

	if !r.scissorEnabled {
		gl.Enable(gl.SCISSOR_TEST)
		r.scissorEnabled = true
	}

	gl.Scissor(x, realY, width, height)
	r.scissorRect = [4]int32{x, realY, width, height}
}

func (r *Renderer_WebGL2) DisableScissor() {
	if r.scissorEnabled {
		gl.Disable(gl.SCISSOR_TEST)
		r.scissorEnabled = false
		// Do not zero r.scissorRect here because the hardware retains the last rect even when the test is off
	}
}

// ------------------------------------------------------------------
// Uniforms

func (r *Renderer_WebGL2) SetUniformISub(loc int32, val int32) {
	if loc < 0 {
		return
	}

	// Cached path for the sprite shader
	if r.program == r.spriteShader.program {
		key := (r.program << 16) | uint32(loc)
		if old, exists := r.uniformICache[key]; exists && old == val {
			return
		}
		r.uniformICache[key] = val
	}

	gl.Uniform1i(loc, val)
}

func (r *Renderer_WebGL2) SetUniformFSub(loc int32, values ...float32) {
	if loc < 0 || len(values) == 0 {
		return
	}

	// Cached path for the sprite shader
	if r.program == r.spriteShader.program {
		key := (r.program << 16) | uint32(loc)

		switch len(values) {
		case 1:
			if old, exists := r.uniformF1Cache[key]; exists && old == values[0] {
				return
			}
			r.uniformF1Cache[key] = values[0]
		case 2:
			v2 := [2]float32{values[0], values[1]}
			if old, exists := r.uniformF2Cache[key]; exists && old == v2 {
				return
			}
			r.uniformF2Cache[key] = v2
		case 3:
			v3 := [3]float32{values[0], values[1], values[2]}
			if old, exists := r.uniformF3Cache[key]; exists && old == v3 {
				return
			}
			r.uniformF3Cache[key] = v3
		case 4:
			v4 := [4]float32{values[0], values[1], values[2], values[3]}
			if old, exists := r.uniformF4Cache[key]; exists && old == v4 {
				return
			}
			r.uniformF4Cache[key] = v4
		}
	}

	// Uncached path
	switch len(values) {
	case 1:
		gl.Uniform1f(loc, values[0])
	case 2:
		gl.Uniform2f(loc, values[0], values[1])
	case 3:
		gl.Uniform3f(loc, values[0], values[1], values[2])
	case 4:
		gl.Uniform4f(loc, values[0], values[1], values[2], values[3])
	}
}

func (r *Renderer_WebGL2) SetUniformFvSub(loc int32, values []float32) {
	if loc < 0 || len(values) == 0 {
		return
	}

	switch len(values) {
	case 1, 2, 3, 4:
		r.SetUniformFSub(loc, values...)
	case 8:
		gl.Uniform4fv(loc, values)
	default:
		gl.Uniform1fv(loc, values)
	}
}

func (r *Renderer_WebGL2) SetUniformI(name string, val int) {
	loc := r.currentProgram.uniforms[name]
	r.SetUniformISub(loc, int32(val))
}

func (r *Renderer_WebGL2) SetUniformF(name string, values ...float32) {
	loc := r.currentProgram.uniforms[name]
	r.SetUniformFSub(loc, values...)
}

func (r *Renderer_WebGL2) SetUniformFv(name string, values []float32) {
	loc := r.currentProgram.uniforms[name]
	r.SetUniformFvSub(loc, values)
}

// Caching matrices is as expensive as direct function calls
func (r *Renderer_WebGL2) SetUniformMatrix(name string, value []float32) {
	loc, ok := r.currentProgram.uniforms[name]
	if ok && loc >= 0 {
		gl.UniformMatrix4fv(loc, value)
	}
}

// Checks what texture is bound where before wasting time rebinding them
// Every program skips redundant binds. The sprite shader also uses a LRU system that allows it to use all texture slots for caching
func (r *Renderer_WebGL2) SetTextureSub(uMap map[string]int32, tMap map[string]int, name string, tex Texture) {
	t := tex.(*Texture_WebGL2)
	loc := uMap[name]

	// Cached path for the sprite shader
	// Note: The cache doesn't care if a texture is "tex" or "pal"
	if r.program == r.spriteShader.program {
		// Increment reference timer
		r.texCacheTimer++

		var oldestUnit int32 = 0
		var minTime uint64 = math.MaxUint64

		// Look for a hit or the oldest slot
		for i, bound := range r.boundTex {
			if bound.tex == t {
				r.texCacheLastUsed[i] = r.texCacheTimer
				r.SetUniformISub(loc, int32(i))
				return
			}

			// While searching, track the oldest slot in case we miss
			if r.texCacheLastUsed[i] < minTime {
				minTime = r.texCacheLastUsed[i]
				oldestUnit = int32(i)
			}
		}

		// Cache miss
		r.bindTextureToUnit(oldestUnit, gl.TEXTURE_2D, t, 0)
		r.texCacheLastUsed[oldestUnit] = r.texCacheTimer

		// Update uniform
		r.SetUniformISub(loc, oldestUnit)
		return
	}

	// Fixed unit
	// No LRU needed, but still skip a redundant rebind
	fixedUnit := uint32(tMap[name])
	r.bindTextureToUnit(int32(fixedUnit), gl.TEXTURE_2D, t, 0)
	r.SetUniformISub(loc, int32(fixedUnit))
}

func (r *Renderer_WebGL2) SetTexture(name string, tex Texture) {
	r.SetTextureSub(r.currentProgram.uniforms, r.currentProgram.textures, name, tex)
}

// ------------------------------------------------------------------
// Drawing

func (r *Renderer_WebGL2) SetVertexData(values ...float32) {
	r.bindBuffer(gl.ARRAY_BUFFER, r.vertexBuffer)

	// Upload the new data as a fresh buffer
	// So this draw doesn't overwrite vertices that earlier draws may still be reading
	gl.BufferData(gl.ARRAY_BUFFER, len(values)*4, gl.F32Bytes(values), gl.DYNAMIC_DRAW)
}

func (r *Renderer_WebGL2) RenderQuad() {
	gl.DrawArrays(gl.TRIANGLE_STRIP, 0, 4)
}

func (r *Renderer_WebGL2) RenderQuadBatch(vertexCount int32) {
	gl.DrawArrays(gl.TRIANGLE_STRIP, 0, vertexCount)
}

func (r *Renderer_WebGL2) PerspectiveProjectionMatrix(angle, aspect, near, far float32) mgl.Mat4 {
	return mgl.Perspective(angle, aspect, near, far)
}

func (r *Renderer_WebGL2) OrthographicProjectionMatrix(left, right, bottom, top, near, far float32) mgl.Mat4 {
	return mgl.Ortho(left, right, bottom, top, near, far)
}

// There are no threads to share GL contexts with in the browser
func (r *Renderer_WebGL2) NewWorkerThread() bool {
	return false
}

// The browser decides when frames are shown
func (r *Renderer_WebGL2) SetVSync(interval int) {}

// ------------------------------------------------------------------
// Custom sprite shaders

func (r *Renderer_WebGL2) LoadCustomSpriteShader(shaderName string, shaderData []byte) uint32 {
	if id, ok := r.customShaderMap[shaderName]; ok {
		return id
	}

	fragSource := string(shaderData)

	shader, err := r.newShaderProgram(vertShader, fragSource, "", "Custom Shader: "+shaderName, false)
	if err != nil {
		LogMessage("Failed to compile custom shader %s: %v", shaderName, err)
		return 0
	}

	shader.RegisterAttributes("position", "uv")
	shader.RegisterUniforms("modelview", "projection", "x1x2x4x3",
		"alpha", "tint", "mask", "neg", "gray", "add", "mult", "isFlat", "isRgba", "isTrapez", "hue",
		"iTime", "iResolution", "aspectRatio", "sTime", "param")
	shader.RegisterTextures("pal", "tex", "tex1", "tex2", "bgl_RenderedTexture")

	shader.needsGrabPass = strings.Contains(fragSource, "bgl_RenderedTexture")

	id := r.nextShaderID
	r.nextShaderID++
	r.customShaders[id] = shader
	r.customShaderMap[shaderName] = id

	sys.appendToConsole(fmt.Sprintf("Loaded Custom Shader: %s (ID: %d, NeedsGrabPass: %v)", shaderName, id, shader.needsGrabPass))
	return id
}

func (r *Renderer_WebGL2) UnloadCustomSpriteShader(shaderName string) {
	if id, exists := r.customShaderMap[shaderName]; exists {
		if shader, hasProg := r.customShaders[id]; hasProg {
			gl.DeleteProgram(shader.program)
			delete(r.customShaders, id)
			if r.currentProgram == shader {
				r.currentProgram = nil
			}
		}
		delete(r.customShaderMap, shaderName)
	}
}

func (r *Renderer_WebGL2) SetSpritePipeline(shaderName string) {
	targetShader := r.spriteShader
	if shaderName != "" {
		if id, ok := r.customShaderMap[shaderName]; ok {
			if shader, ok := r.customShaders[id]; ok {
				targetShader = shader
			}
		}
	}

	if r.program != targetShader.program {
		r.currentProgram = targetShader
		r.ChangeProgram(targetShader.program)
		r.bindVertexArray(r.spriteVAO)
	}
}

// Uploads current param values to the shader's "uniform float param[16]"
func (r *Renderer_WebGL2) SetCustomUniforms(params [16]float32) {
	if r.currentProgram == nil {
		return
	}
	if loc, ok := r.currentProgram.uniforms["param"]; ok && loc >= 0 {
		gl.Uniform1fv(loc, params[:])
	}
}

func (r *Renderer_WebGL2) NeedsGrabPass() bool {
	if r.currentProgram != nil {
		return r.currentProgram.needsGrabPass
	}
	return false
}

func (r *Renderer_WebGL2) ResolveBackBuffer() Texture {
	r.bindTextureToUnitForced(0, gl.TEXTURE_2D, r.grabTexture, 0)

	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, r.fbo)
	gl.ReadBuffer(gl.COLOR_ATTACHMENT0)

	gl.CopyTexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, 0, 0, r.grabTexture.width, r.grabTexture.height)

	gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo)
	return r.grabTexture
}

func (r *Renderer_WebGL2) bindBuffer(target uint32, buffer uint32) {
	// Already bound. Skip
	if r.boundBuffers[target] == buffer {
		return
	}
	// Bind and update cache
	gl.BindBuffer(target, buffer)
	r.boundBuffers[target] = buffer
}

func (r *Renderer_WebGL2) bindVertexArray(vao uint32) {
	if r.currentVAO == vao {
		return
	}
	gl.BindVertexArray(vao)
	r.currentVAO = vao
	// The element buffer binding belongs to the vertex array, so forget what the cache thought
	if r.boundBuffers != nil {
		delete(r.boundBuffers, gl.ELEMENT_ARRAY_BUFFER)
	}
}
