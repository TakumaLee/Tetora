package main

import "tetora/internal/i18n"

// initStrings is a type alias so existing callers in this package need no changes.
type initStrings = i18n.InitStrings

// initTranslations exposes the canonical translation map for direct map access
// used in cli_init.go.
var initTranslations = i18n.Translations
