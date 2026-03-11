package main

import (
	"encoding/json"
	"sort"
	"strings"
)

// StoreConfig configures the template marketplace / store.
type StoreConfig struct {
	Enabled     bool   `json:"enabled,omitempty"`
	RegistryURL string `json:"registryUrl,omitempty"` // default: https://registry.tetora.dev/v1
	AuthToken   string `json:"authToken,omitempty"`    // auth token (resolved from $ENV_VAR)
}

// --- Store Browse API (local-first, remote-ready) ---

// StoreItem represents a template in the store browse view.
type StoreItem struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	StepCount   int      `json:"stepCount"`
	Variables   []string `json:"variables,omitempty"`
	Source      string   `json:"source"` // "builtin" | "installed" | "registry"
	Installed   bool     `json:"installed"`
}

// StoreCategory groups items by category.
type StoreCategory struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// storeBrowse returns all available templates (local built-in + installed), grouped and categorized.
func storeBrowse(cfg *Config) ([]StoreItem, []StoreCategory) {
	var items []StoreItem
	categoryCount := map[string]int{}

	// 1. Built-in templates from embedded gallery.
	installedNames := map[string]bool{}
	if wfs, _ := listWorkflows(cfg); wfs != nil {
		for _, wf := range wfs {
			installedNames[wf.Name] = true
		}
	}

	for _, t := range listTemplates() {
		name := strings.TrimPrefix(t.Name, "tpl-")
		tags := deriveTags(t.Name, t.Description, t.Category)
		items = append(items, StoreItem{
			Name:        t.Name,
			Description: t.Description,
			Category:    t.Category,
			Tags:        tags,
			StepCount:   t.StepCount,
			Variables:   t.Variables,
			Source:      "builtin",
			Installed:   installedNames[name] || installedNames[t.Name],
		})
		categoryCount[t.Category]++
	}

	// 2. Installed workflows not from templates.
	if wfs, _ := listWorkflows(cfg); wfs != nil {
		for _, wf := range wfs {
			// Skip if already listed as a builtin template.
			alreadyListed := false
			for _, item := range items {
				name := strings.TrimPrefix(item.Name, "tpl-")
				if name == wf.Name || item.Name == wf.Name {
					alreadyListed = true
					break
				}
			}
			if alreadyListed {
				continue
			}
			cat := deriveCategory(wf.Name)
			tags := deriveTags(wf.Name, wf.Description, cat)
			items = append(items, StoreItem{
				Name:        wf.Name,
				Description: wf.Description,
				Category:    cat,
				Tags:        tags,
				StepCount:   len(wf.Steps),
				Source:      "installed",
				Installed:   true,
			})
			categoryCount[cat]++
		}
	}

	// Build sorted category list.
	var cats []StoreCategory
	for name, count := range categoryCount {
		if name == "" {
			name = "other"
		}
		cats = append(cats, StoreCategory{Name: name, Count: count})
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i].Count > cats[j].Count })

	return items, cats
}

// deriveCategory extracts category from workflow name.
func deriveCategory(name string) string {
	// Map common prefixes to categories.
	prefixMap := map[string]string{
		"standard-dev": "dev", "cicd": "devops", "content": "marketing",
		"resume": "hr", "employee": "hr", "performance": "hr",
		"invoice": "finance", "loan": "finance", "fund": "finance",
		"order": "commerce", "churn": "commerce", "sales": "commerce", "retail": "commerce",
		"insurance": "insurance", "healthcare": "healthcare", "pharma": "healthcare",
		"contract": "legal", "gov": "government", "grant": "nonprofit",
		"real-estate": "realestate", "property": "realestate",
		"freight": "logistics", "customs": "logistics",
		"hotel": "hospitality", "restaurant": "hospitality",
		"site-safety": "construction", "manufacturing": "manufacturing",
		"utility": "energy", "media": "media", "consulting": "consulting",
		"vendor": "procurement", "it-": "it", "admissions": "education",
		"ocr": "automation", "stripe": "payments", "support": "support",
		"workflow": "general",
	}
	lower := strings.ToLower(name)
	for prefix, cat := range prefixMap {
		if strings.Contains(lower, prefix) {
			return cat
		}
	}
	return "other"
}

// deriveTags generates tags from name, description, and category.
func deriveTags(name, desc, category string) []string {
	tags := []string{}
	if category != "" {
		tags = append(tags, category)
	}
	// Extract keywords from name.
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for _, p := range parts {
		p = strings.ToLower(p)
		if len(p) > 2 && p != "tpl" && p != "workflow" {
			tags = append(tags, p)
		}
	}
	// Deduplicate.
	seen := map[string]bool{}
	var unique []string
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	return unique
}

// storeItemsToJSON is a helper for the HTTP handler.
func storeItemsToJSON(items []StoreItem, cats []StoreCategory) ([]byte, error) {
	resp := map[string]any{
		"items":      items,
		"categories": cats,
		"total":      len(items),
	}
	return json.Marshal(resp)
}
