package main

import "C"

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/glfw/v3.1/glfw"
	colorful "github.com/lucasb-eyer/go-colorful"
)

// http://antongerdelan.net/opengl/shaders.html
const (
	width  = 800
	height = 500

	vertexShaderSource = `
    #version 410
    in vec3 vp;
    void main() {
        gl_Position = vec4(vp, 1.0);
    }
` + "\x00"

	fragmentShaderSource = `
    #version 410
    uniform vec4 inputColour;
	out vec4 fragColour;

	void main() {
	  fragColour = inputColour;
	}
    
` + "\x00" // null termmination

	rows = 5
	cols = 12
)

var fragmentShaderName = []byte("inputColour" + "\x00")

// to be filled in when it runs
var vertexShader, fragmentShader uint32

func setShader(v, f uint32) {
	vertexShader = v
	fragmentShader = v
}

var rect = []float32{
	-0.75, 0.1, 0,
	-0.75, -0.1, 0,
	0.75, -0.1, 0,

	-0.75, 0.1, 0,
	0.75, 0.1, 0,
	0.75, -0.1, 0,
}

var wvibgyor = []colorful.Color{
	colorful.Hsl(360, 0.5, 0.5-0.03*float64(rows)), // pinkred
	colorful.Hsl(330, 0.5, 0.5-0.03*float64(rows)), // pink
	colorful.Hsl(300, 0.5, 0.5-0.03*float64(rows)), // magenta
	colorful.Hsl(270, 0.5, 0.5-0.03*float64(rows)), // purple
	colorful.Hsl(240, 0.5, 0.5-0.03*float64(rows)), // blue
	colorful.Hsl(180, 0.5, 0.5-0.03*float64(rows)), // cyan
	colorful.Hsl(150, 0.5, 0.5-0.03*float64(rows)), // some sort of tealish green
	colorful.Hsl(120, 0.5, 0.5-0.03*float64(rows)), // green
	colorful.Hsl(90, 0.5, 0.5-0.03*float64(rows)),  // some sort of greenish yellow
	colorful.Hsl(60, 0.5, 0.5-0.03*float64(rows)),  // yellow
	colorful.Hsl(30, 0.5, 0.5-0.03*float64(rows)),  // orange
	colorful.Hsl(0, 0.5, 0.5-0.03*float64(rows)),   // red

}

var cellLookup = map[byte]struct{ x, y int }{}
var cells [][]*cell
var globalLock sync.Mutex

type cell struct {
	drawable uint32

	colorful.Color

	on, off colorful.Color

	x int
	y int
}

func (c *cell) draw(program uint32) {
	loc := gl.GetUniformLocation(program, &fragmentShaderName[0])

	gl.ProgramUniform4f(program, loc, float32(c.R), float32(c.G), float32(c.B), 1)
	// gl.ProgramUniform4f(program, loc, 0, 1, 0, float32(c.A)/255)

	gl.BindVertexArray(c.drawable)
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(rect)/3))
}

func updateCells(msg message) {
	globalLock.Lock()
	switch {
	case msg.key == 255:
		// do nothin
	case msg.velocity == 0:
		coord := cellLookup[msg.key]
		cells[coord.y][coord.x].Color = cells[coord.y][coord.x].off
	default:

		coord := cellLookup[msg.key]
		log.Printf("coord %v", coord)
		cells[coord.y][coord.x].Color = cells[coord.y][coord.x].on

	}
	globalLock.Unlock()
}

// func mainGL(in <-chan message, out chan bool) {
func mainGL() {
	runtime.LockOSThread()

	window := initGlfw()
	defer glfw.Terminate()

	program := initOpenGL()
	cells = makeCells()
	for !window.ShouldClose() {
		globalLock.Lock()
		draw(cells, window, program)
		globalLock.Unlock()
	}
	// for {
	// 	select {
	// 	case msg := <-in:
	// 		if !window.ShouldClose() {
	// 			// on or off
	// 			switch {
	// 			case msg.key == 255:
	// 				for i, rows := range cells {
	// 					for j := range rows {
	// 						cells[i][j].A = 64
	// 					}
	// 				}
	// 			default:
	// 				coord := cellLookup[msg.key]
	// 				cells[coord.y][coord.x].A = 255
	// 			}

	// 			draw(cells, window, program)
	// 		}
	// 	default:
	// 		out <- window.ShouldClose()
	// 	}
	// }
}

