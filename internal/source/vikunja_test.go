package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVikunjaDiscover(t *testing.T) {
	var gotAuth, gotPath, gotFilter string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/projects/49/tasks":
			gotAuth = r.Header.Get("Authorization")
			gotPath = r.URL.Path
			gotFilter = r.URL.Query().Get("filter")
			w.Header().Set("x-pagination-total-pages", "1")
			json.NewEncoder(w).Encode([]vikunjaTask{
				{
					ID:          1,
					Title:       "Fix login bug",
					Description: "<p>Steps to reproduce</p>",
					Labels:      []vikunjaLabel{{Title: "bug"}, {Title: "critical"}},
				},
				{
					ID:    2,
					Title: "Add feature",
				},
			})
		case r.URL.Path == "/api/v1/tasks/1/comments":
			json.NewEncoder(w).Encode([]vikunjaComment{
				{Comment: "First comment"},
				{Comment: "Second comment"},
			})
		case r.URL.Path == "/api/v1/tasks/2/comments":
			json.NewEncoder(w).Encode([]vikunjaComment{})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	s := &VikunjaSource{
		BaseURL:   server.URL,
		ProjectID: 49,
		Token:     "test-token",
	}

	items, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuth != "Bearer test-token" {
		t.Errorf("expected Bearer auth header, got %q", gotAuth)
	}
	if gotPath != "/api/v1/projects/49/tasks" {
		t.Errorf("expected project-scoped tasks path, got %q", gotPath)
	}
	if gotFilter != "done = false" {
		t.Errorf("expected default filter %q, got %q", "done = false", gotFilter)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].ID != "1" {
		t.Errorf("expected ID %q, got %q", "1", items[0].ID)
	}
	if items[0].Number != 1 {
		t.Errorf("expected Number 1, got %d", items[0].Number)
	}
	if items[0].Title != "Fix login bug" {
		t.Errorf("expected Title %q, got %q", "Fix login bug", items[0].Title)
	}
	if items[0].Body != "<p>Steps to reproduce</p>" {
		t.Errorf("expected Body to be the raw HTML description, got %q", items[0].Body)
	}
	if !strings.HasSuffix(items[0].URL, "/tasks/1") {
		t.Errorf("expected URL to end with /tasks/1, got %q", items[0].URL)
	}
	if len(items[0].Labels) != 2 || items[0].Labels[0] != "bug" || items[0].Labels[1] != "critical" {
		t.Errorf("unexpected labels: %v", items[0].Labels)
	}
	if items[0].Kind != "Task" {
		t.Errorf("expected Kind %q, got %q", "Task", items[0].Kind)
	}
	expectedComments := "First comment\n---\nSecond comment"
	if items[0].Comments != expectedComments {
		t.Errorf("expected comments %q, got %q", expectedComments, items[0].Comments)
	}

	if items[1].ID != "2" {
		t.Errorf("expected ID %q, got %q", "2", items[1].ID)
	}
	if len(items[1].Labels) != 0 {
		t.Errorf("expected no labels, got %v", items[1].Labels)
	}
	if items[1].Comments != "" {
		t.Errorf("expected no comments, got %q", items[1].Comments)
	}
}

func TestVikunjaDiscoverCustomFilter(t *testing.T) {
	var gotFilter string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/projects/7/tasks" {
			gotFilter = r.URL.Query().Get("filter")
			w.Header().Set("x-pagination-total-pages", "1")
			json.NewEncoder(w).Encode([]vikunjaTask{})
			return
		}
		t.Fatalf("unexpected request path: %s", r.URL.Path)
	}))
	defer server.Close()

	s := &VikunjaSource{
		BaseURL:   server.URL,
		ProjectID: 7,
		Filter:    "done = false && labels in 5",
	}

	_, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotFilter != "done = false && labels in 5" {
		t.Errorf("expected custom filter to be used as-is, got %q", gotFilter)
	}
}

func TestVikunjaDiscoverPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/1/tasks":
			page := r.URL.Query().Get("page")
			w.Header().Set("x-pagination-total-pages", "2")
			if page == "1" {
				json.NewEncoder(w).Encode([]vikunjaTask{{ID: 1, Title: "Task 1"}})
				return
			}
			json.NewEncoder(w).Encode([]vikunjaTask{{ID: 2, Title: "Task 2"}})
		case "/api/v1/tasks/1/comments", "/api/v1/tasks/2/comments":
			json.NewEncoder(w).Encode([]vikunjaComment{})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	s := &VikunjaSource{
		BaseURL:   server.URL,
		ProjectID: 1,
	}

	items, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != "1" || items[1].ID != "2" {
		t.Errorf("unexpected items: %+v", items)
	}
}

func TestVikunjaDiscoverCommentCap(t *testing.T) {
	longComment := strings.Repeat("a", maxVikunjaCommentBytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/1/tasks":
			w.Header().Set("x-pagination-total-pages", "1")
			json.NewEncoder(w).Encode([]vikunjaTask{{ID: 1, Title: "Big task"}})
		case "/api/v1/tasks/1/comments":
			json.NewEncoder(w).Encode([]vikunjaComment{
				{Comment: longComment},
				{Comment: "this one should be dropped"},
			})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	s := &VikunjaSource{
		BaseURL:   server.URL,
		ProjectID: 1,
	}

	items, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Comments != longComment {
		t.Errorf("expected comments capped to the first entry only, got length %d", len(items[0].Comments))
	}
}

func TestVikunjaDiscoverAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"unauthorized"}`))
	}))
	defer server.Close()

	s := &VikunjaSource{
		BaseURL:   server.URL,
		ProjectID: 1,
	}

	_, err := s.Discover(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected error to contain status 401, got %v", err)
	}
}

func TestVikunjaDiscoverNoAuthHeaderWithoutToken(t *testing.T) {
	var gotAuth string
	gotAuthSet := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/1/tasks":
			gotAuth = r.Header.Get("Authorization")
			gotAuthSet = true
			w.Header().Set("x-pagination-total-pages", "1")
			json.NewEncoder(w).Encode([]vikunjaTask{})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	s := &VikunjaSource{
		BaseURL:   server.URL,
		ProjectID: 1,
	}

	if _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gotAuthSet {
		t.Fatal("expected the tasks endpoint to be requested")
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header without a token, got %q", gotAuth)
	}
}

func TestVikunjaDiscoverCommentsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/1/tasks":
			w.Header().Set("x-pagination-total-pages", "1")
			json.NewEncoder(w).Encode([]vikunjaTask{{ID: 1, Title: "Task 1"}})
		case "/api/v1/tasks/1/comments":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("boom"))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	s := &VikunjaSource{
		BaseURL:   server.URL,
		ProjectID: 1,
	}

	_, err := s.Discover(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain status 500, got %v", err)
	}
}

func TestVikunjaDiscoverMaxPagesBound(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/1/tasks" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		requestCount++
		page := r.URL.Query().Get("page")
		w.Header().Set("x-pagination-total-pages", "1000")
		json.NewEncoder(w).Encode([]vikunjaTask{{ID: int64(requestCount), Title: fmt.Sprintf("Task on page %s", page)}})
	}))
	defer server.Close()

	s := &VikunjaSource{
		BaseURL:   server.URL,
		ProjectID: 1,
		Client:    server.Client(),
	}

	// Comments requests will hit the same handler and be treated as task
	// pages with an unexpected path, so stub a client-side round tripper
	// instead of relying on the shared test server for comments.
	items, err := s.fetchAllTasks(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if requestCount != maxVikunjaPages {
		t.Errorf("expected %d page requests, got %d", maxVikunjaPages, requestCount)
	}
	if len(items) != maxVikunjaPages {
		t.Errorf("expected %d tasks, got %d", maxVikunjaPages, len(items))
	}
}
