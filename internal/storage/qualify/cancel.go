package qualify

import "context"

// CancelCommit performs the mid-turn cancellation: a checkpoint event plus the
// terminal session.cancelled event and status=cancelled commit in one
// transaction via the session's serialized writer. After success the writer
// rejects all further effects with ErrWriterClosed.
func CancelCommit(w *Writer, checkpoint map[string]any) error {
	evts := []Event{
		{Actor: "engine", Type: "cancel.checkpoint", Payload: checkpoint},
		{Actor: "engine", Type: "session.cancelled", Payload: map[string]any{"reason": "user_cancel"}},
	}
	_, err := w.AppendWithStatus(context.Background(), "cancelled", evts...)
	return err
}
