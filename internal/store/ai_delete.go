package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
)

var ErrAIDeleteBusy = errors.New("AI synchronization holds the license lock")
var ErrDeleteUnconfirmed = errors.New("delete outcome could not be confirmed")

// A separate pool prevents a broken COMMIT connection from deciding the outcome.
func (s *Store) confirmLicenseDeletion(id, auditID string) bool {
	if s.dsn == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(s.dsn), pgdriver.WithDialTimeout(time.Second), pgdriver.WithReadTimeout(2*time.Second), pgdriver.WithWriteTimeout(time.Second)))
	defer pool.Close()
	other := bun.NewDB(pool, s.DB.Dialect())
	defer other.Close()
	var confirmed bool
	err := other.NewRaw("SELECT NOT EXISTS(SELECT 1 FROM licenses WHERE id=?) AND EXISTS(SELECT 1 FROM audit_logs WHERE id=? AND entity_id=? AND action='deleted')", id, auditID, id).Scan(ctx, &confirmed)
	return err == nil && confirmed
}

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
