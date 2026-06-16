// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ThreadRef points at one watched thread on GitHub.
type ThreadRef struct {
	Owner  string
	Repo   string
	Number int
}

// Endpoint names used as ETag map keys and metric labels.
const (
	EndpointIssue          = "issue"
	EndpointComments       = "comments"
	EndpointIssueEvents    = "issue_events"
	EndpointReviews        = "reviews"
	EndpointReviewComments = "review_comments"
)

// FetchResult bundles what one FetchThread call produces: the snapshot,
// the per-endpoint ETags to persist for next poll, and the most recent
// rate-limit headers we saw (for metrics + back-off decisions in the
// poller).
type FetchResult struct {
	Snapshot  ThreadSnapshot
	ETags     map[string]string
	RateLimit RateLimit
	// EndpointStatuses records the HTTP status code seen for each endpoint
	// (200 = fresh, 304 = unchanged, etc.) so the poller can persist them
	// into the polls table.
	EndpointStatuses map[string]int
}

// FetchThread hits the 1–5 endpoints needed to fully describe one thread.
// prevETags is keyed by endpoint name (use the Endpoint* constants); pass
// an empty map on first poll.
//
// The function follows a "best effort" policy: if a non-required endpoint
// fails (e.g. reviews for an issue), we log via the error return but the
// snapshot's other fields are still populated. The /issues/{n} call is
// load-bearing — its failure returns an empty snapshot.
func (c *Client) FetchThread(ctx context.Context, ref ThreadRef, prevETags map[string]string) (FetchResult, error) {
	if prevETags == nil {
		prevETags = map[string]string{}
	}
	out := FetchResult{
		ETags:            map[string]string{},
		EndpointStatuses: map[string]int{},
	}

	// 1. /issues/{n} — always.
	issuePath := fmt.Sprintf("/repos/%s/%s/issues/%d", ref.Owner, ref.Repo, ref.Number)
	r, err := c.get(ctx, issuePath, prevETags[EndpointIssue])
	// Record what we learned about the response even on errors so the
	// caller can still surface rate-limit info / observed status.
	out.EndpointStatuses[EndpointIssue] = r.StatusCode
	out.RateLimit = r.RateLimit
	carryETag(out.ETags, prevETags, EndpointIssue, r.ETag)
	if err != nil {
		return out, fmt.Errorf("issue: %w", err)
	}

	var issue issueResource
	if r.StatusCode == 200 {
		if err := decode(r, &issue); err != nil {
			return out, fmt.Errorf("issue: decode: %w", err)
		}
	}
	// On 304 we don't have a fresh issue body. Caller will reuse stored
	// state (kind/title/state) from the threads row.
	if r.StatusCode == 200 {
		out.Snapshot.Owner = ref.Owner
		out.Snapshot.Repo = ref.Repo
		out.Snapshot.Number = ref.Number
		out.Snapshot.Kind, out.Snapshot.State = deriveKindState(issue)
		out.Snapshot.Title = issue.Title
		out.Snapshot.URL = issue.HTMLURL
		out.Snapshot.UpdatedAt = issue.UpdatedAt
	}

	// 2. /issues/{n}/comments
	if err := c.fetchInto(ctx, fmt.Sprintf("%s/comments?per_page=100", issuePath),
		EndpointComments, prevETags, &out, &out.Snapshot.Comments,
		liftComments,
	); err != nil {
		return out, fmt.Errorf("comments: %w", err)
	}

	// 3. /issues/{n}/events
	if err := c.fetchInto(ctx, fmt.Sprintf("%s/events?per_page=100", issuePath),
		EndpointIssueEvents, prevETags, &out, &out.Snapshot.IssueEvents,
		liftIssueEvents,
	); err != nil {
		return out, fmt.Errorf("issue_events: %w", err)
	}

	// 4 & 5. PR-only endpoints. Only fire when we positively know it's a PR.
	if out.Snapshot.Kind == "pr" {
		pullsPath := fmt.Sprintf("/repos/%s/%s/pulls/%d", ref.Owner, ref.Repo, ref.Number)

		if err := c.fetchInto(ctx, fmt.Sprintf("%s/reviews?per_page=100", pullsPath),
			EndpointReviews, prevETags, &out, &out.Snapshot.Reviews,
			liftReviews,
		); err != nil {
			return out, fmt.Errorf("reviews: %w", err)
		}

		if err := c.fetchInto(ctx, fmt.Sprintf("%s/comments?per_page=100", pullsPath),
			EndpointReviewComments, prevETags, &out, &out.Snapshot.ReviewComments,
			liftReviewComments,
		); err != nil {
			return out, fmt.Errorf("review_comments: %w", err)
		}
	}

	return out, nil
}

