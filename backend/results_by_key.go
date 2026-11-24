package main

import (
	"errors"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
)

type resultPatch struct {
	Status    *string `json:"status"`
	Actual    *string `json:"actual_result"`
	Comment   *string `json:"comment"`
	ChangedBy string  `json:"changed_by"`
	Reason    string  `json:"reason"`
}

// resultIDByKey resolves :run + :case_key to the result ID. The pair is unique because of
// UNIQUE(test_run_id,test_case_id) and UNIQUE(app_id,case_key).
func (s *server) resultIDByKey(c echo.Context) (int64, error) {
	run, err := id(c, "run")
	if err != nil {
		return 0, err
	}
	key := c.Param("case_key")
	if decoded, e := url.PathUnescape(key); e == nil {
		key = decoded
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, echo.NewHTTPError(400, "ケースの識別子を指定してください")
	}
	ctx := c.Request().Context()
	var found bool
	if err = s.db.QueryRow(ctx, "SELECT true FROM test_runs WHERE id=$1", run).Scan(&found); errors.Is(err, pgx.ErrNoRows) {
		return 0, echo.NewHTTPError(404, "実行回が見つかりません")
	} else if err != nil {
		return 0, err
	}
	var result int64
	err = s.db.QueryRow(ctx, "SELECT r.id FROM test_results r JOIN test_cases t ON t.id=r.test_case_id WHERE r.test_run_id=$1 AND t.case_key=$2", run, key).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, echo.NewHTTPError(404, "この実行回にケース「"+key+"」は含まれていません")
	}
	return result, err
}

// saveResultByKey: PUT /api/runs/:run/results/by-key/:case_key (same body as PUT /api/results/:id)
func (s *server) saveResultByKey(c echo.Context) error {
	key, err := s.resultIDByKey(c)
	if err != nil {
		return err
	}
	var v resultInput
	if err = decode(c, &v); err != nil {
		return err
	}
	return s.updateResult(c, key, v, 0, nil)
}

// patchResultByKey: PATCH /api/runs/:run/results/by-key/:case_key (only given fields change)
func (s *server) patchResultByKey(c echo.Context) error {
	key, err := s.resultIDByKey(c)
	if err != nil {
		return err
	}
	var v resultPatch
	if err = decode(c, &v); err != nil {
		return err
	}
	if v.Status == nil && v.Actual == nil && v.Comment == nil {
		return echo.NewHTTPError(400, "status・actual_result・commentのいずれかを指定してください")
	}
	return s.updateResult(c, key, resultInput{}, 0, &v)
}

// uploadByKey: POST /api/runs/:run/results/by-key/:case_key/attachments
func (s *server) uploadByKey(c echo.Context) error {
	key, err := s.resultIDByKey(c)
	if err != nil {
		return err
	}
	return s.saveAttachment(c, key)
}
