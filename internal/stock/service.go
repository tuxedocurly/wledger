package stock

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
)

type Service interface {
	AssignStock(ctx context.Context, req AssignStockRequest) error
	MoveStock(ctx context.Context, req MoveStockRequest) error
	RemoveStock(ctx context.Context, req RemoveStockRequest) error
	AdjustStock(ctx context.Context, assignmentID int64, delta int) error
}

type AssignStockRequest struct {
	PartID   int64
	BinID    int64
	Quantity int
}

type MoveStockRequest struct {
	PartID       int64
	AssignmentID int64
	TargetBinID  int64
}

type RemoveStockRequest struct {
	PartID       int64
	AssignmentID int64
}

type service struct {
	store  db.Store
	logger *slog.Logger
}

func NewService(store db.Store, logger *slog.Logger) Service {
	return &service{
		store:  store,
		logger: logger,
	}
}

func (s *service) AssignStock(ctx context.Context, req AssignStockRequest) error {
	s.logger.Debug("assigning stock", "part_id", req.PartID, "bin_id", req.BinID, "qty", req.Quantity)
	nullBinID := sql.NullInt64{Int64: int64(req.BinID), Valid: true}

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		existingID, err := q.GetAssignmentID(ctx, db.GetAssignmentIDParams{
			PartID: req.PartID,
			BinID:  nullBinID,
		})

		if err == nil {
			// Assignment exists, fetch current quantity
			s.logger.Debug("updating existing assignment", "id", existingID)
			existing, err := q.GetAssignment(ctx, existingID)
			if err != nil {
				s.logger.Error("failed to fetch existing assignment for stock addition", "err", err, "part_id", req.PartID, "bin_id", req.BinID)
				return fmt.Errorf("failed to fetch existing assignment: %w", err)
			}

			newQty := existing.Quantity + int64(req.Quantity)
			err = q.UpdatePartAssignmentQuantityByID(ctx, db.UpdatePartAssignmentQuantityByIDParams{
				Quantity: newQty,
				ID:       existingID,
			})
			if err != nil {
				return err
			}

			audit.Log(ctx, q, "STOCK_ADD", "PART", req.PartID, "Added stock to existing bin",
				map[string]any{"quantity": existing.Quantity},
				map[string]any{"quantity": newQty})

		} else {
			// New assignment
			s.logger.Debug("creating new assignment", "part_id", req.PartID, "bin_id", req.BinID)
			err = q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
				PartID:   req.PartID,
				BinID:    nullBinID,
				Quantity: int64(req.Quantity),
			})
			if err != nil {
				return err
			}

			audit.Log(ctx, q, "STOCK_ADD", "PART", req.PartID, "Added stock to new bin", nil,
				map[string]any{"bin_id": req.BinID, "quantity": req.Quantity})
		}
		return nil
	})
}

func (s *service) AdjustStock(ctx context.Context, assignmentID int64, delta int) error {
	s.logger.Debug("adjusting stock", "id", assignmentID, "delta", delta)

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		assignment, err := q.GetAssignment(ctx, assignmentID)
		if err != nil {
			s.logger.Error("assignment not found for adjustment", "err", err, "assignment_id", assignmentID)
			return fmt.Errorf("assignment not found: %w", err)
		}

		newQty := assignment.Quantity + int64(delta)
		if newQty < 0 {
			newQty = 0
		}

		err = q.UpdatePartAssignmentQuantityByID(ctx, db.UpdatePartAssignmentQuantityByIDParams{
			Quantity: newQty,
			ID:       assignment.ID,
		})

		if err != nil {
			return err
		}

		action := "STOCK_INC"
		if delta < 0 {
			action = "STOCK_DEC"
		}
		audit.Log(ctx, q, action, "PART", assignment.PartID, fmt.Sprintf("Adjusted stock by %d", delta),
			map[string]any{"quantity": assignment.Quantity},
			map[string]any{"quantity": newQty})

		return nil
	})
}

func (s *service) MoveStock(ctx context.Context, req MoveStockRequest) error {
	s.logger.Debug("moving stock", "part_id", req.PartID, "assignment_id", req.AssignmentID, "target_bin_id", req.TargetBinID)

	return s.store.ExecTx(ctx, func(q db.Querier) error {
		source, err := q.GetAssignment(ctx, req.AssignmentID)
		if err != nil {
			s.logger.Error("source assignment not found for stock move", "err", err, "assignment_id", req.AssignmentID)
			return fmt.Errorf("source assignment not found: %w", err)
		}

		// Check for same bin move (No-op)
		if source.BinID.Valid && source.BinID.Int64 == req.TargetBinID {
			s.logger.Debug("source and target bins are identical, ignoring move request", "bin_id", req.TargetBinID)
			return nil
		}

		s.logger.Debug("checking if target bin already has an assignment for this part", "part_id", req.PartID, "bin_id", req.TargetBinID)
		targetID, err := q.GetAssignmentID(ctx, db.GetAssignmentIDParams{
			PartID: req.PartID,
			BinID:  sql.NullInt64{Int64: req.TargetBinID, Valid: true},
		})

		if err == nil {
			s.logger.Debug("merging stock into existing target assignment", "target_id", targetID)
			target, err := q.GetAssignment(ctx, targetID)
			if err != nil {
				return fmt.Errorf("failed to fetch target for merge: %w", err)
			}

			newQty := target.Quantity + source.Quantity

			err = q.UpdatePartAssignmentQuantityByID(ctx, db.UpdatePartAssignmentQuantityByIDParams{
				Quantity: newQty,
				ID:       targetID,
			})
			if err != nil {
				return fmt.Errorf("failed to update target stock: %w", err)
			}

			err = q.DeleteAssignment(ctx, req.AssignmentID)
			if err != nil {
				return fmt.Errorf("failed to delete source stock after merge: %w", err)
			}

			audit.Log(ctx, q, "STOCK_MERGE", "PART", req.PartID, fmt.Sprintf("Merged stock into bin %d", req.TargetBinID),
				map[string]any{"bin_id": source.BinID.Int64, "quantity": source.Quantity},
				map[string]any{"bin_id": req.TargetBinID, "quantity": newQty}) // newQty is total in target
		} else {
			s.logger.Debug("reassigning assignment to new bin")
			err = q.ReassignPartAssignment(ctx, db.ReassignPartAssignmentParams{
				BinID: sql.NullInt64{Int64: req.TargetBinID, Valid: true},
				ID:    req.AssignmentID,
			})
			if err != nil {
				return fmt.Errorf("failed to move stock: %w", err)
			}

			audit.Log(ctx, q, "STOCK_MOVE", "PART", req.PartID, fmt.Sprintf("Moved stock to bin %d", req.TargetBinID),
				map[string]any{"bin_id": source.BinID.Int64},
				map[string]any{"bin_id": req.TargetBinID})
		}
		return nil
	})
}

func (s *service) RemoveStock(ctx context.Context, req RemoveStockRequest) error {
	return s.store.ExecTx(ctx, func(q db.Querier) error {
		assignment, err := q.GetAssignment(ctx, req.AssignmentID)
		if err == nil {
			audit.Log(ctx, q, "STOCK_REMOVE", "PART", req.PartID, "Removed stock",
				map[string]any{
					"bin_id":   assignment.BinID.Int64,
					"quantity": assignment.Quantity,
				}, nil)
		}

		err = q.DeleteAssignment(ctx, req.AssignmentID)
		if err != nil {
			return err
		}
		return nil
	})
}
