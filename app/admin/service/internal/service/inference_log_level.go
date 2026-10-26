package service

import (
	"encoding/json"
	"strings"

	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	view "go-wind-admin/api/gen/go/inference/service/v1"
)

// Runtime output may be structured JSON or standard engine text. Unknown
// levels stay unknown; stderr is not itself evidence of an error level.
func inferenceLogLevel(message string) string {
	var structured struct {
		Level    string `json:"level"`
		Severity string `json:"severity"`
	}
	value := ""
	if json.Unmarshal([]byte(message), &structured) == nil {
		value = structured.Level
		if value == "" {
			value = structured.Severity
		}
	} else {
		fields := strings.Fields(message)
		if len(fields) > 0 {
			value = strings.Trim(fields[0], "[]:")
		}
	}
	switch strings.ToLower(value) {
	case "debug", "info", "error", "trace":
		return strings.ToLower(value)
	case "warning", "warn":
		return "warn"
	case "critical", "fatal":
		return "fatal"
	default:
		return "unknown"
	}
}

func ProjectInferenceLog(line *runtimev1.RuntimeLogLine, level string) (*view.RuntimeLogLine, bool) {
	if line == nil {
		return nil, false
	}
	projected := &view.RuntimeLogLine{Timestamp: line.Timestamp, Message: line.Message, Level: inferenceLogLevel(line.Message)}
	return projected, level == "" || projected.Level == level
}
