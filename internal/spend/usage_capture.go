package spend

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/usage"
)

const maxSSEEventBytes = 32 << 20

var errSSEEventTooLarge = errors.New("SSE event too large")

// newUsageRecord extracts only bounded, non-content request metadata. Failure
// to get OS randomness disables telemetry for this request without affecting
// its dispatch.
func newUsageRecord(envelope map[string]json.RawMessage, r *http.Request, decision Decision, started time.Time) *usage.Record {
	requestID, err := randomRequestID()
	if err != nil {
		return nil
	}
	var model string
	_ = json.Unmarshal(envelope["model"], &model)
	var tier string
	_ = json.Unmarshal(envelope["service_tier"], &tier)
	var reasoning struct {
		Effort string `json:"effort"`
	}
	_ = json.Unmarshal(envelope["reasoning"], &reasoning)
	if reasoning.Effort == "" {
		_ = json.Unmarshal(envelope["reasoning_effort"], &reasoning.Effort)
	}
	var fast *bool
	if raw, ok := envelope["fast"]; ok {
		var value bool
		if json.Unmarshal(raw, &value) == nil {
			fast = &value
		}
	}
	var metadata map[string]string
	_ = json.Unmarshal(envelope["client_metadata"], &metadata)
	parentThreadID := boundedString(r.Header.Get("X-Codex-Parent-Thread-Id"), 64)
	return &usage.Record{
		RequestID:       requestID,
		StartedAt:       started.UTC(),
		ModelRequested:  boundedString(model, 128),
		TierRequested:   boundedString(tier, 64),
		ReasoningEffort: boundedString(reasoning.Effort, 64),
		ParentThreadID:  parentThreadID,
		ThreadID:        requestThreadID(r, metadata),
		TurnID:          boundedString(metadata["turn_id"], 64),
		Subagent:        isSubagentRequest(r, metadata),
		AccountID:       decision.AccountID,
		Fast:            fast,
	}
}

func requestThreadID(r *http.Request, metadata map[string]string) string {
	if id := boundedString(metadata["thread_id"], 64); id != "" {
		return id
	}
	// The session header is accepted as a thread fallback only when it is a
	// UUID. This avoids turning arbitrary header text into a relation key.
	sessionID := boundedString(r.Header.Get("X-Codex-Session-Id"), 64)
	if isUUID(sessionID) {
		return sessionID
	}
	return ""
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func isSubagentRequest(r *http.Request, metadata map[string]string) bool {
	return metadata["x-openai-subagent"] != "" || r.Header.Get("X-OpenAI-Subagent") != ""
}

func boundedString(value string, max int) string {
	if len(value) > max {
		return ""
	}
	return value
}

func randomRequestID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	// RFC 4122 UUID v4 bits; a random UUID makes idempotent ledger operations
	// safe across process restarts without creating a stable user identifier.
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], id[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], id[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], id[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], id[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], id[10:16])
	return string(encoded[:]), nil
}

func invokeBegin(begin func(context.Context, *Record), ctx context.Context, record *Record) {
	if begin == nil || record == nil {
		return
	}
	// A telemetry callback is advisory and must never stop or alter inference.
	defer func() { _ = recover() }()
	begin(ctx, record)
}

