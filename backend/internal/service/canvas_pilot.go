package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	CanvasMaxProjects     = 20
	CanvasMaxProjectBytes = 1 << 20
	CanvasMaxOwnerBytes   = 10 << 20
)

var (
	ErrCanvasInvalid  = errors.New("画布项目格式无效")
	ErrCanvasConflict = errors.New("画布已在其他窗口更新，请刷新后再保存")
	ErrCanvasQuota    = errors.New("画布存储已达到额度，请导出并删除不需要的项目")
	canvasID          = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
)

// CanvasPilotService reuses the single site-owner identity and existing wallet.
// It never fetches a media URL or stores credentials or embedded media.
type CanvasPilotService struct {
	db    *sql.DB
	owner *BusinessMonitorService
}

func NewCanvasPilotService(db *sql.DB, owner *BusinessMonitorService) *CanvasPilotService {
	return &CanvasPilotService{db: db, owner: owner}
}

func (s *CanvasPilotService) Enabled() bool {
	return s != nil && s.owner != nil && s.owner.OwnerID() > 0
}
func (s *CanvasPilotService) AccessAllowed(id int64) bool {
	return s.Enabled() && s.owner.AccessAllowed(id)
}

type canvasProject struct {
	data     map[string]any
	id       string
	revision int64
}

func validateCanvasProject(raw json.RawMessage) (canvasProject, error) {
	invalid := func(reason string) (canvasProject, error) {
		return canvasProject{}, fmt.Errorf("%w：%s", ErrCanvasInvalid, reason)
	}
	if len(raw) > CanvasMaxProjectBytes {
		return invalid("单个项目不能超过 1 MiB")
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return invalid("需要项目对象")
	}
	id, _ := data["id"].(string)
	title, _ := data["title"].(string)
	if !canvasID.MatchString(id) || strings.TrimSpace(title) == "" || len(title) > 400 {
		return invalid("项目 ID 或名称不正确")
	}
	nodes, nodesOK := data["nodes"].([]any)
	edges, edgesOK := data["connections"].([]any)
	if !nodesOK || !edgesOK || len(nodes) > 300 || len(edges) > 600 {
		return invalid("最多 300 个节点、600 条连线")
	}
	nodeIDs := map[string]bool{}
	for _, value := range nodes {
		node, ok := value.(map[string]any)
		if !ok {
			return invalid("节点格式不正确")
		}
		nodeID, _ := node["id"].(string)
		typeName, _ := node["type"].(string)
		if !canvasID.MatchString(nodeID) || nodeIDs[nodeID] || (typeName != "text" && typeName != "image" && typeName != "group") {
			return invalid("仅支持文本、图片和分组节点，节点 ID 不能重复")
		}
		position, positionOK := node["position"].(map[string]any)
		if !positionOK || !validCanvasNumber(position["x"], -1e7, 1e7) || !validCanvasNumber(position["y"], -1e7, 1e7) || !validCanvasNumber(node["width"], 1, 10000) || !validCanvasNumber(node["height"], 1, 10000) {
			return invalid("节点坐标或尺寸不正确")
		}
		if metadata, exists := node["metadata"]; exists && metadata != nil {
			if _, ok := metadata.(map[string]any); !ok {
				return invalid("节点属性不正确")
			}
		}
		nodeIDs[nodeID] = true
	}
	edgeIDs := map[string]bool{}
	for _, value := range edges {
		edge, ok := value.(map[string]any)
		if !ok {
			return invalid("连线格式不正确")
		}
		edgeID, _ := edge["id"].(string)
		from, _ := edge["fromNodeId"].(string)
		to, _ := edge["toNodeId"].(string)
		if !canvasID.MatchString(edgeID) || edgeIDs[edgeID] || !nodeIDs[from] || !nodeIDs[to] || from == to {
			return invalid("连线必须连接两个现有节点")
		}
		edgeIDs[edgeID] = true
	}
	if err := validateCanvasValue(data, 0); err != nil {
		return invalid(err.Error())
	}
	revision := int64(0)
	if value, exists := data["revision"]; exists {
		number, ok := value.(float64)
		if !ok || number < 0 || number > 1e12 || float64(int64(number)) != number {
			return invalid("版本号不正确")
		}
		revision = int64(number)
	}
	return canvasProject{data: data, id: id, revision: revision}, nil
}

func validCanvasNumber(value any, minimum, maximum float64) bool {
	number, ok := value.(float64)
	return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && number >= minimum && number <= maximum
}

