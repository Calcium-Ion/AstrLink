package intelligence

import (
	"encoding/json"
	"regexp"
	"strings"
)

var finalNumber = regexp.MustCompile(`(?i)(?:最终答案|答案|answer|最少(?:需要|取出|摸出)?|因此|所以)(?:是|为|需要|取出|摸出|至少|\s|[:：=])*([0-9]+)(?:\s*(?:颗|个|粒))?\s*[。.!！]?\s*$`)
var onlyNumber = regexp.MustCompile(`^([0-9]+)(?:\s*(?:颗|个|粒))?[。.!！]?$`)
var boxedNumber = regexp.MustCompile(`\\boxed\{\s*([0-9]+)\s*(?:\\text\{[^{}]*\})?\s*\}\s*(?:\\\]|\$\$?)?\s*[。.!！]?\s*$`)

func Grade(q Question, output string) (string, string) {
	text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(output, "**", ""), "`", ""))
	answer := ""
	if q.Kind == "number" {
		if match := onlyNumber.FindStringSubmatch(text); len(match) > 1 {
			answer = match[1]
		} else if match := boxedNumber.FindStringSubmatch(text); len(match) > 1 {
			answer = match[1]
		} else if match := finalNumber.FindStringSubmatch(text); len(match) > 1 {
			answer = match[1]
		} else {
			return "ungraded", "无法提取唯一最终答案，请查看原文"
		}
	} else {
		answer = strings.ToLower(strings.TrimSpace(strings.TrimRight(text, "。.!！")))
	}
	if answer == strings.ToLower(strings.TrimSpace(q.Answer)) {
		return "passed", "最终答案与标准答案一致"
	}
	return "failed", "最终答案：" + answer + "；标准答案：" + q.Answer
}

func GradeImage(output string) (string, string) {
	text := strings.TrimSpace(output)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	var verdict struct {
		Passed *bool  `json:"passed"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(text), &verdict) != nil || verdict.Passed == nil || strings.TrimSpace(verdict.Reason) == "" {
		return "ungraded", "判分模型未返回有效判定，请查看判分原文"
	}
	if *verdict.Passed {
		return "passed", verdict.Reason
	}
	return "failed", verdict.Reason
}
