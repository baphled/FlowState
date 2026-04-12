package engine

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/streaming"
)

// FormatCompletionReminder builds a system-reminder message for a single
// completed background task. The message instructs the planner to retrieve
// the result via the background_output tool.
//
// Expected:
//   - taskID, agent, and duration are non-empty strings describing the task.
//
// Returns:
//   - A formatted system-reminder string.
//
// Side effects:
//   - None.
func FormatCompletionReminder(taskID, agent, duration string) string {
	return fmt.Sprintf(
		"<system-reminder>\n[BACKGROUND TASK COMPLETE]\n"+
			"Task %s (%s) completed in %s.\n"+
			"Use background_output(task_id=%q) to retrieve the result.\n"+
			"</system-reminder>",
		taskID, agent, duration, taskID,
	)
}

// FormatCompletionReminders builds a single system-reminder message for
// multiple completed background tasks. Each notification is formatted
// individually and joined with newlines.
//
// Expected:
//   - notifications contains one or more completed task notifications.
//
// Returns:
//   - A formatted system-reminder string covering all notifications.
//   - An empty string if notifications is empty.
//
// Side effects:
//   - None.
func FormatCompletionReminders(notifications []streaming.CompletionNotificationEvent) string {
	if len(notifications) == 0 {
		return ""
	}

	parts := make([]string, 0, len(notifications))
	for _, n := range notifications {
		parts = append(parts, FormatCompletionReminder(n.TaskID, n.Agent, n.Duration.String()))
	}

	return strings.Join(parts, "\n")
}