// fetchInto is the common path for the list endpoints: GET with prior
// ETag, decode into a raw slice, lift into the public type, persist the
// new ETag, record the status. On 304 we leave dst as-is (empty); the
// poller's diff will see "no items" and emit zero events for that
// endpoint, which is the correct behaviour.
func fetchInto[Raw any, Lifted any](
	ctx context.Context,
	c *Client,
	path, endpoint string,
	prevETags map[string]string,
	out *FetchResult,
	dst *[]Lifted,
	lift func([]Raw) []Lifted,
) error {
	r, err := c.get(ctx, path, prevETags[endpoint])
	if err != nil {
		// Squash "not found" for the list endpoints — we don't want a
		// removed sub-resource to abort the whole fetch.
		if errors.Is(err, ErrNotFound) {
			out.EndpointStatuses[endpoint] = r.StatusCode
			return nil
		}
		return err
	}
	out.EndpointStatuses[endpoint] = r.StatusCode
	out.RateLimit = r.RateLimit
	carryETag(out.ETags, prevETags, endpoint, r.ETag)

	if r.StatusCode != 200 {
		return nil
	}
	var raw []Raw
	if err := decode(r, &raw); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	*dst = lift(raw)
	return nil
}

// fetchInto is a method-shaped wrapper around the generic helper so the
// fetcher can call it with familiar method-style syntax.
func (c *Client) fetchInto(
	ctx context.Context,
	path, endpoint string,
	prevETags map[string]string,
	out *FetchResult,
	dstAny any,
	liftAny any,
) error {
	// We dispatch by the concrete `dst` slice type because Go generics can't
	// be parameterized purely by lambda types here.
	switch dst := dstAny.(type) {
	case *[]Comment:
		lift := liftAny.(func([]rawComment) []Comment)
		return fetchInto(ctx, c, path, endpoint, prevETags, out, dst, lift)
	case *[]Review:
		lift := liftAny.(func([]rawReview) []Review)
		return fetchInto(ctx, c, path, endpoint, prevETags, out, dst, lift)
	case *[]ReviewComment:
		lift := liftAny.(func([]rawReviewComment) []ReviewComment)
		return fetchInto(ctx, c, path, endpoint, prevETags, out, dst, lift)
	case *[]IssueEvent:
		lift := liftAny.(func([]rawIssueEvent) []IssueEvent)
		return fetchInto(ctx, c, path, endpoint, prevETags, out, dst, lift)
	default:
		return fmt.Errorf("fetchInto: unsupported dst type %T", dstAny)
	}
}

func carryETag(out, prev map[string]string, key, fresh string) {
	if fresh != "" {
		out[key] = fresh
		return
	}
	if v, ok := prev[key]; ok {
		out[key] = v
	}
}

// ─── JSON shapes ──────────────────────────────────────────────────────────────

type issueResource struct {
	Number      int       `json:"number"`
	State       string    `json:"state"` // "open" | "closed"
	Title       string    `json:"title"`
	HTMLURL     string    `json:"html_url"`
	UpdatedAt   time.Time `json:"updated_at"`
	PullRequest *struct {
		MergedAt *time.Time `json:"merged_at"`
	} `json:"pull_request,omitempty"`
}

type userRef struct {
	Login string `json:"login"`
}

type rawComment struct {
	ID        int64     `json:"id"`
	User      userRef   `json:"user"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
}

type rawReview struct {
	ID          int64     `json:"id"`
	User        userRef   `json:"user"`
	State       string    `json:"state"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	SubmittedAt time.Time `json:"submitted_at"`
}

type rawReviewComment struct {
	ID        int64     `json:"id"`
	User      userRef   `json:"user"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
}

type rawIssueEvent struct {
	ID        int64     `json:"id"`
	Actor     userRef   `json:"actor"`
	Event     string    `json:"event"`
	CreatedAt time.Time `json:"created_at"`
	Label     *struct {
		Name string `json:"name"`
	} `json:"label,omitempty"`
}

func deriveKindState(i issueResource) (kind, state string) {
	if i.PullRequest != nil {
		kind = "pr"
		if i.PullRequest.MergedAt != nil && !i.PullRequest.MergedAt.IsZero() {
			state = "merged"
		} else {
			state = i.State
		}
		return
	}
	return "issue", i.State
}

func liftComments(raw []rawComment) []Comment {
	out := make([]Comment, len(raw))
	for i, r := range raw {
		out[i] = Comment{
			ID:        r.ID,
			Author:    r.User.Login,
			Body:      r.Body,
			URL:       r.HTMLURL,
			CreatedAt: r.CreatedAt,
		}
	}
	return out
}

func liftReviews(raw []rawReview) []Review {
	out := make([]Review, len(raw))
	for i, r := range raw {
		out[i] = Review{
			ID:          r.ID,
			Author:      r.User.Login,
			State:       r.State,
			Body:        r.Body,
			URL:         r.HTMLURL,
			SubmittedAt: r.SubmittedAt,
		}
	}
	return out
}

func liftReviewComments(raw []rawReviewComment) []ReviewComment {
	out := make([]ReviewComment, len(raw))
	for i, r := range raw {
		out[i] = ReviewComment{
			ID:        r.ID,
			Author:    r.User.Login,
			Body:      r.Body,
			URL:       r.HTMLURL,
			CreatedAt: r.CreatedAt,
		}
	}
	return out
}

func liftIssueEvents(raw []rawIssueEvent) []IssueEvent {
	out := make([]IssueEvent, len(raw))
	for i, r := range raw {
		label := ""
		if r.Label != nil {
			label = r.Label.Name
		}
		out[i] = IssueEvent{
			ID:        r.ID,
			Actor:     r.Actor.Login,
			Event:     r.Event,
			Label:     label,
			CreatedAt: r.CreatedAt,
		}
	}
	return out
}
