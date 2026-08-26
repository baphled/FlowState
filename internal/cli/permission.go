package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// NewCLIPermissionHandler creates a PermissionHandler that prompts via stdin/stdout.
//
// Expected:
//   - in is a readable input source (typically os.Stdin).
//   - out is a writable output destination (typically os.Stdout).
//
// Returns:
//   - A PermissionHandler that prints tool details and reads y/n from the user.
//
// Side effects:
//   - None (the returned handler performs I/O when invoked).
func NewCLIPermissionHandler(in io.Reader, out io.Writer) tool.PermissionHandler {
	return func(req tool.PermissionRequest) (bool, error) {
		_, err := fmt.Fprintf(out, "Tool: %s\nArguments: %v\nAllow? (y/n): ", req.ToolName, req.Arguments)
		if err != nil {
			return false, fmt.Errorf("writing permission prompt: %w", err)
		}

		scanner := bufio.NewScanner(in)
		if !scanner.Scan() {
			return false, nil
		}

		return strings.TrimSpace(scanner.Text()) == "y", nil
	}
}
