package swarm

import "fmt"

// NewSteeringQueue creates an empty steering log.
func NewSteeringQueue() *SteeringQueue { return &SteeringQueue{} }

// Send records msg in the queue, preserving send order. It fails
// closed when From, To or Body is empty.
func (q *SteeringQueue) Send(msg SteeringMessage) error {
	switch {
	case msg.From == "":
		return fmt.Errorf("swarm: steering sender must not be empty")
	case msg.To == "":
		return fmt.Errorf("swarm: steering target must not be empty")
	case msg.Body == "":
		return fmt.Errorf("swarm: steering body must not be empty")
	}
	q.msgs = append(q.msgs, msg)
	return nil
}

// Drain returns the steering messages addressed to child in send
// order and removes them from the queue; subsequent drains see only
// newer messages.
func (q *SteeringQueue) Drain(child string) []SteeringMessage {
	out := make([]SteeringMessage, 0, len(q.msgs))
	kept := q.msgs[:0]
	for _, m := range q.msgs {
		if m.To == child {
			out = append(out, m)
		} else {
			kept = append(kept, m)
		}
	}
	q.msgs = kept
	return out
}

// Queued reports how many undrained messages target child.
func (q *SteeringQueue) Queued(child string) int {
	n := 0
	for _, m := range q.msgs {
		if m.To == child {
			n++
		}
	}
	return n
}
