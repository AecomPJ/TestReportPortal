package main

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
)

// updateRun: PATCH /api/runs/:id
// Only fields present in the body are changed. Typical use is recording finished_at once an
// automated run completes. The run's app, type, start time and case set cannot be changed.
func (s *server) updateRun(c echo.Context) error {
	key, err := id(c, "id")
	if err != nil {
		return err
	}
	var v struct {
		Finished *time.Time `json:"finished_at"`
		Runner   *string    `json:"runner_name"`
		By       *string    `json:"executed_by"`
		Version  *string    `json:"app_version"`
		Device   *string    `json:"device_name"`
		Android  *string    `json:"android_version"`
		Notes    *string    `json:"notes"`
		Summary  *string    `json:"summary"`
	}
	if err = decode(c, &v); err != nil {
		return err
	}
	if v.Finished == nil && v.Runner == nil && v.By == nil && v.Version == nil && v.Device == nil && v.Android == nil && v.Notes == nil && v.Summary == nil {
		return echo.NewHTTPError(400, "更新する項目を1つ以上指定してください")
	}
	ctx := c.Request().Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var started time.Time
	err = tx.QueryRow(ctx, "SELECT started_at FROM test_runs WHERE id=$1 FOR UPDATE", key).Scan(&started)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(404, "実行回が見つかりません")
	}
	if err != nil {
		return err
	}
	if v.Finished != nil && v.Finished.Before(started) {
		return echo.NewHTTPError(400, "終了日時は開始日時以降にしてください")
	}
	// NULL parameters keep the current value.
	b, err := object(ctx, tx, `UPDATE test_runs SET
		finished_at=coalesce($2,finished_at), runner_name=coalesce($3,runner_name), executed_by=coalesce($4,executed_by),
		app_version=coalesce($5,app_version), device_name=coalesce($6,device_name), android_version=coalesce($7,android_version),
		notes=coalesce($8,notes), summary=coalesce($9,summary)
		WHERE id=$1 RETURNING to_jsonb(test_runs)`, key, v.Finished, v.Runner, v.By, v.Version, v.Device, v.Android, v.Notes, v.Summary)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return c.JSONBlob(200, b)
}
