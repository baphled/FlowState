package swarm

import (
	"fmt"
	"os"
	"strings"
)

// RunOrder returns the manifest's packets in a stable order where
// prerequisites come first (Kahn's algorithm on the prerequisite
// edges); ambiguity resolves by manifest order.
func RunOrder(m Manifest) []Packet {
	byID := map[string]Packet{}
	deps := map[string]int{}
	children := map[string][]string{}
	for _, p := range m.Packets {
		byID[p.ID] = p
		deps[p.ID] = len(p.Prerequisites)
		for _, pre := range p.Prerequisites {
			children[pre] = append(children[pre], p.ID)
		}
	}
	var order []Packet
	var ready []string
	for _, p := range m.Packets { // manifest order seeds the ready set
		if deps[p.ID] == 0 {
			ready = append(ready, p.ID)
		}
	}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, byID[id])
		for _, c := range children[id] {
			deps[c]--
			if deps[c] == 0 {
				ready = append(ready, c)
			}
		}
	}
	return order
}

// sourceBody fetches the declared source body of one packet: the
// local file whose path equals the packet Task when Task names an
// existing file, else the task text itself. Operators pin real
// bodies by pointing Task at a source file.
func sourceBody(p Packet) []byte {
	if !strings.Contains(p.Task, "\n") {
		if raw, err := os.ReadFile(p.Task); err == nil {
			return raw
		}
	}
	return []byte(p.Task)
}

// Run drives one configured swarm: admission of every packet in
// dependency order, then evaluation of every gate. Admission
// failures are reported to ev and do not abort the run; the gates
// still run and acceptance fails closed. The receipts sink receives
// gate outcomes for durable proof.
func Run(m Manifest, admitter RunAdmitter, gates *GateRunner, ev RunEvents, receipts RunReceipts) (RunResult, error) {
	if err := ValidateManifest(m); err != nil {
		return RunResult{}, fmt.Errorf("swarm run: %w", err)
	}
	res := RunResult{Manifest: m}
	for _, p := range RunOrder(m) {
		if err := admitter.Admit(p, sourceBody(p)); err != nil {
			if ev != nil {
				ev.PacketRejected(p.ID, err.Error())
			}
			continue
		}
		res.Admitted = append(res.Admitted, p.ID)
		if ev != nil {
			ev.PacketAdmitted(p)
		}
	}
	res.Gates = gates.Evaluate(m)
	if receipts != nil {
		_ = receipts.RecordGates(m.Name, res.Gates)
	}
	return res, nil
}
