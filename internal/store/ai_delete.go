package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
)

var ErrAIDeleteUnconfirmed = errors.New("AI key disable is not confirmed or operations remain unresolved")

func deleteSafeAIBinding(ctx context.Context, tx bun.Tx, id string) error {
	var b model.AIBinding
	err := tx.NewSelect().Model(&b).Where("license_id=?", id).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	pending, err := tx.NewSelect().Model((*model.AIJob)(nil)).Where("license_id=? AND state <> 'done'", id).Exists(ctx)
	if err != nil {
		return err
	}
	if pending || b.CandidateID != 0 || b.State != "synced" || b.RemoteID > 0 && b.RemoteView["status"] != "inactive" {
		return ErrAIDeleteUnconfirmed
	}
	if _, err := tx.NewDelete().Model((*model.AIJob)(nil)).Where("license_id=?", id).Exec(ctx); err != nil {
		return err
	}
	_, err = tx.NewDelete().Model(&b).WherePK().Exec(ctx)
	return err
}
