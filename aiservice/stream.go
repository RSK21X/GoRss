package aiservice

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type chatResponse struct {
	Object  string          `json:"object"`
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Delta struct {
			Content json.RawMessage `json:"content"`
		} `json:"delta"`
		Finish string `json:"finish_reason"`
	} `json:"choices"`
}

func finishReason(reason string, required bool) error {
	if reason == "length" {
		return ErrOutputLength
	}
	if reason != "stop" && (required || reason != "") {
		return ErrUnsupportedProtocol
	}
	return nil
}
func requestCompletion(ctx context.Context, x session, payload completion, timeout time.Duration, onDelta func(string)) (string, error) {
	var output string
	err := requestResponse(ctx, x, "chat/completions", payload, timeout, func(ctx context.Context, resp *http.Response) error {
		if payload.Stream && strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
			var err error
			output, err = readChatStream(ctx, resp.Body, onDelta)
			return err
		}
		data, err := readResponse(ctx, resp)
		if err != nil {
			return err
		}
		var result chatResponse
		if json.Unmarshal(data, &result) != nil {
			return ErrInvalidJSON
		}
		if result.Object != "" && result.Object != "chat.completion" {
			return ErrUnsupportedProtocol
		}
		if len(result.Choices) == 0 {
			return ErrMissingChoices
		}
		if err := finishReason(result.Choices[0].Finish, false); err != nil {
			return err
		}
		output, err = completionText(result.Choices[0].Message.Content)
		if err == nil && onDelta != nil {
			onDelta(output)
		}
		return err
	})
	return output, err
}

// Bound both the SSE wire data and the decoded text. A stop finish reason is
// required; a broken connection must never turn a partial result into a cache hit.
func readChatStream(ctx context.Context, body io.Reader, onDelta func(string)) (string, error) {
	limited := &io.LimitedReader{R: body, N: 8<<20 + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var output, event strings.Builder
	finished := false
	last := time.Time{}
	firstLine := true
	emit := func(force bool) {
		if onDelta != nil && output.Len() > 0 && (force || time.Since(last) >= 40*time.Millisecond) {
			onDelta(output.String())
			last = time.Now()
		}
	}
	consume := func() (bool, error) {
		data := strings.TrimSpace(event.String())
		event.Reset()
		if data == "" {
			return false, nil
		}
		if data == "[DONE]" {
			if !finished {
				return false, ErrProtocol
			}
			return true, nil
		}
		var chunk chatResponse
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return false, ErrInvalidJSON
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return false, ErrProtocol
		}
		if chunk.Object != "" && chunk.Object != "chat.completion.chunk" {
			return false, ErrUnsupportedProtocol
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if len(choice.Delta.Content) > 0 && string(choice.Delta.Content) != "null" {
				text, err := completionText(choice.Delta.Content)
				if err != nil && !errors.Is(err, ErrEmptyContent) {
					return false, err
				}
				if text != "" {
					if finished {
						return false, ErrProtocol
					}
					output.WriteString(text)
					if output.Len() > 4<<20 {
						return false, ErrProtocol
					}
					emit(false)
				}
			}
			if choice.Finish != "" {
				if err := finishReason(choice.Finish, true); err != nil {
					return false, err
				}
				finished = true
			}
		}
		return false, nil
	}
	for scanner.Scan() {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		line := scanner.Text()
		if firstLine {
			line = strings.TrimPrefix(line, "\ufeff")
			firstLine = false
		}
		if line == "" {
			done, err := consume()
			if err != nil {
				return "", err
			}
			if done {
				if strings.TrimSpace(output.String()) == "" {
					return "", ErrEmptyContent
				}
				emit(true)
				return output.String(), nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if event.Len() > 0 {
				event.WriteByte('\n')
			}
			event.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			if event.Len() > 1<<20 {
				return "", ErrProtocol
			}
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if limited.N == 0 {
		return "", ErrProtocol
	}
	if scanner.Err() != nil {
		return "", ErrNetwork
	}
	if _, err := consume(); err != nil {
		return "", err
	}
	if !finished {
		return "", ErrProtocol
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", ErrEmptyContent
	}
	emit(true)
	return output.String(), nil
}
