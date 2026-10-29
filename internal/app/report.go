package app

import (
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

// Text mode streams as before. JSON mode bounds captured output and keeps stderr separate.
type reportOutput struct {
	mu        sync.Mutex
	writer    io.Writer
	root      *cobra.Command
	buffer    strings.Builder
	truncated bool
}

func (w *reportOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	format, _ := w.root.PersistentFlags().GetString("output")
	if format != "json" {
		return w.writer.Write(p)
	}
	remaining := (1 << 20) - w.buffer.Len()
	if len(p) > remaining {
		w.truncated = true
	}
	w.buffer.Write(p[:min(len(p), remaining)])
	return len(p), nil
}

type diagnosticOutput struct {
	report *reportOutput
	writer io.Writer
}

func (w diagnosticOutput) Write(p []byte) (int, error) {
	w.report.mu.Lock()
	defer w.report.mu.Unlock()
	return w.writer.Write(p)
}
func (w *reportOutput) finish(command string, code int, runErr error) error {
	format, _ := w.root.PersistentFlags().GetString("output")
	if format != "json" {
		return nil
	}
	result := struct {
		SchemaVersion int             `json:"schema_version"`
		Command       string          `json:"command"`
		OK            bool            `json:"ok"`
		ExitCode      int             `json:"exit_code"`
		ErrorCode     string          `json:"error_code,omitempty"`
		Result        json.RawMessage `json:"result,omitempty"`
		Log           string          `json:"log,omitempty"`
		Truncated     bool            `json:"truncated"`
	}{SchemaVersion: 1, Command: command, OK: runErr == nil, ExitCode: code, Truncated: w.truncated}
	if runErr != nil {
		result.ErrorCode = map[int]string{1: "operation-failed", 2: "invalid-input", 3: "dependency-unavailable", 4: "timeout-or-cancelled", 5: "upload-failed"}[code]
		if result.ErrorCode == "" {
			result.ErrorCode = "operation-failed"
		}
	}
	text := w.buffer.String()
	if json.Valid([]byte(text)) {
		result.Result = json.RawMessage(text)
	} else {
		result.Log = text
	}
	return json.NewEncoder(w.writer).Encode(result)
}