func validateCanvasValue(value any, depth int) error {
	if depth > 20 {
		return errors.New("项目嵌套过深")
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > 200 {
			return errors.New("项目字段过多")
		}
		for key, child := range typed {
			normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
			switch normalized {
			case "apikey", "authorization", "password", "token", "accesstoken", "refreshtoken", "secret", "secretaccesskey", "accesskeyid", "clientsecret":
				if child != nil && child != "" {
					return errors.New("项目不得包含密钥或登录凭据")
				}
			}
			if key == "__proto__" || key == "constructor" || key == "prototype" {
				return errors.New("项目字段不允许")
			}
			if err := validateCanvasValue(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > 2000 {
			return errors.New("项目数组过大")
		}
		for _, child := range typed {
			if err := validateCanvasValue(child, depth+1); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > 65536 {
			return errors.New("文本字段不能超过 64 KiB")
		}
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
			return errors.New("图片文件请使用素材引用，不得嵌入项目")
		}
		if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			parsed, err := url.Parse(trimmed)
			if err != nil || parsed.User != nil {
				return errors.New("媒体地址包含无效凭据")
			}
			for key := range parsed.Query() {
				normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
				if normalized == "apikey" || normalized == "authorization" || normalized == "accesstoken" || normalized == "token" || normalized == "password" {
					return errors.New("媒体地址不得包含密钥")
				}
			}
		}
	}
	return nil
}

func (s *CanvasPilotService) List(ctx context.Context, ownerID int64) ([]json.RawMessage, error) {
	if !s.AccessAllowed(ownerID) || s.db == nil {
		return nil, errors.New("画布服务不可用")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM canvas_pilot_projects WHERE owner_id=$1 ORDER BY updated_at DESC, project_id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	projects := make([]json.RawMessage, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		projects = append(projects, json.RawMessage(raw))
	}
	return projects, rows.Err()
}

func (s *CanvasPilotService) Save(ctx context.Context, ownerID int64, raws []json.RawMessage) ([]json.RawMessage, error) {
	if !s.AccessAllowed(ownerID) || s.db == nil {
		return nil, errors.New("画布服务不可用")
	}
	if len(raws) == 0 || len(raws) > CanvasMaxProjects {
		return nil, ErrCanvasInvalid
	}
	projects := make([]canvasProject, len(raws))
	seen := map[string]bool{}
	for i, raw := range raws {
		project, err := validateCanvasProject(raw)
		if err != nil {
			return nil, err
		}
		if seen[project.id] {
			return nil, ErrCanvasInvalid
		}
		seen[project.id] = true
		projects[i] = project
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize each owner's writes, including new projects, so quota checks are atomic.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(249, hashtext($1::text))`, ownerID); err != nil {
		return nil, err
	}
	var count, bytes int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(data_bytes),0) FROM canvas_pilot_projects WHERE owner_id=$1`, ownerID).Scan(&count, &bytes); err != nil {
		return nil, err
	}
	saved := make([]json.RawMessage, 0, len(projects))
	for _, project := range projects {
		var previousRevision int64
		var previousBytes int
		err := tx.QueryRowContext(ctx, `SELECT revision, data_bytes FROM canvas_pilot_projects WHERE owner_id=$1 AND project_id=$2 FOR UPDATE`, ownerID, project.id).Scan(&previousRevision, &previousBytes)
		if errors.Is(err, sql.ErrNoRows) {
			count++
			previousRevision = 0
		} else if err != nil {
			return nil, err
		}
		if previousRevision != project.revision {
			return nil, ErrCanvasConflict
		}
		project.data["revision"] = previousRevision + 1
		project.data["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		raw, err := json.Marshal(project.data)
		if err != nil {
			return nil, err
		}
		bytes += len(raw) - previousBytes
		if len(raw) > CanvasMaxProjectBytes || count > CanvasMaxProjects || bytes > CanvasMaxOwnerBytes {
			return nil, ErrCanvasQuota
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO canvas_pilot_projects(owner_id,project_id,revision,data,data_bytes) VALUES($1,$2,$3,$4,$5) ON CONFLICT(owner_id,project_id) DO UPDATE SET revision=EXCLUDED.revision,data=EXCLUDED.data,data_bytes=EXCLUDED.data_bytes,updated_at=NOW()`, ownerID, project.id, previousRevision+1, string(raw), len(raw)); err != nil {
			return nil, err
		}
		saved = append(saved, raw)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}

func (s *CanvasPilotService) Delete(ctx context.Context, ownerID int64, ids []string) error {
	if !s.AccessAllowed(ownerID) || s.db == nil {
		return errors.New("画布服务不可用")
	}
	if len(ids) == 0 || len(ids) > CanvasMaxProjects {
		return ErrCanvasInvalid
	}
	for _, id := range ids {
		if !canvasID.MatchString(id) {
			return ErrCanvasInvalid
		}
	}
	// Removes metadata only; deleting a node/project never cancels or resubmits a paid job.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(249, hashtext($1::text))`, ownerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM canvas_pilot_projects WHERE owner_id=$1 AND project_id=ANY($2)`, ownerID, pq.Array(ids)); err != nil {
		return err
	}
	return tx.Commit()
}
