package modelroute

import (
	"strings"

	"github.com/tidwall/gjson"
)

// HasContent recognizes useful deltas only, not role/usage/keepalive metadata.
// It does not infer billable token counts or alter the forwarded payload.
func HasContent(data string) bool {
	if !gjson.Valid(data) {
		return false
	}
	v := gjson.Parse(data)
	switch v.Get("type").String() {
	case "response.output_text.delta", "response.function_call_arguments.delta":
		return strings.TrimSpace(v.Get("delta").String()) != ""
	case "content_block_delta":
		return strings.TrimSpace(v.Get("delta.text").String()) != "" || strings.TrimSpace(v.Get("delta.partial_json").String()) != ""
	}
	for _, choice := range v.Get("choices").Array() {
		if strings.TrimSpace(choice.Get("delta.content").String()) != "" {
			return true
		}
		for _, tool := range choice.Get("delta.tool_calls").Array() {
			if strings.TrimSpace(tool.Get("function.arguments").String()) != "" || tool.Get("function.name").String() != "" {
				return true
			}
		}
	}
	for _, candidate := range v.Get("candidates").Array() {
		for _, part := range candidate.Get("content.parts").Array() {
			if part.Get("thought").Bool() {
				continue
			}
			if strings.TrimSpace(part.Get("text").String()) != "" || part.Get("functionCall.name").String() != "" {
				return true
			}
		}
	}
	return false
}
