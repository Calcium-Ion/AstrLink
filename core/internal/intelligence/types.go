// Package intelligence runs persisted, explicitly targeted provider evaluations.
package intelligence

import (
	"crypto/rand"
	"fmt"
	"github.com/QuantumNous/astrlink/core/contract"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const DefaultModel = "gpt-6-astra"

type Question struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Prompt       string `json:"prompt"`
	Answer       string `json:"answer"`
	Model        string `json:"model"`
	ReferencePNG string `json:"reference_png,omitempty"`
}
type Settings struct {
	DefaultModel       string             `json:"default_model"`
	JudgeServiceID     contract.ServiceID `json:"judge_service_id"`
	JudgeModel         string             `json:"judge_model"`
	TimeoutSeconds     int                `json:"timeout_seconds"`
	Questions          []Question         `json:"questions"`
	DefaultQuestionIDs []string           `json:"default_question_ids"`
}
type ChannelConfig struct {
	UseDefault  bool     `json:"use_default"`
	Model       string   `json:"model"`
	QuestionIDs []string `json:"question_ids"`
}
type StartRequest struct {
	Mode  string `json:"mode"`
	Count int    `json:"count"`
}
type Item struct {
	Question    Question `json:"question"`
	Model       string   `json:"model"`
	Status      string   `json:"status"`
	Output      string   `json:"output"`
	Reason      string   `json:"reason"`
	PNG         string   `json:"png,omitempty"`
	DurationMS  int64    `json:"duration_ms"`
	StartedAt   string   `json:"started_at,omitempty"`
	JudgeOutput string   `json:"judge_output,omitempty"`
}
type Run struct {
	ID             string             `json:"id"`
	ServiceID      contract.ServiceID `json:"service_id"`
	StartedAt      string             `json:"started_at"`
	Status         string             `json:"status"`
	Items          []Item             `json:"items"`
	JudgeServiceID contract.ServiceID `json:"judge_service_id"`
	JudgeModel     string             `json:"judge_model"`
}
type RenderInput struct {
	QuestionID string `json:"question_id"`
	PNG        string `json:"png"`
	Error      string `json:"error"`
}

func DefaultSettings() Settings {
	return Settings{DefaultModel: DefaultModel, TimeoutSeconds: 120, DefaultQuestionIDs: []string{"pelican", "candy", "thibault"}, Questions: []Question{
		{ID: "pelican", Name: "鹈鹕骑单车 SVG", Kind: "svg", Prompt: "请用 SVG 画一只正在骑自行车的鹈鹕。画面需要清楚展示鹈鹕、自行车及骑行关系。请返回完整、可独立渲染的 SVG。", Answer: "清楚展示鹈鹕、自行车，以及鹈鹕正在骑车的关系，画面完整。"},
		{ID: "candy", Name: "糖果题", Kind: "number", Prompt: "在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）\n\n          苹果味  桃子味  西瓜味\n圆形        7       9       8\n五角星形    7       6       4", Answer: "21"},
		{ID: "thibault", Name: "Thibault Sottiaux", Kind: "text", Prompt: "don't search the internet, do you know Thibault Sottiaux on X. answer yes or no", Answer: "yes"},
	}}
}

var resourceID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func ValidRunID(id string) bool { return len(id) == 32 && resourceID.MatchString(id) }
func (s Settings) Validate() error {
	if len(s.Questions) < 1 || len(s.Questions) > 30 || len(s.DefaultModel) > 256 || len(s.JudgeModel) > 256 || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 300 {
		return fmt.Errorf("题库或生成参数无效")
	}
	if s.JudgeServiceID != "" && s.JudgeServiceID.Validate() != nil {
		return fmt.Errorf("判分渠道无效")
	}
	ids := map[string]bool{}
	for _, q := range s.Questions {
		if !resourceID.MatchString(q.ID) || ids[q.ID] || strings.TrimSpace(q.Name) == "" || len(q.Name) > 200 || !slices.Contains([]string{"svg", "number", "text"}, q.Kind) || strings.TrimSpace(q.Prompt) == "" || utf8.RuneCountInString(q.Prompt) > 1800 || strings.TrimSpace(q.Answer) == "" || len(q.Answer) > 600 || len(q.Model) > 256 {
			return fmt.Errorf("题目配置无效：%s", q.ID)
		}
		ids[q.ID] = true
		if q.ReferencePNG != "" {
			if err := contract.ValidateIntelligencePNG(q.ReferencePNG); err != nil {
				return err
			}
		}
	}
	if len(s.DefaultQuestionIDs) == 0 {
		return fmt.Errorf("请选择默认题目")
	}
	return validateIDs(s.DefaultQuestionIDs, ids)
}
func validateIDs(values []string, known map[string]bool) error {
	seen := map[string]bool{}
	for _, id := range values {
		if !known[id] || seen[id] {
			return fmt.Errorf("无效或重复题目：%s", id)
		}
		seen[id] = true
	}
	return nil
}
func (c ChannelConfig) Validate(s Settings) error {
	if len(c.Model) > 256 {
		return fmt.Errorf("模型名称过长")
	}
	ids := map[string]bool{}
	for _, q := range s.Questions {
		ids[q.ID] = true
	}
	return validateIDs(c.QuestionIDs, ids)
}
func ResolveModel(service contract.Service, c ChannelConfig, s Settings, q Question) (string, error) {
	model := strings.TrimSpace(c.Model)
	if model == "" && c.UseDefault {
		model = strings.TrimSpace(q.Model)
		if model == "" {
			model = strings.TrimSpace(s.DefaultModel)
		}
	}
	if model == "" || (len(service.Models) > 0 && !slices.Contains(service.Models, model)) {
		return "", fmt.Errorf("没有指定模型：请为当前渠道选择可用的测试模型")
	}
	return model, nil
}
func SelectQuestions(s Settings, c ChannelConfig, mode string, count int) ([]Question, error) {
	ids := c.QuestionIDs
	if len(ids) == 0 {
		ids = s.DefaultQuestionIDs
	}
	if mode != "all" && mode != "random" {
		return nil, fmt.Errorf("选题方式无效")
	}
	lookup := map[string]Question{}
	for _, q := range s.Questions {
		lookup[q.ID] = q
	}
	out := []Question{}
	for _, id := range ids {
		q, ok := lookup[id]
		if !ok {
			return nil, fmt.Errorf("题目已不存在：%s", id)
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("请选择题目")
	}
	if mode == "random" {
		if count < 1 || count > len(out) {
			return nil, fmt.Errorf("随机题数超出范围")
		}
		for i := len(out) - 1; i > 0; i-- {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
			if err != nil {
				return nil, err
			}
			j := int(n.Int64())
			out[i], out[j] = out[j], out[i]
		}
		out = out[:count]
	}
	return out, nil
}
