package search

import (
	"sync"
)

type Registry struct {
	providers map[string]SearchProvider
	mu        sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]SearchProvider),
	}
}

func (r *Registry) Register(p SearchProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

func (r *Registry) Get(name string) (SearchProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

func (r *Registry) List() []SearchProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []SearchProvider
	for _, p := range r.providers {
		list = append(list, p)
	}
	return list
}
