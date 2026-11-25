package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Swarm-run outcome sentinels; the whitelist lives in the domain
// layer, not DB CHECKs (see orm_types.go).
const (
	// SwarmRunOutcomePass marks a run every gate accepted.
	SwarmRunOutcomePass = "pass"
	// SwarmRunOutcomeFail marks a run some gate or admission refused.
	SwarmRunOutcomeFail = "fail"
)

// Swarm-run record sentinels; validation failures write nothing.
var (
	// ErrSwarmRunNameMissing rejects a run that names no swarm.
	ErrSwarmRunNameMissing = errors.New("store: swarm run name must not be empty")
	// ErrSwarmRunManifestMissing rejects a run that pins no digest.
	ErrSwarmRunManifestMissing = errors.New("store: swarm run manifest digest must not be empty")
	// ErrSwarmRunOutcomeInvalid rejects an outcome outside pass/fail.
	ErrSwarmRunOutcomeInvalid = errors.New("store: swarm run outcome must be pass or fail")
	// ErrSwarmRunPacketMissing rejects a decision row without a packet id.
	ErrSwarmRunPacketMissing = errors.New("store: swarm run packet id must not be empty")
	// ErrSwarmRunChildMissing rejects a child row without a session id. The packet
	// id may be empty: it marks a child the engine could not attribute to a packet.
	ErrSwarmRunChildMissing = errors.New("store: swarm run child session id must not be empty")
)

// RecordSwarmRun persists one configured-swarm run, its packet
// admission rows and the child sessions it names in a single
// transaction: the store assigns the run
// id and record time; validation failures write nothing and return
// their sentinel.
func (s *Store) RecordSwarmRun(ctx context.Context, run SwarmRun) (SwarmRun, error) {
	if err := s.checkOpen(); err != nil {
		return SwarmRun{}, err
	}
	if err := validateSwarmRun(run); err != nil {
		return SwarmRun{}, err
	}
	run.CreatedAt = time.Now().UTC()
	id, merr := uuid.NewV7()
	if merr != nil {
		return SwarmRun{}, fmt.Errorf("mint swarm run id: %w", merr)
	}
	run.ID = id.String()
	err := s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&swarmRunRow{
			ID: run.ID, Name: run.Name, ManifestSHA256: run.ManifestSHA256,
			Outcome: run.Outcome, CreatedAtUTC: iso(run.CreatedAt),
		}).Error; err != nil {
			return fmt.Errorf("create swarm run: %w", err)
		}
		for _, p := range run.Packets {
			pid, perr := uuid.NewV7()
			if perr != nil {
				return fmt.Errorf("mint swarm run packet id: %w", perr)
			}
			if err := tx.Create(&swarmRunPacketRow{
				ID: pid.String(), RunID: run.ID, PacketID: p.PacketID,
				Admitted: swarmRunAdmitted(p.Admitted), Reason: p.Reason,
				CreatedAtUTC: iso(run.CreatedAt),
			}).Error; err != nil {
				return fmt.Errorf("create swarm run packet %s: %w", p.PacketID, err)
			}
		}
		return createSwarmRunChildren(tx, run)
	})
	if err != nil {
		return SwarmRun{}, err
	}
	return run, nil
}

// SwarmRunsFor loads the recorded runs of the named swarm, oldest
// first, each with its packet rows in admission order and the child
// sessions it named. An unknown
// name is not an error; it yields no runs.
func (s *Store) SwarmRunsFor(ctx context.Context, name string) ([]SwarmRun, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	var runRows []swarmRunRow
	err := s.gdb.WithContext(ctx).
		Where(map[string]any{"name": name}).
		Order("created_at_utc, id").
		Find(&runRows).Error
	if err != nil {
		return nil, fmt.Errorf("load swarm runs for %s: %w", name, err)
	}
	runs := make([]SwarmRun, 0, len(runRows))
	for _, row := range runRows {
		run := SwarmRun{
			ID: row.ID, Name: row.Name, ManifestSHA256: row.ManifestSHA256,
			Outcome: row.Outcome,
		}
		created, cerr := parseStored(row.ID, "created_at_utc", row.CreatedAtUTC)
		if cerr != nil {
			return nil, fmt.Errorf("load swarm runs for %s: %w", name, cerr)
		}
		run.CreatedAt = created
		var packetRows []swarmRunPacketRow
		perr := s.gdb.WithContext(ctx).
			Where(map[string]any{"run_id": row.ID}).
			Order("rowid"). // rowid order preserves insertion (admission) order
			Find(&packetRows).Error
		if perr != nil {
			return nil, fmt.Errorf("load packets of swarm run %s: %w", row.ID, perr)
		}
		run.Packets = make([]SwarmRunPacket, 0, len(packetRows))
		for _, prow := range packetRows {
			run.Packets = append(run.Packets, SwarmRunPacket{
				PacketID: prow.PacketID, Admitted: prow.Admitted == 1, Reason: prow.Reason,
			})
		}
		children, cerr := s.swarmRunChildren(ctx, row.ID)
		if cerr != nil {
			return nil, fmt.Errorf("load swarm runs for %s: %w", name, cerr)
		}
		run.Children = children
		runs = append(runs, run)
	}
	sortChronological(runs, func(r SwarmRun) (time.Time, string) { return r.CreatedAt, r.ID })
	return runs, nil
}

// validateSwarmRun enforces the record invariants before any write.
func validateSwarmRun(run SwarmRun) error {
	if run.Name == "" {
		return ErrSwarmRunNameMissing
	}
	if run.ManifestSHA256 == "" {
		return ErrSwarmRunManifestMissing
	}
	if run.Outcome != SwarmRunOutcomePass && run.Outcome != SwarmRunOutcomeFail {
		return ErrSwarmRunOutcomeInvalid
	}
	for _, p := range run.Packets {
		if p.PacketID == "" {
			return ErrSwarmRunPacketMissing
		}
	}
	for _, c := range run.Children {
		if c.SessionID == "" {
			return ErrSwarmRunChildMissing
		}
	}
	return nil
}

// swarmRunAdmitted maps the admission decision onto the 1/0 form.
func swarmRunAdmitted(admitted bool) int64 {
	if admitted {
		return 1
	}
	return 0
}

// createSwarmRunChildren writes the run's child rows inside its
// transaction, so a run and the children it names persist together.
func createSwarmRunChildren(tx *gorm.DB, run SwarmRun) error {
	for _, c := range run.Children {
		cid, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("mint swarm run child id: %w", err)
		}
		if err := tx.Create(&swarmRunChildRow{
			ID: cid.String(), RunID: run.ID, PacketID: c.PacketID,
			SessionID: c.SessionID, CreatedAtUTC: iso(run.CreatedAt),
		}).Error; err != nil {
			return fmt.Errorf("create swarm run child %s: %w", c.PacketID, err)
		}
	}
	return nil
}

// swarmRunChildren loads one run's child rows in recorded order; a run
// that named none yields nil.
func (s *Store) swarmRunChildren(ctx context.Context, runID string) ([]SwarmRunChild, error) {
	var rows []swarmRunChildRow
	err := s.gdb.WithContext(ctx).
		Where(map[string]any{"run_id": runID}).
		Order("rowid"). // rowid order preserves insertion order
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load children of swarm run %s: %w", runID, err)
	}
	var out []SwarmRunChild
	for _, r := range rows {
		out = append(out, SwarmRunChild{PacketID: r.PacketID, SessionID: r.SessionID})
	}
	return out, nil
}