func cloneUsageRecord(record *usage.Record) *usage.Record {
	if record == nil {
		return nil
	}
	copy := *record
	if record.Usage != nil {
		tokens := *record.Usage
		tokens.InputTokens = cloneInt64(record.Usage.InputTokens)
		tokens.OutputTokens = cloneInt64(record.Usage.OutputTokens)
		tokens.TotalTokens = cloneInt64(record.Usage.TotalTokens)
		tokens.CachedInputTokens = cloneInt64(record.Usage.CachedInputTokens)
		tokens.CacheWriteTokens = cloneInt64(record.Usage.CacheWriteTokens)
		tokens.ReasoningTokens = cloneInt64(record.Usage.ReasoningTokens)
		copy.Usage = &tokens
	}
	copy.Fast = cloneBool(record.Fast)
	return &copy
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// responseUsagePayload mirrors only authoritative response usage and serving
// metadata; output text, tool calls and reasoning content are never retained.
type responseUsagePayload struct {
	Model       *string         `json:"model"`
	ServiceTier *string         `json:"service_tier"`
	Fast        *bool           `json:"fast"`
	Usage       json.RawMessage `json:"usage"`
}

type rawTokenUsage struct {
	InputTokens        *int64 `json:"input_tokens"`
	OutputTokens       *int64 `json:"output_tokens"`
	TotalTokens        *int64 `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens     *int64 `json:"cached_tokens"`
		CacheWriteTokens *int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func decodeTokenUsage(raw json.RawMessage, source string) *usage.TokenUsage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var got rawTokenUsage
	if json.Unmarshal(raw, &got) != nil {
		return nil
	}
	result := &usage.TokenUsage{
		InputTokens:  cloneInt64(got.InputTokens),
		OutputTokens: cloneInt64(got.OutputTokens),
		TotalTokens:  cloneInt64(got.TotalTokens),
		Source:       source,
	}
	if got.InputTokensDetails != nil {
		result.CachedInputTokens = cloneInt64(got.InputTokensDetails.CachedTokens)
		result.CacheWriteTokens = cloneInt64(got.InputTokensDetails.CacheWriteTokens)
	}
	if got.OutputTokensDetails != nil {
		result.ReasoningTokens = cloneInt64(got.OutputTokensDetails.ReasoningTokens)
	}
	return result
}

func captureResponsePayload(record *usage.Record, raw json.RawMessage, source string) {
	if record == nil || len(raw) == 0 {
		return
	}
	var response responseUsagePayload
	if json.Unmarshal(raw, &response) != nil {
		return
	}
	captureResponseMetadata(record, response)
	if captured := decodeTokenUsage(response.Usage, source); captured != nil {
		record.Usage = captured
	}
}

func captureResponseMetadata(record *usage.Record, response responseUsagePayload) {
	if response.Model != nil {
		record.ModelServed = boundedString(*response.Model, 128)
	}
	if response.ServiceTier != nil {
		record.TierServed = boundedString(*response.ServiceTier, 64)
	}
	if response.Fast != nil {
		record.Fast = cloneBool(response.Fast)
	}
}

func captureCompactionUsage(record *usage.Record, body []byte) {
	captureJSONResponseUsage(record, body, "compaction")
}

func captureJSONResponseUsage(record *usage.Record, body []byte, source string) {
	if record == nil {
		return
	}
	var response responseUsagePayload
	if json.Unmarshal(body, &response) == nil && (response.Usage != nil || response.Model != nil || response.ServiceTier != nil) {
		captureResponsePayload(record, body, source)
		return
	}
	var wrapped struct {
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(body, &wrapped) == nil {
		captureResponsePayload(record, wrapped.Response, source)
	}
}

type responseEventEnvelope struct {
	Type     string          `json:"type"`
	Response json.RawMessage `json:"response"`
}

func captureSSEEvent(record *usage.Record, data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var event responseEventEnvelope
	if json.Unmarshal(data, &event) != nil {
		return ""
	}
	switch event.Type {
	case "response.created", "response.in_progress":
		// Serving metadata may be sent before the terminal response. Progress
		// token counters are not final usage and are deliberately not captured.
		if record != nil {
			var response responseUsagePayload
			if json.Unmarshal(event.Response, &response) == nil {
				captureResponseMetadata(record, response)
			}
		}
		return ""
	case "response.completed":
		if record != nil {
			captureResponsePayload(record, event.Response, "responses")
		}
		return "completed"
	case "response.failed", "response.incomplete", "error":
		if record != nil {
			captureResponsePayload(record, event.Response, "responses")
		}
		return "upstream-failed"
	default:
		return ""
	}
}

// forwardResponsesSSE parses only SSE framing and terminal response usage,
// forwarding each original byte (including LF/CRLF delimiters) unchanged.
// Event memory is bounded to the existing 32 MiB scanner allowance.
func forwardResponsesSSE(w http.ResponseWriter, src io.Reader, telemetry *usage.Record) (outcome, failure string) {
	reader := bufio.NewReaderSize(src, 32*1024)
	controller := http.NewResponseController(w)
	var eventData []byte
	hasData := false
	eventBytes := 0
	firstLine := true
	for {
		raw, line, terminated, readErr := readSSELine(reader)
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, errSSEEventTooLarge) && len(raw) > 0 {
				if _, err := w.Write(raw); err != nil {
					return "client-disconnected", ""
				}
				if err := controller.Flush(); err != nil {
					return "client-disconnected", transportFailureKind(err)
				}
			}
			if errors.Is(readErr, errSSEEventTooLarge) {
				return "stream-interrupted", "stream-event-too-large"
			}
			if !errors.Is(readErr, io.EOF) {
				return "stream-interrupted", "stream-" + transportFailureKind(readErr)
			}
			if len(raw) > 0 {
				if _, err := w.Write(raw); err != nil {
					return "client-disconnected", ""
				}
				if err := controller.Flush(); err != nil {
					return "client-disconnected", transportFailureKind(err)
				}
			}
			return "stream-interrupted", "stream-eof-before-terminal"
		}
		if !terminated {
			if _, err := w.Write(raw); err != nil {
				return "client-disconnected", ""
			}
			if err := controller.Flush(); err != nil {
				return "client-disconnected", transportFailureKind(err)
			}
			return "stream-interrupted", "stream-eof-before-terminal"
		}
		eventBytes += len(raw)
		if eventBytes > maxSSEEventBytes {
			return "stream-interrupted", "stream-event-too-large"
		}
		terminal := ""
		if firstLine {
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
			firstLine = false
		}
		if len(line) == 0 {
			if hasData {
				terminal = captureSSEEvent(telemetry, eventData)
			}
			eventData = eventData[:0]
			hasData = false
			eventBytes = 0
		}
		if _, err := w.Write(raw); err != nil {
			return "client-disconnected", ""
		}
		if len(line) == 0 {
			if err := controller.Flush(); err != nil {
				return "client-disconnected", transportFailureKind(err)
			}
			if terminal != "" {
				return terminal, ""
			}
			continue
		}
		if field, value, ok := bytes.Cut(line, []byte(":")); ok && bytes.Equal(field, []byte("data")) {
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			if hasData {
				eventData = append(eventData, '\n')
			}
			eventData = append(eventData, value...)
			hasData = true
		} else if bytes.Equal(line, []byte("data")) {
			if hasData {
				eventData = append(eventData, '\n')
			}
			hasData = true
		}
		if err := controller.Flush(); err != nil {
			return "client-disconnected", transportFailureKind(err)
		}
	}
}

// readSSELine supports the LF, CRLF and CR line endings allowed by SSE. It
// returns raw bytes intact and a delimiter-free view used only for parsing.
func readSSELine(reader *bufio.Reader) (raw, line []byte, terminated bool, err error) {
	const maxLineBytes = maxSSEEventBytes
	for {
		b, readErr := reader.ReadByte()
		if readErr != nil {
			if len(raw) > 0 && errors.Is(readErr, io.EOF) {
				return raw, raw, false, io.EOF
			}
			return raw, nil, false, readErr
		}
		raw = append(raw, b)
		if len(raw) > maxLineBytes {
			return raw, nil, false, errSSEEventTooLarge
		}
		if b == '\n' {
			line = raw[:len(raw)-1]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return raw, line, true, nil
		}
		if b == '\r' {
			if next, peekErr := reader.Peek(1); peekErr == nil && next[0] == '\n' {
				lf, _ := reader.ReadByte()
				raw = append(raw, lf)
				return raw, raw[:len(raw)-2], true, nil
			}
			return raw, raw[:len(raw)-1], true, nil
		}
	}
}
