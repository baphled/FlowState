package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// spinnerFrames are the Braille glyphs animated in-place while the CLI is
// waiting on a long-running operation. Mirrors the frame set used by the
// TUI's status_indicator widget so the two surfaces feel consistent.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerInterval is the per-frame redraw interval. 100ms is fast enough
// to read as motion and slow enough that the terminal output stays calm.
const spinnerInterval = 100 * time.Millisecond

// Spinner draws a single in-place spinner line on a TTY writer while a
// long-running operation runs in the foreground. On non-TTY writers
// (pipes, log files, CI capture) Start prints msg once and Stop prints
// finalMsg — preserving useful output without ANSI carriage returns
// peppering log files.
//
// Lifecycle:
//
//	s := NewSpinner(out, "Waiting for X...")
//	s.Start()
//	defer s.Stop("Done.")
//	doSlowThing()
//
// Start is safe to call once; Stop is idempotent.
type Spinner struct {
	out      io.Writer
	msg      string
	isTTY    bool
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// NewSpinner returns a configured Spinner bound to out. It does not
// start the animation — call Start to begin.
//
// Expected:
//   - out is a non-nil writer; *os.File writers are TTY-checked.
//   - msg is the label rendered next to the spinner glyph.
//
// Returns:
//   - A Spinner ready to Start. The TTY check happens here so callers
//     can branch on s.IsTTY() if needed.
//
// Side effects:
//   - Calls Stat on out when out is *os.File (cheap; no I/O).
func NewSpinner(out io.Writer, msg string) *Spinner {
	return &Spinner{
		out:   out,
		msg:   msg,
		isTTY: writerIsTerminal(out),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// IsTTY reports whether the spinner detected a terminal writer at
// construction time. False means Start prints once and skips animation.
//
// Returns:
//   - true when the writer is a character device.
//
// Side effects:
//   - None.
func (s *Spinner) IsTTY() bool {
	return s.isTTY
}

// Start begins the spinner animation on a background goroutine. On
// non-TTY writers it prints msg + newline and returns immediately.
//
// Expected:
//   - The Spinner has not been Started before. Calling Start twice
//     leaks the first goroutine.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Spawns one goroutine on TTY writers.
//   - Writes msg + newline on non-TTY writers.
func (s *Spinner) Start() {
	if !s.isTTY {
		fmt.Fprintln(s.out, s.msg)
		close(s.done)
		return
	}
	go s.loop()
}

// Stop halts the spinner, clears the in-place line on a TTY, and writes
// finalMsg + newline. Idempotent — safe to defer alongside an
// inline Stop call.
//
// Expected:
//   - finalMsg may be empty (no trailing line is written in that case).
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Closes the stop channel.
//   - Waits for the animation goroutine to finish.
//   - Writes finalMsg when non-empty.
func (s *Spinner) Stop(finalMsg string) {
	s.stopOnce.Do(func() {
		close(s.stop)
	})
	<-s.done
	if finalMsg != "" {
		fmt.Fprintln(s.out, finalMsg)
	}
}

// loop drives the spinner animation. Runs on its own goroutine until
// the stop channel closes; on exit it clears the in-place line so the
// caller's next Fprintln lands on a clean row.
//
// Expected:
//   - Called only from Start; receiver is a TTY writer.
//
// Returns:
//   - Nothing (closes done on exit).
//
// Side effects:
//   - Writes carriage-return-prefixed frames to s.out every
//     spinnerInterval.
func (s *Spinner) loop() {
	defer close(s.done)
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	frame := 0
	for {
		select {
		case <-s.stop:
			fmt.Fprint(s.out, "\r\033[K")
			return
		case <-ticker.C:
			fmt.Fprintf(s.out, "\r%s %s", spinnerFrames[frame%len(spinnerFrames)], s.msg)
			frame++
		}
	}
}

// writerIsTerminal reports whether out is an *os.File backed by a
// character device. Pipes, regular files, and bytes.Buffer all return
// false — the spinner falls back to plain output for them.
//
// Expected:
//   - out is any io.Writer; non-File writers always return false.
//
// Returns:
//   - true when out is a TTY-like file descriptor.
//
// Side effects:
//   - Calls Stat on the underlying *os.File (cheap).
func writerIsTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
