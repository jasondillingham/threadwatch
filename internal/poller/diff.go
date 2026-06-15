// SPDX-License-Identifier: Apache-2.0

// Package poller turns ThreadSnapshots into rows for the events table.
//
// The Diff function is the correctness core of threadwatch. Given the
// caller's view of "what we've already seen for this thread" and a fresh
// snapshot from GitHub, it returns the new events to insert and any
// metadata updates for the thread row.
//
// Diff is intentionally pure: no I/O, no clocks, no globals. The poller
// loop wires it to storage and the GitHub fetcher.
package poller

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jasondillingham/threadwatch/internal/github"
)

// Existing is the caller's projection of what's already recorded for a
// thread. SeenIDs is a set of source IDs keyed by source_kind; both layers
// of map may be nil and that's treated as "have seen nothing".
type Existing struct {
	State       string     // previously-observed state, "" if never observed
	Title       string     // previously-observed title
	Kind        string     // "issue" | "pr" | "unknown" | ""
	LastEventAt *time.Time // most recent event timestamp recorded, nil if none

	// SeenIDs[source_kind] is the set of source_ids already in the events
	// table for that kind. Used for dedup; the body of past events isn't
	// needed.
	SeenIDs map[string]map[string]bool
}

// DiffResult is the output of Diff: the new events to insert plus any
// updates to apply to the threads row.
type DiffResult struct {
	NewEvents []NewEvent
	Update    ThreadUpdate
}

// NewEvent matches the events table columns. RawJSON is left as a
// placeholder (the fetcher's responsibility); body excerpt is computed
// here from Body so the diff layer owns truncation.
type NewEvent struct {
	SourceKind  string // issue_comment | review | review_comment | issue_event | state_change
	SourceID    string
	EventType   string
	Actor       string
	OccurredAt  time.Time
	BodyExcerpt string
	URL         string
	RawJSON     string
}

// ThreadUpdate carries the metadata changes the threads row needs.
// LastEventAt is nil when there's nothing new to record (it does not
// "downgrade" an existing timestamp on the row).
type ThreadUpdate struct {
	State       string
	Title       string
	Kind        string
	LastEventAt *time.Time
}

// excerptLength is the number of UTF-8 runes preserved in BodyExcerpt
// before truncation with ellipsis.
const excerptLength = 280

// interestingIssueEvents are the issue-timeline event types worth
// surfacing in the thread view. Everything else (subscribed, mentioned,
// referenced, head_ref_*, etc.) is noise for our purposes.
var interestingIssueEvents = map[string]bool{
	"labeled":                true,
	"unlabeled":              true,
	"closed":                 true,
	"reopened":               true,
	"merged":                 true,
	"assigned":               true,
	"unassigned":             true,
	"review_requested":       true,
	"review_request_removed": true,
	"ready_for_review":       true,
	"convert_to_draft":       true,
	"milestoned":             true,
	"demilestoned":           true,
	"renamed":                true,
}

