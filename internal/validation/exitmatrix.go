package validation

// ExitCode identifies a stable CLI exit code from the CLI contract (VA-C1).
type ExitCode int

// Stable exit codes from specs/CLI-AND-API-CONTRACT.md. The behaviour lives
// in cmd/flowstate exitCodeFor; this table only validates observed pairs.
const (
	ExitCompleted      ExitCode = 0
	ExitUnknownSession ExitCode = 1
	ExitUsage          ExitCode = 2
	ExitProvider       ExitCode = 3
	ExitConfigInvalid  ExitCode = 4
	ExitBudgetPause    ExitCode = 5
	ExitTerminalRefuse ExitCode = 6
	ExitReplayMismatch ExitCode = 7
	ExitReceiptInvalid ExitCode = 8
	ExitCancelled      ExitCode = 130
)

// ExitExpectation is one row of the VA-C1 command/error/exit matrix.
type ExitExpectation struct {
	Command string
	Error   string
	Exit    ExitCode
}

// ExitMatrix is the declarative VA-C1 matrix from the CLI contract.
var ExitMatrix = []ExitExpectation{
	{Command: "session new", Error: "ok", Exit: ExitCompleted},
	{Command: "session new", Error: "usage", Exit: ExitUsage},
	{Command: "session new", Error: "config_invalid", Exit: ExitConfigInvalid},
	{Command: "session run", Error: "completed", Exit: ExitCompleted},
	{Command: "session run", Error: "provider_error", Exit: ExitProvider},
	{Command: "session run", Error: "budget_exceeded", Exit: ExitBudgetPause},
	{Command: "session run", Error: "cancelled", Exit: ExitCancelled},
	{Command: "session cancel", Error: "ok", Exit: ExitCompleted},
	{Command: "session cancel", Error: "session_terminal", Exit: ExitTerminalRefuse},
	{Command: "session run", Error: "session_terminal", Exit: ExitTerminalRefuse},
	{Command: "session events", Error: "ok", Exit: ExitCompleted},
	{Command: "session events", Error: "session_not_found", Exit: ExitUnknownSession},
	{Command: "replay", Error: "ok", Exit: ExitCompleted},
	{Command: "replay", Error: "replay_mismatch", Exit: ExitReplayMismatch},
	{Command: "receipt validate", Error: "ok", Exit: ExitCompleted},
	{Command: "receipt validate", Error: "receipt_invalid", Exit: ExitReceiptInvalid},
}

// ValidateExit checks an observed exit against the expected exit: an
// expected cancel/error exit is VALID when the actual exit equals it; an
// unexpected exit is the failure (VA-C1 oracle rule).
func ValidateExit(expected ExitCode, actual int) []string {
	if ExitCode(actual) != expected {
		return []string{"unexpected exit: expected " + itoa(int(expected)) + " got " + itoa(actual)}
	}
	return nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
