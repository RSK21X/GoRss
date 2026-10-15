package aiservice

import (
	"encoding/json"
	"strings"
)

// Decode only the known spans array. Unknown IDs and duplicate IDs cannot enter
// a preview. The final parser still validates the complete response independently.
func previewTranslations(raw string, batch []TranslationSpan) map[string]string {
	allowed := make(map[string]bool, len(batch))
	for _, span := range batch {
		allowed[span.ID] = true
	}
	values := map[string]string{}
	d := json.NewDecoder(strings.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return values
	}
	key, err := d.Token()
	if err != nil || key != "spans" {
		return values
	}
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return values
	}
	for d.More() {
		start := d.InputOffset()
		var span struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		if err := d.Decode(&span); err != nil {
			span.ID, span.Text = partialTranslationSpan(strings.TrimLeft(raw[start:], " \r\n\t,"))
			if allowed[span.ID] && values[span.ID] == "" && strings.TrimSpace(span.Text) != "" && len(span.Text) <= 16000 {
				values[span.ID] = span.Text
			}
			return values
		}
		if !allowed[span.ID] || values[span.ID] != "" || len(span.Text) > 16000 {
			return map[string]string{}
		}
		if strings.TrimSpace(span.Text) != "" {
			values[span.ID] = span.Text
		}
	}
	return values
}

func partialTranslationSpan(raw string) (id, text string) {
	d := json.NewDecoder(strings.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return "", ""
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return id, ""
		}
		start := d.InputOffset()
		var value string
		if err := d.Decode(&value); err != nil {
			if key != "text" || id == "" {
				return "", ""
			}
			prefix := strings.TrimLeft(raw[start:], " \r\n\t:")
			if !strings.HasPrefix(prefix, `"`) {
				return "", ""
			}
			// A delta may end inside an escape or Unicode surrogate pair.
			// Trim only the bounded escape tail, never repair arbitrary JSON.
			for trim := 0; trim <= 12 && len(prefix) > trim; trim++ {
				candidate := prefix[:len(prefix)-trim] + `"`
				if json.Unmarshal([]byte(candidate), &value) == nil && !strings.ContainsRune(value, '\uFFFD') {
					return id, value
				}
			}
			return "", ""
		}
		if key == "id" {
			id = value
		} else if key == "text" {
			text = value
		} else {
			return "", ""
		}
	}
	return id, text
}