// Diff computes the events to insert for `snap` relative to `prev`, plus
// the threads-row updates implied by the new snapshot. Events are
// returned in ascending occurred_at order.
func Diff(prev Existing, snap github.ThreadSnapshot) DiffResult {
	var events []NewEvent

	seen := func(kind, id string) bool {
		m := prev.SeenIDs[kind]
		return m != nil && m[id]
	}

	// Issue comments.
	for _, c := range snap.Comments {
		id := strconv.FormatInt(c.ID, 10)
		if seen("issue_comment", id) {
			continue
		}
		events = append(events, NewEvent{
			SourceKind:  "issue_comment",
			SourceID:    id,
			EventType:   "commented",
			Actor:       c.Author,
			OccurredAt:  c.CreatedAt,
			BodyExcerpt: excerpt(c.Body),
			URL:         c.URL,
		})
	}

	// PR reviews.
	for _, r := range snap.Reviews {
		id := strconv.FormatInt(r.ID, 10)
		if seen("review", id) {
			continue
		}
		events = append(events, NewEvent{
			SourceKind:  "review",
			SourceID:    id,
			EventType:   reviewEventType(r.State),
			Actor:       r.Author,
			OccurredAt:  r.SubmittedAt,
			BodyExcerpt: excerpt(r.Body),
			URL:         r.URL,
		})
	}

	// PR review comments (inline code-review comments).
	for _, rc := range snap.ReviewComments {
		id := strconv.FormatInt(rc.ID, 10)
		if seen("review_comment", id) {
			continue
		}
		events = append(events, NewEvent{
			SourceKind:  "review_comment",
			SourceID:    id,
			EventType:   "review_commented",
			Actor:       rc.Author,
			OccurredAt:  rc.CreatedAt,
			BodyExcerpt: excerpt(rc.Body),
			URL:         rc.URL,
		})
	}

	// Issue events (filtered to interesting types).
	for _, e := range snap.IssueEvents {
		if !interestingIssueEvents[e.Event] {
			continue
		}
		id := strconv.FormatInt(e.ID, 10)
		if seen("issue_event", id) {
			continue
		}
		body := ""
		if e.Label != "" {
			body = e.Label
		}
		events = append(events, NewEvent{
			SourceKind:  "issue_event",
			SourceID:    id,
			EventType:   e.Event,
			Actor:       e.Actor,
			OccurredAt:  e.CreatedAt,
			BodyExcerpt: body,
		})
	}

	// Synthesized state-change event. We only fire when:
	//   1. prev.State is non-empty (no synthesized event on first observation)
	//   2. prev.State differs from snap.State
	//
	// The source_id encodes the new state and the snapshot's updated_at so
	// that reopen-then-close sequences each fire once and re-observing the
	// same state with the same updated_at is deduped.
	if prev.State != "" && prev.State != snap.State {
		id := fmt.Sprintf("state:%s:%s", snap.State, snap.UpdatedAt.UTC().Format(time.RFC3339))
		if !seen("state_change", id) {
			events = append(events, NewEvent{
				SourceKind: "state_change",
				SourceID:   id,
				EventType:  "state_" + snap.State,
				OccurredAt: snap.UpdatedAt,
			})
		}
	}

	sortByOccurredAt(events)

	// LastEventAt: max of new events' OccurredAt, but never less than the
	// previously-recorded value. Stays nil if there are no events at all.
	var lastEvent *time.Time
	for i := range events {
		if lastEvent == nil || events[i].OccurredAt.After(*lastEvent) {
			t := events[i].OccurredAt
			lastEvent = &t
		}
	}
	if lastEvent == nil {
		lastEvent = prev.LastEventAt
	} else if prev.LastEventAt != nil && prev.LastEventAt.After(*lastEvent) {
		lastEvent = prev.LastEventAt
	}

	return DiffResult{
		NewEvents: events,
		Update: ThreadUpdate{
			State:       snap.State,
			Title:       snap.Title,
			Kind:        snap.Kind,
			LastEventAt: lastEvent,
		},
	}
}

// excerpt collapses internal whitespace, trims edges, and truncates body
// to excerptLength runes with an ellipsis if needed. Returns "" for an
// all-whitespace input.
func excerpt(body string) string {
	s := strings.Join(strings.Fields(body), " ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= excerptLength {
		return s
	}
	return string(runes[:excerptLength]) + "…"
}

func reviewEventType(state string) string {
	switch strings.ToUpper(state) {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes_requested"
	case "COMMENTED":
		return "reviewed"
	case "DISMISSED":
		return "dismissed"
	default:
		return strings.ToLower(state)
	}
}

func sortByOccurredAt(events []NewEvent) {
	// Insertion sort is fine for the sizes we see (max ~hundreds per poll).
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j-1].OccurredAt.After(events[j].OccurredAt); j-- {
			events[j-1], events[j] = events[j], events[j-1]
		}
	}
}
