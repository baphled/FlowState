package store

import "time"

// SwarmRun is one durable configured-swarm run: the manifest
// identity that ran, the aggregated gate outcome and the per-packet
// admission decisions in admission order; the store assigns ID and
// CreatedAt at record time.
type SwarmRun struct {
	ID             string
	Name           string
	ManifestSHA256 string
	Outcome        string
	CreatedAt      time.Time
	Packets        []SwarmRunPacket
}

// SwarmRunPacket is one packet admission decision inside a swarm
// run; Reason carries the rejection cause and stays empty for
// admitted packets.
type SwarmRunPacket struct {
	PacketID string
	Admitted bool
	Reason   string
}
