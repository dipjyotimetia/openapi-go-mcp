// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// todoStore is intentionally minimal: its job is to give the MCP proxy a real
// HTTP backend to talk to, not to be a reference service.
type todoStore struct {
	mu     sync.RWMutex
	nextID int64
	items  map[int64]storedTodo
}

type storedTodo struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Completed bool      `json:"completed"`
	CreatedAt time.Time `json:"createdAt"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newTodoStore() *todoStore {
	s := &todoStore{items: map[int64]storedTodo{}}
	s.create("Read the openapi-go-mcp README", false)
	s.create("Try the todos example end-to-end", false)
	s.create("Ship something cool", true)
	return s
}

func (s *todoStore) create(title string, completed bool) storedTodo {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	t := storedTodo{
		ID:        s.nextID,
		Title:     title,
		Completed: completed,
		CreatedAt: time.Now().UTC(),
	}
	s.items[t.ID] = t
	return t
}

func (s *todoStore) register(mux *http.ServeMux) {
	mux.HandleFunc("/todos", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleList(w, r)
		case http.MethodPost:
			s.handleCreate(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		}
	})

	mux.HandleFunc("/todos/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/todos/")
		if idStr == "" || strings.Contains(idStr, "/") {
			writeError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
			return
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "id must be an integer")
			return
		}
		switch r.Method {
		case http.MethodGet:
			s.handleGet(w, id)
		case http.MethodPut:
			s.handleUpdate(w, r, id)
		case http.MethodDelete:
			s.handleDelete(w, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		}
	})
}

func (s *todoStore) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var (
		filterCompleted *bool
		limit           = 100
	)
	if v := q.Get("completed"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "completed must be a boolean")
			return
		}
		filterCompleted = &b
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 100")
			return
		}
		limit = n
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]storedTodo, 0, len(s.items))
	for _, t := range s.items {
		if filterCompleted != nil && t.Completed != *filterCompleted {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *todoStore) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     string `json:"title"`
		Completed *bool  `json:"completed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	completed := false
	if body.Completed != nil {
		completed = *body.Completed
	}
	t := s.create(body.Title, completed)
	writeJSON(w, http.StatusCreated, t)
}

func (s *todoStore) handleGet(w http.ResponseWriter, id int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.items[id]
	if !ok {
		writeError(w, http.StatusNotFound, "todo not found")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *todoStore) handleUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	var body struct {
		Title     *string `json:"title"`
		Completed *bool   `json:"completed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.items[id]
	if !ok {
		writeError(w, http.StatusNotFound, "todo not found")
		return
	}
	if body.Title != nil {
		if strings.TrimSpace(*body.Title) == "" {
			writeError(w, http.StatusBadRequest, "title must be non-empty")
			return
		}
		t.Title = *body.Title
	}
	if body.Completed != nil {
		t.Completed = *body.Completed
	}
	s.items[id] = t
	writeJSON(w, http.StatusOK, t)
}

func (s *todoStore) handleDelete(w http.ResponseWriter, id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		writeError(w, http.StatusNotFound, "todo not found")
		return
	}
	delete(s.items, id)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Code: status, Message: msg})
}
