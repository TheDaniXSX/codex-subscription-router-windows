package mux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/protocol"
)

const (
	defaultCombinedThreadLimit = 20
	maximumCombinedThreadLimit = 500
	threadListPageSize         = 100
	maximumThreadsPerAccount   = 2_000
	maximumThreadListPages     = maximumThreadsPerAccount / threadListPageSize
	combinedCursorPrefix       = "codex-mux-offset:"
)

type threadListResult struct {
	index     int
	accountID string
	threads   []map[string]any
	err       error
}

func (m *Multiplexer) aggregateThreadList(request protocol.Message) {
	entries := m.childEntries()
	if len(entries) == 0 {
		m.write(protocol.Failure(request.ID, -32032, "no enabled account app-server is available"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.requestTimeout)
	defer cancel()

	results := make(chan threadListResult, len(entries))
	semaphore := make(chan struct{}, maximumConcurrentChildren)
	var wait sync.WaitGroup
	for index, entry := range entries {
		index, entry := index, entry
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- threadListResult{index: index, accountID: entry.account.ID, err: ctx.Err()}
				return
			}
			threads, err := m.listAllThreads(ctx, entry, request.Params)
			results <- threadListResult{index: index, accountID: entry.account.ID, threads: threads, err: err}
		}()
	}
	wait.Wait()
	close(results)

	ordered := make([]threadListResult, len(entries))
	for result := range results {
		ordered[result.index] = result
	}
	partialErrors := make(map[string]string)
	successes := 0
	for _, result := range ordered {
		if result.err != nil {
			partialErrors[result.accountID] = result.err.Error()
		}
		if result.err == nil || len(result.threads) > 0 {
			successes++
		}
	}
	if successes == 0 {
		m.write(protocol.Failure(request.ID, -32033, "thread history is unavailable for every enabled account"))
		m.publish(Event{Type: "thread-list-error", Message: "Thread history could not be loaded", Data: partialErrors})
		return
	}
	if len(partialErrors) > 0 {
		m.publish(Event{Type: "thread-list-partial", Message: "Some account histories could not be loaded", Data: partialErrors})
		fmt.Fprintf(os.Stderr, "codex-mux: partial thread/list failure: %v\n", partialErrors)
	}

	threads, owners := m.mergeThreadsDeterministically(ordered)
	if len(owners) > 0 {
		if err := m.store.SetThreadOwners(owners); err != nil {
			m.publish(Event{Type: "thread-list-partial", Message: "Thread ownership could not be persisted", Data: map[string]string{"state": err.Error()}})
		}
	}
	sortThreads(threads)
	limit, offset := combinedThreadPage(request.Params)
	if offset > len(threads) {
		offset = len(threads)
	}
	end := min(offset+limit, len(threads))
	page := threads[offset:end]
	var nextCursor *string
	if end < len(threads) {
		value := combinedCursorPrefix + strconv.Itoa(end)
		nextCursor = &value
	}
	encoded, err := json.Marshal(map[string]any{"data": page, "nextCursor": nextCursor})
	if err != nil {
		m.write(protocol.Failure(request.ID, -32603, "failed to merge thread list"))
		return
	}
	m.write(protocol.Success(request.ID, encoded))
}

func (m *Multiplexer) mergeThreadsDeterministically(results []threadListResult) ([]map[string]any, map[string]string) {
	type candidate struct {
		accountID    string
		accountIndex int
		thread       map[string]any
	}
	byID := make(map[string][]candidate)
	anonymous := make([]map[string]any, 0)
	for _, result := range results {
		for _, thread := range result.threads {
			threadID := stringField(thread, "id")
			if threadID == "" {
				anonymous = append(anonymous, thread)
				continue
			}
			byID[threadID] = append(byID[threadID], candidate{
				accountID: result.accountID, accountIndex: result.index, thread: thread,
			})
		}
	}
	threads := make([]map[string]any, 0, len(byID)+len(anonymous))
	owners := make(map[string]string, len(byID))
	for threadID, candidates := range byID {
		selected := candidates[0]
		selectedPersistedOwner := false
		if persistedOwner, ok := m.store.ThreadOwner(threadID); ok {
			for _, candidate := range candidates {
				if candidate.accountID == persistedOwner {
					selected = candidate
					selectedPersistedOwner = true
					break
				}
			}
		}
		if !selectedPersistedOwner {
			for _, candidate := range candidates[1:] {
				candidateTime := numericField(candidate.thread, "updatedAt", "createdAt")
				selectedTime := numericField(selected.thread, "updatedAt", "createdAt")
				if candidateTime > selectedTime || (candidateTime == selectedTime && candidate.accountIndex < selected.accountIndex) {
					selected = candidate
				}
			}
		}
		threads = append(threads, selected.thread)
		owners[threadID] = selected.accountID
	}
	threads = append(threads, anonymous...)
	return threads, owners
}

func (m *Multiplexer) listAllThreads(parent context.Context, entry childEntry, originalParams json.RawMessage) ([]map[string]any, error) {
	var params map[string]any
	if json.Unmarshal(originalParams, &params) != nil {
		params = make(map[string]any)
	}
	delete(params, "cursor")
	params["limit"] = threadListPageSize
	threads := make([]map[string]any, 0, threadListPageSize)
	seenCursors := make(map[string]struct{})
	var cursor string
	for page := 0; page < maximumThreadListPages; page++ {
		if cursor == "" {
			params["cursor"] = nil
		} else {
			params["cursor"] = cursor
		}
		encodedParams, _ := json.Marshal(params)
		ctx, cancel := context.WithTimeout(parent, m.requestTimeout)
		response, err := entry.child.Request(ctx, "thread/list", encodedParams)
		cancel()
		if err != nil {
			return threads, err
		}
		var decoded struct {
			Data       []map[string]any `json:"data"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := json.Unmarshal(response.Result, &decoded); err != nil {
			return threads, fmt.Errorf("decode thread/list response: %w", err)
		}
		threads = append(threads, decoded.Data...)
		if decoded.NextCursor == nil || *decoded.NextCursor == "" {
			return threads, nil
		}
		cursor = *decoded.NextCursor
		if _, repeated := seenCursors[cursor]; repeated {
			return threads, fmt.Errorf("app-server repeated thread/list cursor %q", cursor)
		}
		seenCursors[cursor] = struct{}{}
	}
	return threads, errors.New("thread history exceeded the per-account safety limit")
}

func combinedThreadPage(params json.RawMessage) (limit, offset int) {
	limit = defaultCombinedThreadLimit
	var decoded map[string]any
	if json.Unmarshal(params, &decoded) != nil {
		return limit, 0
	}
	if requested, ok := decoded["limit"].(float64); ok && requested > 0 {
		switch {
		case requested < 1:
			limit = 1
		case requested >= maximumCombinedThreadLimit:
			limit = maximumCombinedThreadLimit
		default:
			limit = int(requested)
		}
	}
	if cursor, ok := decoded["cursor"].(string); ok && strings.HasPrefix(cursor, combinedCursorPrefix) {
		parsed, err := strconv.Atoi(strings.TrimPrefix(cursor, combinedCursorPrefix))
		if err == nil && parsed > 0 {
			offset = parsed
		}
	}
	return limit, offset
}
