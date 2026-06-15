// SPDX-License-Identifier: Apache-2.0

// Package github holds the small subset of GitHub API types threadwatch
// needs, plus the HTTP client and per-thread fetcher.
package github

import "time"

// ThreadSnapshot is the full state of one watched thread at a point in
// time, as fetched from GitHub. The diff package compares two snapshots
// to compute new events.
type ThreadSnapshot struct {
	Owner     string
	Repo      string
	Number    int
	Kind      string    // "issue" or "pr"
	State     string    // "open" | "closed" | "merged"
	Title     string
	URL       string
	UpdatedAt time.Time // from /issues/{n}.updated_at

	// Activity streams.
	Comments       []Comment       // /issues/{n}/comments
	IssueEvents    []IssueEvent    // /issues/{n}/events (labeled, closed, etc.)
	Reviews        []Review        // /pulls/{n}/reviews — empty for issues
	ReviewComments []ReviewComment // /pulls/{n}/comments — empty for issues
}

// Comment is one item from /issues/{n}/comments.
type Comment struct {
	ID        int64
	Author    string
	Body      string
	URL       string
	CreatedAt time.Time
}

// Review is one PR review from /pulls/{n}/reviews.
type Review struct {
	ID          int64
	Author      string
	State       string // "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED" | "DISMISSED"
	Body        string
	URL         string
	SubmittedAt time.Time
}

// ReviewComment is one inline code-review comment from /pulls/{n}/comments.
// Distinct from review.body and from issue comments.
type ReviewComment struct {
	ID        int64
	Author    string
	Body      string
	URL       string
	CreatedAt time.Time
}

// IssueEvent is one timeline event from /issues/{n}/events. Many event
// types exist; threadwatch filters to the interesting ones in the diff
// package.
type IssueEvent struct {
	ID        int64
	Actor     string
	Event     string // "labeled" | "closed" | "merged" | "reopened" | ...
	Label     string // populated for labeled/unlabeled
	CreatedAt time.Time
}
