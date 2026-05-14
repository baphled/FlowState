package carbonyl

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

const (
	defaultFPS              = 15
	defaultZoom             = 100
	watchdogInterval        = 5 * time.Second
	startupGracePeriod      = 3 * time.Second
	gracefulShutdownTimeout = 5 * time.Second
)

type Config struct {
	BinaryPath string
	URL        string
	FPS        int
	Zoom       int
	Width      int
	Height     int
}

func DefaultConfig() Config {
	return Config{
		FPS:   defaultFPS,
		Zoom:  defaultZoom,
		Width: 0,
		Height: 0,
	}
}

func (c Config) WithBinary(path string) Config {
	c.BinaryPath = path
	return c
}

func (c Config) WithURL(url string) Config {
	c.URL = url
	return c
}

func (c Config) WithFPS(fps int) Config {
	c.FPS = fps
	return c
}

func (c Config) WithZoom(zoom int) Config {
	c.Zoom = zoom
	return c
}

func (c Config) WithSize(width, height int) Config {
	c.Width = width
	c.Height = height
	return c
}

func (c Config) ApplyDefaults() Config {
	if c.FPS <= 0 {
		c.FPS = defaultFPS
	}
	if c.Zoom <= 0 {
		c.Zoom = defaultZoom
	}
	return c
}

func (c Config) TerminalSize() (int, int) {
	if c.Width > 0 && c.Height > 0 {
		return c.Width, c.Height
	}

	cols, rows := autoDetectTerminalSize()
	width := c.Width
	height := c.Height

	if width <= 0 {
		width = cols
	}
	if height <= 0 {
		height = rows
	}

	return width, height
}

func autoDetectTerminalSize() (int, int) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 80, 24
	}

	var rows, cols int
	if _, err := fmt.Sscanf(string(out), "%d %d", &rows, &cols); err != nil {
		return 80, 24
	}

	return cols, rows
}
