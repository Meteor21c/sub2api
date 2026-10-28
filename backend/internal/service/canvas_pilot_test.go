package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const testCanvasProject = `{"id":"test_project","title":"测试画布","nodes":[],"connections":[],"chatSessions":[],"agentConfig":null,"viewport":{"x":0,"y":0,"k":1}}`

func TestCanvasValidationRejectsCredentialsAndEmbeddedMedia(t *testing.T) {
	for _, field := range []string{`"apiKey":"sk-private"`, `"password":"private"`, `"authorization":"Bearer private"`, `"content":"data:image/png;base64,AAAA"`, `"url":"https://example.test/a.png?api_key=private"`, `"__proto__":{}`} {
		raw := strings.TrimSuffix(testCanvasProject, "}") + "," + field + "}"
		if _, err := validateCanvasProject(json.RawMessage(raw)); !errors.Is(err, ErrCanvasInvalid) {
			t.Fatalf("accepted unsafe field %s: %v", field, err)
		}
	}
	if _, err := validateCanvasProject(json.RawMessage(testCanvasProject)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"id":"../project","title":"test","nodes":[],"connections":[]}`,
		`{"id":"test","title":"test","nodes":[{"id":"node","type":"video"}],"connections":[]}`,
		`{"id":"test","title":"test","nodes":[],"connections":[{"id":"edge","fromNodeId":"missing","toNodeId":"other"}]}`,
		`{"id":"test","title":"test","revision":-1,"nodes":[],"connections":[]}`,
	} {
		if _, err := validateCanvasProject(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid project: %s", raw)
		}
	}
}

func newTestCanvasService(t *testing.T) (*CanvasPilotService, sqlmock.Sqlmock) {
	t.Helper()
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "7")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewCanvasPilotService(db, NewBusinessMonitorService(nil)), mock
}

func TestCanvasSaveRejectsOtherOwnerBeforeDatabase(t *testing.T) {
	svc, mock := newTestCanvasService(t)
	if _, err := svc.Save(context.Background(), 8, []json.RawMessage{json.RawMessage(testCanvasProject)}); err == nil {
		t.Fatal("other owner can save")
	}
	if _, err := svc.List(context.Background(), 8); err == nil {
		t.Fatal("other owner can list")
	}
	if err := svc.Delete(context.Background(), 8, []string{"test_project"}); err == nil {
		t.Fatal("other owner can delete")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectCanvasTransaction(mock sqlmock.Sqlmock, count, bytes int, revision int64) {
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT\(\*\), COALESCE\(SUM\(data_bytes\),0\)`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"count", "bytes"}).AddRow(count, bytes))
	query := mock.ExpectQuery(`SELECT revision, data_bytes FROM canvas_pilot_projects WHERE owner_id=\$1 AND project_id=\$2 FOR UPDATE`).WithArgs(int64(7), "test_project")
	if revision == 0 {
		query.WillReturnError(sql.ErrNoRows)
	} else {
		query.WillReturnRows(sqlmock.NewRows([]string{"revision", "data_bytes"}).AddRow(revision, 100))
	}
}

func TestCanvasRevisionConflictRollsBack(t *testing.T) {
	svc, mock := newTestCanvasService(t)
	expectCanvasTransaction(mock, 1, 100, 3)
	mock.ExpectRollback()
	if _, err := svc.Save(context.Background(), 7, []json.RawMessage{json.RawMessage(testCanvasProject)}); !errors.Is(err, ErrCanvasConflict) {
		t.Fatalf("want conflict got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasQuotaIsCheckedInsideWriteTransaction(t *testing.T) {
	svc, mock := newTestCanvasService(t)
	expectCanvasTransaction(mock, CanvasMaxProjects, 100, 0)
	mock.ExpectRollback()
	if _, err := svc.Save(context.Background(), 7, []json.RawMessage{json.RawMessage(testCanvasProject)}); !errors.Is(err, ErrCanvasQuota) {
		t.Fatalf("want quota got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasSaveReturnsRevisionAndOwnerScopedList(t *testing.T) {
	svc, mock := newTestCanvasService(t)
	expectCanvasTransaction(mock, 0, 0, 0)
	mock.ExpectExec(`INSERT INTO canvas_pilot_projects`).WithArgs(int64(7), "test_project", int64(1), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	saved, err := svc.Save(context.Background(), 7, []json.RawMessage{json.RawMessage(testCanvasProject)})
	if err != nil || len(saved) != 1 || !strings.Contains(string(saved[0]), `"revision":1`) {
		t.Fatalf("saved=%s err=%v", saved, err)
	}
	mock.ExpectQuery(`SELECT data FROM canvas_pilot_projects WHERE owner_id=\$1`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow(saved[0]))
	listed, err := svc.List(context.Background(), 7)
	if err != nil || len(listed) != 1 || string(listed[0]) != string(saved[0]) {
		t.Fatalf("list=%s err=%v", listed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
