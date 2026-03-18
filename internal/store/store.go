package store

import (
	"encoding/json"
	"sort"
	"strings"
)

// WorkflowInfo is the subset of workflow data needed by Browse.
type WorkflowInfo struct {
	Name        string
	Description string
	StepCount   int
}

// TemplateInfo is the subset of template data needed by Browse.
type TemplateInfo struct {
	Name        string
	Description string
	Category    string
	StepCount   int
	Variables   []string
}

// Deps holds the data-access functions Browse depends on.
type Deps struct {
	ListWorkflows func() ([]WorkflowInfo, error)
	ListTemplates func() []TemplateInfo
}

// Item represents a template in the store browse view.
type Item struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	StepCount   int      `json:"stepCount"`
	Variables   []string `json:"variables,omitempty"`
	Source      string   `json:"source"` // "builtin" | "installed" | "registry"
	Installed   bool     `json:"installed"`
}

// Category groups items by category.
type Category struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Browse returns all available templates (local built-in + installed), grouped and categorized.
func Browse(deps Deps) ([]Item, []Category) {
	var items []Item
	categoryCount := map[string]int{}

	// 1. Built-in templates from embedded gallery.
	installedNames := map[string]bool{}
	if wfs, _ := deps.ListWorkflows(); wfs != nil {
		for _, wf := range wfs {
			installedNames[wf.Name] = true
		}
	}

	for _, t := range deps.ListTemplates() {
		name := strings.TrimPrefix(t.Name, "tpl-")
		tags := DeriveTags(t.Name, t.Description, t.Category)
		items = append(items, Item{
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
	if wfs, _ := deps.ListWorkflows(); wfs != nil {
		for _, wf := range wfs {
			// Skip if already listed as a builtin template.
			alreadyListed := false
			for _, item := range items {
				tplName := strings.TrimPrefix(item.Name, "tpl-")
				if tplName == wf.Name || item.Name == wf.Name {
					alreadyListed = true
					break
				}
			}
			if alreadyListed {
				continue
			}
			cat := DeriveCategory(wf.Name)
			tags := DeriveTags(wf.Name, wf.Description, cat)
			items = append(items, Item{
				Name:        wf.Name,
				Description: wf.Description,
				Category:    cat,
				Tags:        tags,
				StepCount:   wf.StepCount,
				Source:      "installed",
				Installed:   true,
			})
			categoryCount[cat]++
		}
	}

	// Build sorted category list.
	var cats []Category
	for name, count := range categoryCount {
		if name == "" {
			name = "other"
		}
		cats = append(cats, Category{Name: name, Count: count})
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i].Count > cats[j].Count })

	return items, cats
}

// DeriveCategory extracts category from workflow name.
func DeriveCategory(name string) string {
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

// DeriveTags generates tags from name, description, and category.
func DeriveTags(name, desc, category string) []string {
	tags := []string{}
	if category != "" {
		tags = append(tags, category)
	}
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

// ItemsToJSON serialises items and categories for the HTTP handler.
func ItemsToJSON(items []Item, cats []Category) ([]byte, error) {
	resp := map[string]any{
		"items":      items,
		"categories": cats,
		"total":      len(items),
	}
	return json.Marshal(resp)
}
