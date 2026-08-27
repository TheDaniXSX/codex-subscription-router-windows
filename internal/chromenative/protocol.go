package chromenative

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const (
	ProtocolVersion = "codex-router-native-v1"
	HostName        = "io.github.thedanixsx.codex_subscription_router"
	MaxMessageSize  = 1024 * 1024
)

type Request struct {
	Protocol string          `json:"protocol"`
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type Response struct {
	Protocol string `json:"protocol"`
	ID       string `json:"id,omitempty"`
	OK       bool   `json:"ok"`
	Result   any    `json:"result,omitempty"`
	Error    *Error `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type BrowserContext struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Selection string `json:"selection,omitempty"`
	Text      string `json:"text,omitempty"`
}

func ReadFrame(reader io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header)
	if size == 0 {
		return nil, errors.New("native message is empty")
	}
	if size > MaxMessageSize {
		return nil, fmt.Errorf("native message is %d bytes; maximum is %d", size, MaxMessageSize)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("read native message payload: %w", err)
	}
	return payload, nil
}

func WriteFrame(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode native message: %w", err)
	}
	if len(payload) == 0 || len(payload) > MaxMessageSize {
		return fmt.Errorf("native message is %d bytes; maximum is %d", len(payload), MaxMessageSize)
	}
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(payload)))
	buffer := bufio.NewWriter(writer)
	if _, err := buffer.Write(header); err != nil {
		return err
	}
	if _, err := buffer.Write(payload); err != nil {
		return err
	}
	return buffer.Flush()
}

func ParseRequest(payload []byte) (Request, error) {
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("invalid request JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return request, errors.New("request contains trailing JSON")
	}
	request.Protocol = strings.TrimSpace(request.Protocol)
	request.ID = strings.TrimSpace(request.ID)
	request.Type = strings.TrimSpace(request.Type)
	if request.Protocol != ProtocolVersion {
		return request, fmt.Errorf("unsupported protocol %q", request.Protocol)
	}
	if request.ID == "" || len(request.ID) > 128 {
		return request, errors.New("request id must contain 1 to 128 characters")
	}
	if request.Type == "" || len(request.Type) > 64 {
		return request, errors.New("request type must contain 1 to 64 characters")
	}
	return request, nil
}

func ParseBrowserContext(payload json.RawMessage) (BrowserContext, error) {
	var context BrowserContext
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&context); err != nil {
		return context, fmt.Errorf("invalid browser context: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return context, errors.New("browser context contains trailing JSON")
	}
	context.URL = strings.TrimSpace(context.URL)
	context.Title = strings.TrimSpace(context.Title)
	if len(context.URL) == 0 || len(context.URL) > 16*1024 {
		return context, errors.New("url must contain 1 to 16384 characters")
	}
	parsed, err := url.ParseRequestURI(context.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return context, errors.New("url must be an absolute http or https URL")
	}
	if len(context.Title) > 4096 {
		return context, errors.New("title exceeds 4096 characters")
	}
	if len(context.Selection) > 64*1024 {
		return context, errors.New("selection exceeds 65536 characters")
	}
	if len(context.Text) > 256*1024 {
		return context, errors.New("text exceeds 262144 characters")
	}
	return context, nil
}

func Failure(id, code, message string) Response {
	return Response{Protocol: ProtocolVersion, ID: id, OK: false, Error: &Error{Code: code, Message: message}}
}
