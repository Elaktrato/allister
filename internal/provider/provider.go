// Package provider defines the types shared with metadata providers.
package provider

// Title is one title of an anime in a single language tag (ja, ja-latn, en, ...).
type Title struct{ Lang, Text string }
