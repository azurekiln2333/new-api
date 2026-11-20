package controller

import (
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

// testPrompt overrides the synthetic channel test prompt. A model fingerprint
// probe needs its own instruction and a larger output budget than the fixed
// "hi" test request, so it passes this through the request context.
type testPrompt struct {
	Prompt    string
	MaxTokens uint
}

// channelTestPromptKey carries a testPrompt from the caller into the channel
// test, which builds its own gin context and cannot receive it directly.
const channelTestPromptKey = "channel_test_prompt"

// fingerprintReplyText reads the assistant text out of a non-stream reply. The
// relay has already validated the body, so a missing text field means the model
// answered in a shape this check cannot score.
func fingerprintReplyText(body []byte, endpointType string) (string, error) {
	parsed := gjson.ParseBytes(body)
	var text string
	switch endpointType {
	case "openai-response":
		if direct := parsed.Get("output_text"); direct.Type == gjson.String {
			text = direct.String()
		} else {
			var parts []string
			parsed.Get("output.#.content.#.text").ForEach(func(_, value gjson.Result) bool {
				value.ForEach(func(_, part gjson.Result) bool {
					if part.Type == gjson.String {
						parts = append(parts, part.String())
					}
					return true
				})
				return true
			})
			text = strings.Join(parts, "\n")
		}
	case "anthropic":
		var parts []string
		for _, block := range parsed.Get("content").Array() {
			if block.Get("type").String() == "text" && block.Get("text").Type == gjson.String {
				parts = append(parts, block.Get("text").String())
			}
		}
		text = strings.Join(parts, "\n")
	case "gemini":
		var parts []string
		for _, part := range parsed.Get("candidates.0.content.parts").Array() {
			if !part.Get("thought").Bool() && part.Get("text").Type == gjson.String {
				parts = append(parts, part.Get("text").String())
			}
		}
		text = strings.Join(parts, "\n")
	default:
		content := parsed.Get("choices.0.message.content")
		if content.Type == gjson.String {
			text = content.String()
		} else {
			var parts []string
			for _, part := range content.Array() {
				if part.Get("type").String() == "text" && part.Get("text").Type == gjson.String {
					parts = append(parts, part.Get("text").String())
				}
			}
			text = strings.Join(parts, "\n")
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("the reply contained no text to score")
	}
	return text, nil
}

// countChallengeNumbers reports how many integers of the challenge range the
// reply contains. It is a diagnostic shown next to the result; the scoring
// library parses and validates the replies itself.
func countChallengeNumbers(text string) int {
	count := 0
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return (r < '0' || r > '9') && r != '-'
	}) {
		number := strings.TrimSpace(field)
		if number == "" || number == "-" || strings.HasPrefix(number, "-") {
			continue
		}
		value := 0
		for _, digit := range number {
			if digit < '0' || digit > '9' {
				value = 0
				break
			}
			value = value*10 + int(digit-'0')
			if value > 355 {
				value = 0
				break
			}
		}
		if value >= 1 && value <= 355 {
			count++
		}
	}
	return count
}
