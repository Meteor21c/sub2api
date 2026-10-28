package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Runs only against the isolated PostgreSQL fixture provisioned by GitHub CI.
func TestCanvasSQLIntegration(t *testing.T) {
	dsn := os.Getenv("CANVAS_PILOT_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated canvas PostgreSQL fixture not configured")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(3)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id) VALUES(2000000001),(2000000002) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "2000000001")
	first := NewCanvasPilotService(db, NewBusinessMonitorService(db))
	t.Setenv("BUSINESS_MONITOR_OWNER_ID", "2000000002")
	second := NewCanvasPilotService(db, NewBusinessMonitorService(db))
	base := json.RawMessage(`{"id":"sql_fixture","title":"SQL fixture","nodes":[{"id":"text1","type":"text","title":"提示词","position":{"x":0,"y":0},"width":300,"height":200,"metadata":{"content":"绘制山峰"}},{"id":"image1","type":"image","title":"图片","position":{"x":500,"y":0},"width":300,"height":300,"metadata":{"storageKey":"image:example","status":"success"}}],"connections":[{"id":"edge1","fromNodeId":"text1","toNodeId":"image1"}]}`)
	saved, err := first.Save(ctx, 2000000001, []json.RawMessage{base})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Save(ctx, 2000000002, []json.RawMessage{base}); err != nil {
		t.Fatal(err)
	}
	var project map[string]any
	if err := json.Unmarshal(saved[0], &project); err != nil {
		t.Fatal(err)
	}
	project["title"] = "updated by first owner"
	update, _ := json.Marshal(project)
	// Two tabs saving the same version must yield exactly one success.
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := first.Save(ctx, 2000000001, []json.RawMessage{update})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrCanvasConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	other, err := second.List(ctx, 2000000002)
	if err != nil || len(other) != 1 {
		t.Fatalf("other owner list=%s err=%v", other, err)
	}
	var otherProject map[string]any
	if err := json.Unmarshal(other[0], &otherProject); err != nil {
		t.Fatal(err)
	}
	if otherProject["title"] != "SQL fixture" {
		t.Fatal("one owner's write modified another owner")
	}
	if err := first.Delete(ctx, 2000000001, []string{"sql_fixture"}); err != nil {
		t.Fatal(err)
	}
	other, err = second.List(ctx, 2000000002)
	if err != nil || len(other) != 1 {
		t.Fatal("one owner's delete removed another owner's project")
	}
	// Refill one owner's full quota and assert an overflowing transaction is atomic.
	projects := make([]json.RawMessage, 0, CanvasMaxProjects)
	for i := range CanvasMaxProjects {
		item := map[string]any{"id": "quota_" + strconv.Itoa(i), "title": "quota", "nodes": []any{}, "connections": []any{}}
		raw, _ := json.Marshal(item)
		projects = append(projects, raw)
	}
	if _, err := first.Save(ctx, 2000000001, projects); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Save(ctx, 2000000001, []json.RawMessage{base}); !errors.Is(err, ErrCanvasQuota) {
		t.Fatalf("overflow err=%v", err)
	}
	listed, err := first.List(ctx, 2000000001)
	if err != nil || len(listed) != CanvasMaxProjects {
		t.Fatalf("quota transaction changed list length: %d err=%v", len(listed), err)
	}
}
