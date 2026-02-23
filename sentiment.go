package main

import (
	"math"
	"strings"
	"unicode/utf8"
)

// --- P23.1: Rule-Based Sentiment Analysis ---

// SentimentResult holds the output of sentiment analysis.
type SentimentResult struct {
	Score    float64  `json:"score"`    // -1.0 to 1.0
	Keywords []string `json:"keywords"` // matched keywords
}

// --- Keyword Dictionaries ---

var positiveKeywordsEN = []string{
	"happy", "great", "love", "awesome", "thanks", "thank you",
	"perfect", "excellent", "wonderful", "amazing",
}

var negativeKeywordsEN = []string{
	"sad", "angry", "hate", "terrible", "awful", "frustrated",
	"annoyed", "disappointed", "worst", "bad",
}

var positiveKeywordsJP = []string{
	"嬉しい", "楽しい", "ありがとう", "素晴らしい", "最高", "良い", "好き", "幸せ",
}

var negativeKeywordsJP = []string{
	"悲しい", "怒り", "辛い", "最悪", "嫌い", "困った", "疲れた", "ダメ",
}

var positiveKeywordsCN = []string{
	"开心", "高兴", "谢谢", "太好了", "喜欢", "棒", "优秀",
}

var negativeKeywordsCN = []string{
	"难过", "生气", "讨厌", "糟糕", "烦", "累", "差",
}

// Emoji sentiment (each emoji string is a single grapheme cluster).
var positiveEmojis = []string{
	"\U0001F60A", // 😊
	"\U0001F604", // 😄
	"\U0001F389", // 🎉
	"\u2764\uFE0F", // ❤️
	"\U0001F44D", // 👍
	"\U0001F64F", // 🙏
	"\u2728",     // ✨
	"\U0001F4AA", // 💪
	"\U0001F525", // 🔥
}

var negativeEmojis = []string{
	"\U0001F622", // 😢
	"\U0001F621", // 😡
	"\U0001F624", // 😤
	"\U0001F494", // 💔
	"\U0001F44E", // 👎
	"\U0001F61E", // 😞
	"\U0001F62B", // 😫
	"\U0001F62D", // 😭
	"\U0001F926", // 🤦
}

// analyzeSentiment performs rule-based sentiment detection on the input text.
// It checks for English, Japanese, Chinese keywords and emoji patterns.
// Returns a SentimentResult with a score clamped to [-1, 1] and matched keywords.
func analyzeSentiment(text string) SentimentResult {
	if text == "" {
		return SentimentResult{Score: 0, Keywords: nil}
	}

	lower := strings.ToLower(text)
	var posCount, negCount int
	var matched []string

	// English keywords (case-insensitive word boundary check).
	for _, kw := range positiveKeywordsEN {
		if containsWord(lower, kw) {
			posCount++
			matched = append(matched, "+"+kw)
		}
	}
	for _, kw := range negativeKeywordsEN {
		if containsWord(lower, kw) {
			negCount++
			matched = append(matched, "-"+kw)
		}
	}

	// Japanese keywords (substring match -- no word boundaries in JP).
	for _, kw := range positiveKeywordsJP {
		if strings.Contains(text, kw) {
			posCount++
			matched = append(matched, "+"+kw)
		}
	}
	for _, kw := range negativeKeywordsJP {
		if strings.Contains(text, kw) {
			negCount++
			matched = append(matched, "-"+kw)
		}
	}

	// Chinese keywords (substring match).
	for _, kw := range positiveKeywordsCN {
		if strings.Contains(text, kw) {
			posCount++
			matched = append(matched, "+"+kw)
		}
	}
	for _, kw := range negativeKeywordsCN {
		if strings.Contains(text, kw) {
			negCount++
			matched = append(matched, "-"+kw)
		}
	}

	// Emoji detection.
	for _, e := range positiveEmojis {
		if strings.Contains(text, e) {
			posCount++
			matched = append(matched, "+emoji")
		}
	}
	for _, e := range negativeEmojis {
		if strings.Contains(text, e) {
			negCount++
			matched = append(matched, "-emoji")
		}
	}

	total := posCount + negCount
	if total == 0 {
		return SentimentResult{Score: 0, Keywords: nil}
	}

	score := float64(posCount-negCount) / float64(max(1, total))
	score = math.Max(-1.0, math.Min(1.0, score))

	return SentimentResult{
		Score:    score,
		Keywords: matched,
	}
}

// containsWord checks if text contains the word with basic word-boundary logic.
// For ASCII text, it checks that the character before and after the match is not a letter.
func containsWord(text, word string) bool {
	idx := 0
	for {
		pos := strings.Index(text[idx:], word)
		if pos < 0 {
			return false
		}
		absPos := idx + pos
		endPos := absPos + len(word)

		// Check boundary before.
		beforeOK := absPos == 0 || !isLetterByte(text[absPos-1])
		// Check boundary after.
		afterOK := endPos >= len(text) || !isLetterByte(text[endPos])

		if beforeOK && afterOK {
			return true
		}
		// Move past this match.
		idx = absPos + utf8.RuneLen(rune(text[absPos]))
		if idx >= len(text) {
			return false
		}
	}
}

// isLetterByte returns true if the byte is an ASCII letter.
func isLetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// sentimentLabel converts a sentiment score to a human-readable label.
func sentimentLabel(score float64) string {
	switch {
	case score >= 0.5:
		return "positive"
	case score <= -0.5:
		return "negative"
	case score > 0.1:
		return "slightly positive"
	case score < -0.1:
		return "slightly negative"
	default:
		return "neutral"
	}
}
