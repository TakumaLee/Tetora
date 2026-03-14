package main

import "tetora/internal/nlp"

// SentimentResult is an alias for nlp.SentimentResult for backward compatibility.
type SentimentResult = nlp.SentimentResult

// analyzeSentiment delegates to nlp.Analyze.
func analyzeSentiment(text string) SentimentResult {
	return nlp.Analyze(text)
}

// containsWord delegates to nlp.ContainsWord.
func containsWord(text, word string) bool {
	return nlp.ContainsWord(text, word)
}

// sentimentLabel delegates to nlp.Label.
func sentimentLabel(score float64) string {
	return nlp.Label(score)
}
