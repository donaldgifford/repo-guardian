package ghapitest

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) getApp(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeJSON(w, http.StatusUnauthorized, map[string]any{keyMessage: "A JSON web token could not be decoded"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": s.AppID, "slug": s.Slug})
}

func (s *Server) listInstallations(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	items := make([]map[string]any, 0, len(s.Installations))
	for _, in := range s.Installations {
		items = append(items, map[string]any{"id": in.ID, "account": map[string]any{keyLogin: in.Account}})
	}
	s.mu.Unlock()
	paginate(w, r, items, asIs)
}

func (*Server) createToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeJSON(w, http.StatusUnauthorized, map[string]any{keyMessage: "bad credentials"})
		return
	}
	writeJSON(
		w,
		http.StatusCreated,
		map[string]any{"token": InstallationToken(id), "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
	)
}

func (s *Server) installationRepos(w http.ResponseWriter, r *http.Request) {
	id, ok := installationFor(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{keyMessage: "installation token required"})
		return
	}
	s.mu.Lock()
	var account string
	for _, in := range s.Installations {
		if in.ID == id {
			account = in.Account
		}
	}
	items := s.reposOf(account)
	s.mu.Unlock()
	paginate(w, r, items, func(page []map[string]any) any {
		return map[string]any{"total_count": len(items), "repositories": page}
	})
}

func (s *Server) orgRepos(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	items := s.reposOf(r.PathValue("org"))
	s.mu.Unlock()
	paginate(w, r, items, asIs)
}

func (s *Server) reposOf(owner string) []map[string]any {
	var items []map[string]any
	for _, rp := range s.Repos {
		if strings.EqualFold(rp.Owner, owner) {
			items = append(items, map[string]any{"name": rp.Name, "full_name": rp.Owner + "/" + rp.Name, "owner": map[string]any{keyLogin: rp.Owner}})
		}
	}
	return items
}

// search understands the qualifiers rgctl sends: is:pr is:open org:
// author:app/<slug> head:<prefix>. head: is a prefix match, as on GitHub.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var org, author, head string
	for _, f := range strings.Fields(r.URL.Query().Get("q")) {
		k, v, _ := strings.Cut(f, ":")
		switch k {
		case "org":
			org = v
		case "author":
			author = strings.TrimPrefix(v, "app/") + "[bot]"
		case "head":
			head = v
		}
	}
	s.mu.Lock()
	var items []map[string]any
	for _, pr := range s.prs {
		if !strings.EqualFold(pr.Owner, org) || !strings.EqualFold(pr.Author, author) || !strings.HasPrefix(pr.HeadRef, head) {
			continue
		}
		if pr.State != stateOpen && !pr.StaleInIndex {
			continue
		}
		items = append(items, map[string]any{
			"number":         pr.Number,
			"repository_url": fmt.Sprintf("http://%s%s/repos/%s/%s", r.Host, APIPrefix, pr.Owner, pr.Repo),
			"pull_request":   map[string]any{},
		})
	}
	total := len(items)
	if s.SearchTotal != 0 {
		total = s.SearchTotal
	}
	incomplete := s.SearchIncomplete
	s.mu.Unlock()
	paginate(w, r, items, func(page []map[string]any) any {
		return map[string]any{"total_count": total, "incomplete_results": incomplete, "items": page}
	})
}

func (s *Server) listPulls(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	var items []map[string]any
	for _, pr := range s.prs {
		if strings.EqualFold(pr.Owner, r.PathValue("owner")) && strings.EqualFold(pr.Repo, r.PathValue("repo")) && pr.State == stateOpen {
			items = append(items, prJSON(pr))
		}
	}
	s.mu.Unlock()
	paginate(w, r, items, asIs)
}

// pull resolves the {owner}/{repo}/{number} path, answering 404 itself.
func (s *Server) pull(w http.ResponseWriter, r *http.Request) *PR {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err == nil {
		if pr := s.find(r.PathValue("owner"), r.PathValue("repo"), n); pr != nil {
			return pr
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{keyMessage: "Not Found"})
	return nil
}

func (s *Server) getPull(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pr := s.pull(w, r); pr != nil {
		writeJSON(w, http.StatusOK, prJSON(pr))
	}
}

func (s *Server) editPull(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State string `json:"state"`
	}
	if err := readBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{keyMessage: err.Error()})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.pull(w, r)
	if pr == nil {
		return
	}
	if body.State != "" {
		pr.State = body.State
	}
	writeJSON(w, http.StatusOK, prJSON(pr))
}

func (s *Server) listCommits(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	pr := s.pull(w, r)
	if pr == nil {
		s.mu.Unlock()
		return
	}
	items := make([]map[string]any, 0, len(pr.Commits))
	for i, c := range pr.Commits {
		items = append(items, map[string]any{"sha": fmt.Sprintf("c%d", i), "author": user(c.Author), "committer": user(c.Committer)})
	}
	s.mu.Unlock()
	paginate(w, r, items, asIs)
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	pr := s.pull(w, r)
	if pr == nil {
		s.mu.Unlock()
		return
	}
	items := make([]map[string]any, 0, len(pr.Comments))
	for _, c := range pr.Comments {
		items = append(items, map[string]any{"id": c.ID, "body": c.Body, keyUser: user(c.Author)})
	}
	s.mu.Unlock()
	paginate(w, r, items, asIs)
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if err := readBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{keyMessage: err.Error()})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.pull(w, r)
	if pr == nil {
		return
	}
	// An installation token posts as the App; anything else is an operator.
	author := "operator"
	if _, ok := installationFor(r); ok {
		author = s.Slug + "[bot]"
	}
	s.commentID++
	c := Comment{ID: s.commentID, Author: author, Body: body.Body}
	pr.Comments = append(pr.Comments, c)
	writeJSON(w, http.StatusCreated, map[string]any{"id": c.ID, "body": c.Body, keyUser: user(c.Author)})
}

func (s *Server) deleteRef(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.PathValue("owner") + "/" + r.PathValue("repo") + ":" + r.PathValue("branch")
	if !s.refs[key] {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{keyMessage: "Reference does not exist"})
		return
	}
	delete(s.refs, key)
	w.WriteHeader(http.StatusNoContent)
}

func prJSON(pr *PR) map[string]any {
	return map[string]any{
		"number":     pr.Number,
		"state":      pr.State,
		"title":      pr.Title,
		"html_url":   fmt.Sprintf("https://github.example/%s/%s/pull/%d", pr.Owner, pr.Repo, pr.Number),
		keyUser:      user(pr.Author),
		"head":       map[string]any{"ref": pr.HeadRef, "sha": pr.HeadSHA, "repo": map[string]any{"id": pr.HeadRepoID}},
		"base":       map[string]any{"repo": map[string]any{"id": pr.BaseRepoID}},
		"created_at": pr.CreatedAt.Format(time.RFC3339),
		"updated_at": pr.UpdatedAt.Format(time.RFC3339),
	}
}

// user renders a login as a GitHub user object, or null when unresolved.
func user(login string) any {
	if login == "" {
		return nil
	}
	return map[string]any{keyLogin: login}
}
