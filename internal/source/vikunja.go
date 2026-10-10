package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	// defaultVikunjaFilter is used when Filter is empty, restricting discovery
	// to open tasks only.
	defaultVikunjaFilter = "done = false"

	// maxVikunjaPerPage limits the number of tasks requested per page. Vikunja
	// instances commonly cap max_items_per_page at 50 (per the /info endpoint).
	maxVikunjaPerPage = 50

	// maxVikunjaPages limits the number of pages fetched from the Vikunja API.
	maxVikunjaPages = 10

	// maxVikunjaCommentBytes limits the total size of concatenated comments per task.
	maxVikunjaCommentBytes = 64 * 1024

	// vikunjaAPIErrorBodyLimit bounds how much of a non-200 response body is
	// captured for the error message.
	vikunjaAPIErrorBodyLimit = 1024

	// vikunjaTaskKind is the Kind reported for every discovered Vikunja work
	// item. Vikunja has no notion of issue types.
	vikunjaTaskKind = "Task"
)

// VikunjaSource discovers tasks from a Vikunja project.
type VikunjaSource struct {
	BaseURL   string
	ProjectID int64
	Filter    string
	Token     string
	Client    *http.Client
}

// vikunjaTask is the subset of the Vikunja task representation this source uses.
type vikunjaTask struct {
	ID          int64          `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Labels      []vikunjaLabel `json:"labels"`
}

type vikunjaLabel struct {
	Title string `json:"title"`
}

type vikunjaComment struct {
	Comment string `json:"comment"`
}

// vikunjaAPIError carries the HTTP status code returned by the Vikunja API so
// callers get an actionable error message.
type vikunjaAPIError struct {
	StatusCode int
	Body       string
}

func (e *vikunjaAPIError) Error() string {
	return fmt.Sprintf("Vikunja API returned status %d: %s", e.StatusCode, e.Body)
}

func (s *VikunjaSource) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

// Discover fetches tasks from Vikunja and returns them as WorkItems.
func (s *VikunjaSource) Discover(ctx context.Context) ([]WorkItem, error) {
	tasks, err := s.fetchAllTasks(ctx)
	if err != nil {
		return nil, err
	}

	items := make([]WorkItem, 0, len(tasks))
	for _, task := range tasks {
		comments, err := s.fetchComments(ctx, task.ID)
		if err != nil {
			return nil, err
		}

		items = append(items, WorkItem{
			ID:       strconv.FormatInt(task.ID, 10),
			Number:   int(task.ID),
			Title:    task.Title,
			Body:     task.Description,
			URL:      fmt.Sprintf("%s/tasks/%d", strings.TrimRight(s.BaseURL, "/"), task.ID),
			Labels:   labelTitles(task.Labels),
			Comments: comments,
			Kind:     vikunjaTaskKind,
		})
	}

	return items, nil
}

func labelTitles(labels []vikunjaLabel) []string {
	if len(labels) == 0 {
		return nil
	}
	titles := make([]string, len(labels))
	for i, l := range labels {
		titles[i] = l.Title
	}
	return titles
}

// effectiveFilter returns the filter query used to list tasks: the
// user-provided Filter as-is, or defaultVikunjaFilter when empty.
func (s *VikunjaSource) effectiveFilter() string {
	if s.Filter != "" {
		return s.Filter
	}
	return defaultVikunjaFilter
}

func (s *VikunjaSource) fetchAllTasks(ctx context.Context) ([]vikunjaTask, error) {
	var allTasks []vikunjaTask

	for page := 1; page <= maxVikunjaPages; page++ {
		tasks, totalPages, err := s.fetchTasksPage(ctx, page)
		if err != nil {
			return nil, err
		}
		allTasks = append(allTasks, tasks...)

		if page >= totalPages {
			break
		}
	}

	return allTasks, nil
}

// fetchTasksPage lists one page of project tasks via the view-less
// /projects/{id}/tasks route rather than /projects/{id}/views/{view}/tasks,
// so that a view's own filter is never combined with Filter.
func (s *VikunjaSource) fetchTasksPage(ctx context.Context, page int) ([]vikunjaTask, int, error) {
	u, err := url.Parse(strings.TrimRight(s.BaseURL, "/") + fmt.Sprintf("/api/v1/projects/%d/tasks", s.ProjectID))
	if err != nil {
		return nil, 0, fmt.Errorf("parsing base URL: %w", err)
	}

	params := url.Values{}
	params.Set("filter", s.effectiveFilter())
	params.Set("page", strconv.Itoa(page))
	params.Set("per_page", strconv.Itoa(maxVikunjaPerPage))
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}
	s.setAuthHeaders(req)

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetching tasks: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, vikunjaAPIErrorBodyLimit))
		return nil, 0, &vikunjaAPIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var tasks []vikunjaTask
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, 0, fmt.Errorf("decoding response: %w", err)
	}

	totalPages := 1
	if raw := resp.Header.Get("x-pagination-total-pages"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			totalPages = n
		}
	}

	return tasks, totalPages, nil
}

// fetchComments concatenates comment bodies for a task, capped at
// maxVikunjaCommentBytes. Comment bodies are HTML, passed through unchanged.
func (s *VikunjaSource) fetchComments(ctx context.Context, taskID int64) (string, error) {
	u := strings.TrimRight(s.BaseURL, "/") + fmt.Sprintf("/api/v1/tasks/%d/comments", taskID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	s.setAuthHeaders(req)

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching comments for task %d: %w", taskID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, vikunjaAPIErrorBodyLimit))
		return "", &vikunjaAPIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var comments []vikunjaComment
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return "", fmt.Errorf("decoding comments response: %w", err)
	}

	var parts []string
	totalBytes := 0
	for _, c := range comments {
		if c.Comment == "" {
			continue
		}
		totalBytes += len(c.Comment)
		if totalBytes > maxVikunjaCommentBytes {
			break
		}
		parts = append(parts, c.Comment)
	}

	return strings.Join(parts, "\n---\n"), nil
}

func (s *VikunjaSource) setAuthHeaders(req *http.Request) {
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	req.Header.Set("Accept", "application/json")
}
