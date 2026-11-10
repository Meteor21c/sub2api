package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ModelTestInterval      = 2 * time.Hour
	ModelTestRetention     = 24 * time.Hour
	modelTestTimeout       = 10 * time.Minute
	modelTestMaxBody       = 1024 * 1024
	ModelTestPelicanPrompt = "只返回一个完整 SVG：绘制一只鹈鹕骑自行车环游地球的精美 2D 无缝循环动画。使用横向 viewBox，完整显示鹈鹕长嘴、喉囊、身体和整辆车；车架连接清晰，双脚踩踏板、车轮转动，分层景物移动形成骑行感。画面明亮，主体突出。可用内联 CSS 或 SMIL；不要 Markdown、HTML、JavaScript、外部图片、字体或其他外部资源。"
	ModelTestCandyPrompt   = "黑袋中有三种口味、两种可凭手感区分的形状的糖果，但不能凭手感区分口味。数量如下：圆形：苹果 7、桃子 9、西瓜 8；五角星形：苹果 7、桃子 6、西瓜 4。活动前可以预定各取多少颗圆形和五角星形。最少共取多少颗，才能保证同时拥有“圆形苹果 + 五角星桃子”或“圆形桃子 + 五角星苹果”？给出简短解法，并在最后单独一行输出 FINAL_ANSWER: 整数。"
)

var (
	ErrModelTestNotFound    = infraerrors.NotFound("MODEL_TEST_NOT_FOUND", "测试记录不存在或已过期")
	ErrModelTestInvalid     = infraerrors.BadRequest("MODEL_TEST_INVALID", "请选择有效的分组、模型和测试题目")
	ErrModelTestKey         = infraerrors.Forbidden("MODEL_TEST_KEY_UNAVAILABLE", "请选择本人有效且已绑定分组的 API Key")
	ErrModelTestLimit       = infraerrors.New(429, "MODEL_TEST_LIMIT", "测试任务较多，请稍后再试（每人最多同时 2 项、每小时 20 项）")
	ErrModelTestConflict    = infraerrors.New(409, "MODEL_TEST_CONFLICT", "本次提交已用于另一项测试，请重新提交")
	ErrModelTestBusy        = infraerrors.New(409, "MODEL_TEST_BUSY", "测试仍在排队或运行，请完成后再操作")
	ErrModelTestUnavailable = infraerrors.New(503, "MODEL_TEST_UNAVAILABLE", "测试服务暂不可用，请稍后再试")
)

type ModelTestPlan struct {
	ID        int64      `json:"id"`
	Label     string     `json:"label"`
	GroupID   int64      `json:"group_id"`
	Model     string     `json:"model"`
	TestKind  string     `json:"test_kind"`
	Enabled   bool       `json:"enabled"`
	NextRunAt *time.Time `json:"next_run_at"`
	LastRunAt *time.Time `json:"last_run_at"`
}

type ModelTestPlanInput struct {
	Label    string `json:"label"`
	GroupID  int64  `json:"group_id"`
	Model    string `json:"model"`
	TestKind string `json:"test_kind"`
	Enabled  bool   `json:"enabled"`
}

type ModelTestJob struct {
	ID              string     `json:"id"`
	Source          string     `json:"source"`
	OwnerUserID     *int64     `json:"-"`
	APIKeyID        *int64     `json:"api_key_id,omitempty"`
	PlanID          *int64     `json:"plan_id,omitempty"`
	GroupID         int64      `json:"-"`
	Label           string     `json:"label"`
	Model           string     `json:"model"`
	TestKind        string     `json:"test_kind"`
	Status          string     `json:"status"`
	Verdict         string     `json:"verdict"`
	Answer          *int64     `json:"answer"`
	Content         string     `json:"content,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	TotalMs         int64      `json:"total_ms"`
	InputTokens     int64      `json:"input_tokens"`
	OutputTokens    int64      `json:"output_tokens"`
	ClientRequestID string     `json:"-"`
	ClientIP        string     `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	// Routing details are persisted for administrator inspection only.
	UpstreamAccountID   *int64                     `json:"-"`
	UpstreamAccountName string                     `json:"-"`
	UpstreamAttempts    []ModelTestUpstreamAttempt `json:"-"`
	Manual              bool                       `json:"-"`
	RetryOf             *string                    `json:"-"`
}

type ModelTestUpstreamAttempt struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	Success     bool   `json:"success"`
	StatusCode  int    `json:"status_code"`
}

// ModelTestAdminJob is deliberately separate from the customer DTO. Never
// return this shape through the authenticated customer routes.
type ModelTestAdminJob struct {
	ModelTestJob
	UpstreamAccountID   *int64                     `json:"upstream_account_id"`
	UpstreamAccountName string                     `json:"upstream_account_name"`
	UpstreamAttempts    []ModelTestUpstreamAttempt `json:"upstream_attempts"`
	Manual              bool                       `json:"manual"`
	RetryOf             *string                    `json:"retry_of"`
}

func ModelTestAdminView(j ModelTestJob) ModelTestAdminJob {
	attempts := j.UpstreamAttempts
	if attempts == nil {
		attempts = []ModelTestUpstreamAttempt{}
	}
	return ModelTestAdminJob{ModelTestJob: j, UpstreamAccountID: j.UpstreamAccountID,
		UpstreamAccountName: j.UpstreamAccountName, UpstreamAttempts: attempts, Manual: j.Manual, RetryOf: j.RetryOf}
}

type ModelTestManualInput struct {
	ClientRequestID string `json:"client_request_id"`
}

type ModelTestSubmitInput struct {
	APIKeyID        int64  `json:"api_key_id"`
	Model           string `json:"model"`
	TestKind        string `json:"test_kind"`
	ClientRequestID string `json:"client_request_id"`
}

type ModelTestKeyOption struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	GroupName string `json:"group_name"`
}

type ModelTestGroupOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type ModelTestRepository interface {
	ListPlans(context.Context) ([]ModelTestPlan, error)
	SavePlan(context.Context, int64, int64, ModelTestPlanInput) (*ModelTestPlan, error)
	DeletePlan(context.Context, int64) error
	EnqueueDue(context.Context) error
	CreateUserJob(context.Context, *ModelTestJob) (*ModelTestJob, error)
	ListJobs(context.Context, int64, string) ([]ModelTestJob, error)
	GetJob(context.Context, int64, string, string) (*ModelTestJob, error)
	ListAdminJobs(context.Context) ([]ModelTestJob, error)
	GetAdminJob(context.Context, string) (*ModelTestJob, error)
	DeleteAdminJob(context.Context, string) error
	CreateManualJob(context.Context, int64, int64, string, string) (*ModelTestJob, error)
	Claim(context.Context, string) (*ModelTestJob, error)
	Finish(context.Context, string, *ModelTestJob) error
	Cleanup(context.Context) error
}
