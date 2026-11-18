package swarm

// Steering messages for the M2 runtime: a parent (or another agent)
// directs an in-flight child session without terminating it. Steering
// is append-only: each message is recorded in order and projected
// back to whoever observes the child.

// SteeringMessage is one mid-run directive addressed to a child
// session. From and To are session ids; Body carries the directive.
type SteeringMessage struct {
	// From is the sending session's id.
	From string
	// To is the target child session's id.
	To string
	// Body is the directive content.
	Body string
}

// SteeringQueue is the append-only, in-memory steering log for child
// sessions. Messages are delivered in send order; the queue fails
// closed on empty ids and empty bodies.
type SteeringQueue struct {
	msgs []SteeringMessage
}
