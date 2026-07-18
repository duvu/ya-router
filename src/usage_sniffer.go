// usage_sniffer.go extracts exact upstream-reported token usage from a
// response body without altering the response written to the real client.
// It never estimates tokens: if no "usage" object is found (or the body
// exceeds the bounded scan size), usage is reported as unavailable rather
// than guessed.
package yarouter

import (
	"bytes"
	"encoding/json"
	"net/http"

	telemetrypkg "github.com/duvu/ya-router/internal/telemetry"
)

// usageSniffMaxBytes bounds how much of a response body is retained for
// usage extraction. Exceeding it gives up on sniffing that request's usage
// (reported as unavailable) rather than growing memory unboundedly for large
// or long-lived streaming responses.
const usageSniffMaxBytes = 1 << 20 // 1 MiB

// usageSniffWriter transparently tees every write to the wrapped
// http.ResponseWriter into a bounded scan buffer. It never changes what is
// written to the real client and never returns an error the caller didn't
// already get from the wrapped writer, so a bug in usage extraction cannot
// alter the HTTP result.
type usageSniffWriter struct {
	http.ResponseWriter
	buf    bytes.Buffer
	capped bool
}

func newUsageSniffWriter(w http.ResponseWriter) *usageSniffWriter {
	return &usageSniffWriter{ResponseWriter: w}
}

func (s *usageSniffWriter) Write(p []byte) (int, error) {
	n, err := s.ResponseWriter.Write(p)
	if !s.capped {
		if s.buf.Len()+len(p) > usageSniffMaxBytes {
			s.capped = true
			s.buf.Reset()
		} else {
			s.buf.Write(p)
		}
	}
	return n, err
}

func (s *usageSniffWriter) Flush() {
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Usage returns the last "usage" object found in the scanned bytes, or nil
// when none was found (capped, absent, or malformed).
func (s *usageSniffWriter) Usage() *telemetrypkg.Usage {
	if s.capped {
		return nil
	}
	return sniffUsageJSON(s.buf.Bytes())
}

// sniffUsageJSON scans body for the last well-formed OpenAI/Anthropic/
// Responses-style "usage" object and returns it as exact token counts.
// Recognized shapes:
//
//	{"usage": {"prompt_tokens": N, "completion_tokens": N, "total_tokens": N}}
//	{"usage": {"input_tokens": N, "output_tokens": N, "total_tokens": N}}
//
// A body containing multiple "usage" occurrences (e.g. an SSE stream with
// several chunks) keeps the last successfully parsed one, matching upstream
// convention of reporting cumulative usage in the final chunk.
func sniffUsageJSON(body []byte) *telemetrypkg.Usage {
	const key = `"usage"`
	var found *telemetrypkg.Usage
	rest := body
	for {
		index := bytes.Index(rest, []byte(key))
		if index < 0 {
			return found
		}
		afterKey := rest[index+len(key):]
		objectStart := bytes.IndexByte(afterKey, '{')
		if objectStart < 0 {
			return found
		}
		fragment := extractBalancedJSONObject(afterKey[objectStart:])
		if fragment != nil {
			if usage, ok := parseUsageFragment(fragment); ok {
				found = usage
			}
		}
		advance := index + len(key) + objectStart + 1
		if advance <= 0 || advance > len(rest) {
			return found
		}
		rest = rest[advance:]
	}
}

// extractBalancedJSONObject returns the shortest brace-balanced prefix of
// data starting at '{', or nil if data does not contain one within a bounded
// scan window.
func extractBalancedJSONObject(data []byte) []byte {
	const maxObjectScan = 8192
	if len(data) == 0 || data[0] != '{' {
		return nil
	}
	depth := 0
	inString := false
	escaped := false
	limit := len(data)
	if limit > maxObjectScan {
		limit = maxObjectScan
	}
	for i := 0; i < limit; i++ {
		c := data[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// inside a string literal; braces don't count
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return data[:i+1]
			}
		}
	}
	return nil
}

func parseUsageFragment(fragment []byte) (*telemetrypkg.Usage, bool) {
	var openAIShape struct {
		PromptTokens     *int `json:"prompt_tokens"`
		CompletionTokens *int `json:"completion_tokens"`
		TotalTokens      *int `json:"total_tokens"`
	}
	var responsesShape struct {
		InputTokens  *int `json:"input_tokens"`
		OutputTokens *int `json:"output_tokens"`
		TotalTokens  *int `json:"total_tokens"`
	}
	if json.Unmarshal(fragment, &openAIShape) == nil &&
		(openAIShape.PromptTokens != nil || openAIShape.CompletionTokens != nil) {
		usage := &telemetrypkg.Usage{}
		if openAIShape.PromptTokens != nil {
			usage.InputTokens = *openAIShape.PromptTokens
		}
		if openAIShape.CompletionTokens != nil {
			usage.OutputTokens = *openAIShape.CompletionTokens
		}
		if openAIShape.TotalTokens != nil {
			usage.TotalTokens = *openAIShape.TotalTokens
		} else {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		return usage, true
	}
	if json.Unmarshal(fragment, &responsesShape) == nil &&
		(responsesShape.InputTokens != nil || responsesShape.OutputTokens != nil) {
		usage := &telemetrypkg.Usage{}
		if responsesShape.InputTokens != nil {
			usage.InputTokens = *responsesShape.InputTokens
		}
		if responsesShape.OutputTokens != nil {
			usage.OutputTokens = *responsesShape.OutputTokens
		}
		if responsesShape.TotalTokens != nil {
			usage.TotalTokens = *responsesShape.TotalTokens
		} else {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		return usage, true
	}
	return nil, false
}