// initGlfw initializes glfw and returns a Window to use.
func initGlfw() *glfw.Window {
	if err := glfw.Init(); err != nil {
		panic(err)
	}

	glfw.WindowHint(glfw.Resizable, glfw.True)
	glfw.WindowHint(glfw.ContextVersionMajor, 4) // OR 2
	glfw.WindowHint(glfw.ContextVersionMinor, 1)
	glfw.WindowHint(glfw.OpenGLProfile, glfw.OpenGLCoreProfile)
	glfw.WindowHint(glfw.OpenGLForwardCompatible, glfw.True)

	window, err := glfw.CreateWindow(width, height, "Hello GopherCon Singapore", nil, nil)
	if err != nil {
		log.Printf("%p", window)
		panic(err)
	}
	window.MakeContextCurrent()

	return window
}

// initOpenGL initializes OpenGL and returns an intiialized program.
func initOpenGL() uint32 {
	if err := gl.Init(); err != nil {
		panic(err)
	}
	version := gl.GoStr(gl.GetString(gl.VERSION))
	log.Println("OpenGL version", version)
	var err error

	if vertexShader, err = compileShader(vertexShaderSource, gl.VERTEX_SHADER); err != nil {
		panic(err)
	}
	if fragmentShader, err = compileShader(fragmentShaderSource, gl.FRAGMENT_SHADER); err != nil {
		panic(err)
	}

	prog := gl.CreateProgram()
	gl.AttachShader(prog, vertexShader)
	gl.AttachShader(prog, fragmentShader)
	gl.LinkProgram(prog)
	return prog
}

func draw(cells [][]*cell, window *glfw.Window, program uint32) {
	gl.ClearColor(0, 0, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.UseProgram(program)

	for x := range cells {
		for _, c := range cells[x] {
			c.draw(program)
		}
	}

	glfw.PollEvents()
	window.SwapBuffers()
}

// makeVao initializes and returns a vertex array from the points provided.
func makeVao(points []float32) uint32 {
	var vbo uint32
	gl.GenBuffers(1, &vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, 4*len(points), gl.Ptr(points), gl.STATIC_DRAW)

	var vao uint32
	gl.GenVertexArrays(1, &vao)
	gl.BindVertexArray(vao)
	gl.EnableVertexAttribArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.VertexAttribPointer(0, 3, gl.FLOAT, false, 0, nil)

	return vao
}

func compileShader(source string, shaderType uint32) (uint32, error) {
	shader := gl.CreateShader(shaderType)

	csources, free := gl.Strs(source)
	gl.ShaderSource(shader, 1, csources, nil)
	free()
	gl.CompileShader(shader)

	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &logLength)

		log := strings.Repeat("\x00", int(logLength+1))
		gl.GetShaderInfoLog(shader, logLength, nil, gl.Str(log))

		return 0, fmt.Errorf("failed to compile %v: %v", source, log)
	}

	return shader, nil
}

func makeCells() [][]*cell {
	cells := make([][]*cell, 0, rows)
	for y := 0; y < rows; y++ {
		cs := make([]*cell, 0, cols)
		for x := 0; x < cols; x++ {
			c := newCell(x, y)
			h, s, l := wvibgyor[x].Hsl()
			l += 0.03 * float64(y)
			c.Color = colorful.Hsl(h, s, l)
			c.off = colorful.Hsl(h, s, l)
			c.on = colorful.Hsl(h, 1, l)

			cs = append(cs, c)
		}
		cells = append(cells, cs)
	}

	return cells
}

func newCell(x, y int) *cell {
	points := make([]float32, len(rect), len(rect))
	copy(points, rect)

	for i := 0; i < len(points); i++ {
		var position, size float32
		var mul float32 = 2
		var offset float32 = 1
		var isX, isY bool
		switch i % 3 {
		case 0:
			size = 1.0 / float32(cols)
			position = float32(x) * size
			isX = true
			isY = false
		case 1:
			size = 1.0 / float32(rows)
			position = float32(y) * size
			isX = false
			isY = true
			mul = 0.9
		default:
			continue
		}
		_ = isX

		if points[i] < 0 {
			if isY {
				offset = 0.1
				mul = 0.6
			}
			points[i] = (position * mul) - offset
		} else {
			if isY {
				offset = 0.8
				mul = 1
			}
			points[i] = ((position + size) * mul) - offset
		}
	}

	return &cell{
		drawable: makeVao(points),

		x: x,
		y: y,
	}
}
