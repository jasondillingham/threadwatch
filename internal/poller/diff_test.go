// SPDX-License-Identifier: Apache-2.0

package poller_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jasondillingham/threadwatch/internal/github"
	"github.com/jasondillingham/threadwatch/internal/poller"
)

// ts is a small helper for building deterministic timestamps. Day 1 = 2026-01-01.
func ts(day int) time.Time {
	return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC)
}

// timePtr returns a pointer to t.
func timePtr(t time.Time) *time.Time { return &t }

// seen builds a Existing.SeenIDs map from "kind:id" tuples.
func seen(pairs ...[2]string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, p := range pairs {
		if out[p[0]] == nil {
			out[p[0]] = map[string]bool{}
		}
		out[p[0]][p[1]] = true
	}
	return out
}

func TestDiff(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		prev poller.Existing
		snap github.ThreadSnapshot

		wantEventKinds []string // (source_kind, source_id) sequence in result order
		wantEventIDs   []string
		wantEventTypes []string
		wantLastEvent  *time.Time
		wantState      string
		wantKind       string
	}{
		{
			name: "empty_snapshot_no_events",
			prev: poller.Existing{State: "open"},
			snap: github.ThreadSnapshot{State: "open", Kind: "issue"},
			// no events expected
			wantLastEvent: nil,
			wantState:     "open",
			wantKind:      "issue",
		},

		{
			name: "first_observation_no_state_event",
			prev: poller.Existing{}, // State == "", never observed
			snap: github.ThreadSnapshot{
				State: "open", Kind: "issue", Title: "hello", UpdatedAt: ts(5),
			},
			// State went from "" to "open" but prev.State is empty, so we do NOT
			// synthesize a state_change event.
			wantLastEvent: nil,
			wantState:     "open",
			wantKind:      "issue",
		},

		{
			name: "single_comment_first_time",
			prev: poller.Existing{State: "open"},
			snap: github.ThreadSnapshot{
				State: "open", Kind: "issue",
				Comments: []github.Comment{
					{ID: 100, Author: "alice", Body: "hello world", CreatedAt: ts(2)},
				},
			},
			wantEventKinds: []string{"issue_comment"},
			wantEventIDs:   []string{"100"},
			wantEventTypes: []string{"commented"},
			wantLastEvent:  timePtr(ts(2)),
			wantState:      "open",
			wantKind:       "issue",
		},

		{
			name: "comment_already_seen_is_skipped",
			prev: poller.Existing{
				State:   "open",
				SeenIDs: seen([2]string{"issue_comment", "100"}),
			},
			snap: github.ThreadSnapshot{
				State: "open", Kind: "issue",
				Comments: []github.Comment{
					{ID: 100, Author: "alice", Body: "hello", CreatedAt: ts(2)},
				},
			},
			// no new events
			wantLastEvent: nil,
			wantState:     "open",
			wantKind:      "issue",
		},

		{
			name: "mix_of_seen_and_unseen_comments",
			prev: poller.Existing{
				State:       "open",
				SeenIDs:     seen([2]string{"issue_comment", "100"}),
				LastEventAt: timePtr(ts(2)),
			},
			snap: github.ThreadSnapshot{
				State: "open", Kind: "issue",
				Comments: []github.Comment{
					{ID: 100, Author: "alice", Body: "first", CreatedAt: ts(2)},
					{ID: 200, Author: "bob", Body: "second", CreatedAt: ts(3)},
					{ID: 300, Author: "carol", Body: "third", CreatedAt: ts(4)},
				},
			},
			wantEventKinds: []string{"issue_comment", "issue_comment"},
			wantEventIDs:   []string{"200", "300"},
			wantEventTypes: []string{"commented", "commented"},
			wantLastEvent:  timePtr(ts(4)),
			wantState:      "open",
			wantKind:       "issue",
		},

		{
			name: "state_change_open_to_closed",
			prev: poller.Existing{State: "open"},
			snap: github.ThreadSnapshot{
				State: "closed", Kind: "issue", UpdatedAt: ts(7),
			},
			wantEventKinds: []string{"state_change"},
			wantEventIDs:   []string{"state:closed:" + ts(7).UTC().Format(time.RFC3339)},
			wantEventTypes: []string{"state_closed"},
			wantLastEvent:  timePtr(ts(7)),
			wantState:      "closed",
			wantKind:       "issue",
		},

		{
			name: "state_change_open_to_merged",
			prev: poller.Existing{State: "open"},
			snap: github.ThreadSnapshot{
				State: "merged", Kind: "pr", UpdatedAt: ts(8),
			},
			wantEventKinds: []string{"state_change"},
			wantEventIDs:   []string{"state:merged:" + ts(8).UTC().Format(time.RFC3339)},
			wantEventTypes: []string{"state_merged"},
			wantLastEvent:  timePtr(ts(8)),
			wantState:      "merged",
			wantKind:       "pr",
		},

		{
			name: "state_change_idempotent_on_re_poll",
			prev: poller.Existing{
				State: "open",
				SeenIDs: seen([2]string{
					"state_change",
					"state:closed:" + ts(7).UTC().Format(time.RFC3339),
				}),
			},
			snap: github.ThreadSnapshot{
				State: "closed", Kind: "issue", UpdatedAt: ts(7),
			},
			// We've already recorded this exact state transition: no new event.
			wantLastEvent: nil,
			wantState:     "closed",
			wantKind:      "issue",
		},

		{
			name: "reopen_then_close_each_fires",
			prev: poller.Existing{
				State: "closed",
				SeenIDs: seen([2]string{
					"state_change",
					"state:closed:" + ts(7).UTC().Format(time.RFC3339),
				}),
			},
			snap: github.ThreadSnapshot{
				State: "open", Kind: "issue", UpdatedAt: ts(9),
			},
			wantEventKinds: []string{"state_change"},
			wantEventIDs:   []string{"state:open:" + ts(9).UTC().Format(time.RFC3339)},
			wantEventTypes: []string{"state_open"},
			wantLastEvent:  timePtr(ts(9)),
			wantState:      "open",
			wantKind:       "issue",
		},

		{
			name: "review_states_map_to_event_types",
			prev: poller.Existing{State: "open"},
			snap: github.ThreadSnapshot{
				State: "open", Kind: "pr",
				Reviews: []github.Review{
					{ID: 10, Author: "a", State: "APPROVED", SubmittedAt: ts(2)},
					{ID: 11, Author: "b", State: "CHANGES_REQUESTED", SubmittedAt: ts(3)},
					{ID: 12, Author: "c", State: "COMMENTED", SubmittedAt: ts(4)},
					{ID: 13, Author: "d", State: "DISMISSED", SubmittedAt: ts(5)},
				},
			},
			wantEventKinds: []string{"review", "review", "review", "review"},
			wantEventIDs:   []string{"10", "11", "12", "13"},
			wantEventTypes: []string{"approved", "changes_requested", "reviewed", "dismissed"},
			wantLastEvent:  timePtr(ts(5)),
			wantState:      "open",
			wantKind:       "pr",
		},

		{
			name: "uninteresting_issue_events_filtered_out",
			prev: poller.Existing{State: "open"},
			snap: github.ThreadSnapshot{
				State: "open", Kind: "issue",
				IssueEvents: []github.IssueEvent{
					{ID: 1, Event: "subscribed", CreatedAt: ts(2)},
					{ID: 2, Event: "mentioned", CreatedAt: ts(2)},
					{ID: 3, Event: "labeled", Actor: "alice", Label: "bug", CreatedAt: ts(3)},
					{ID: 4, Event: "head_ref_force_pushed", CreatedAt: ts(3)},
					{ID: 5, Event: "closed", Actor: "alice", CreatedAt: ts(4)},
				},
			},
			wantEventKinds: []string{"issue_event", "issue_event"},
			wantEventIDs:   []string{"3", "5"},
			wantEventTypes: []string{"labeled", "closed"},
			wantLastEvent:  timePtr(ts(4)),
			wantState:      "open",
			wantKind:       "issue",
		},

		{
			name: "mix_comments_reviews_state_change_returned_in_time_order",
			prev: poller.Existing{State: "open"},
			snap: github.ThreadSnapshot{
				State: "closed", Kind: "pr", UpdatedAt: ts(6),
				Comments: []github.Comment{
					{ID: 1, Author: "a", Body: "early comment", CreatedAt: ts(2)},
				},
				Reviews: []github.Review{
					{ID: 10, Author: "b", State: "APPROVED", SubmittedAt: ts(4)},
				},
				ReviewComments: []github.ReviewComment{
					{ID: 100, Author: "c", Body: "line nit", CreatedAt: ts(3)},
				},
				IssueEvents: []github.IssueEvent{
					{ID: 1000, Event: "labeled", Actor: "d", Label: "needs-review", CreatedAt: ts(5)},
				},
			},
			wantEventKinds: []string{
				"issue_comment", "review_comment", "review", "issue_event", "state_change",
			},
			wantEventIDs: []string{
				"1", "100", "10", "1000",
				"state:closed:" + ts(6).UTC().Format(time.RFC3339),
			},
			wantEventTypes: []string{"commented", "review_commented", "approved", "labeled", "state_closed"},
			wantLastEvent:  timePtr(ts(6)),
			wantState:      "closed",
			wantKind:       "pr",
		},

		{
			name: "no_new_events_preserves_prev_last_event_at",
			prev: poller.Existing{
				State:       "open",
				LastEventAt: timePtr(ts(2)),
			},
			snap: github.ThreadSnapshot{State: "open", Kind: "issue"},
			// no new events but threads.last_event_at must NOT be overwritten with nil
			wantLastEvent: timePtr(ts(2)),
			wantState:     "open",
			wantKind:      "issue",
		},

		{
			name: "older_event_does_not_downgrade_last_event_at",
			prev: poller.Existing{
				State:       "open",
				LastEventAt: timePtr(ts(10)),
			},
			snap: github.ThreadSnapshot{
				State: "open", Kind: "issue",
				Comments: []github.Comment{
					// arrives "out of order" — older than prev recorded max
					{ID: 999, Author: "a", Body: "late late late", CreatedAt: ts(3)},
				},
			},
			wantEventKinds: []string{"issue_comment"},
			wantEventIDs:   []string{"999"},
			wantEventTypes: []string{"commented"},
			// new event is older than prev.LastEventAt, so prev wins
			wantLastEvent: timePtr(ts(10)),
			wantState:     "open",
			wantKind:      "issue",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := poller.Diff(tc.prev, tc.snap)

			if len(got.NewEvents) != len(tc.wantEventKinds) {
				t.Fatalf("event count: got %d, want %d (got=%+v)",
					len(got.NewEvents), len(tc.wantEventKinds), eventSummaries(got.NewEvents))
			}
			for i, e := range got.NewEvents {
				if e.SourceKind != tc.wantEventKinds[i] {
					t.Errorf("event[%d].SourceKind: got %q, want %q", i, e.SourceKind, tc.wantEventKinds[i])
				}
				if e.SourceID != tc.wantEventIDs[i] {
					t.Errorf("event[%d].SourceID: got %q, want %q", i, e.SourceID, tc.wantEventIDs[i])
				}
				if e.EventType != tc.wantEventTypes[i] {
					t.Errorf("event[%d].EventType: got %q, want %q", i, e.EventType, tc.wantEventTypes[i])
				}
			}

			if !equalTimePtr(got.Update.LastEventAt, tc.wantLastEvent) {
				t.Errorf("LastEventAt: got %v, want %v", fmtTime(got.Update.LastEventAt), fmtTime(tc.wantLastEvent))
			}
			if got.Update.State != tc.wantState {
				t.Errorf("Update.State: got %q, want %q", got.Update.State, tc.wantState)
			}
			if got.Update.Kind != tc.wantKind {
				t.Errorf("Update.Kind: got %q, want %q", got.Update.Kind, tc.wantKind)
			}
		})
	}
}

func TestExcerptTruncation(t *testing.T) {
	// excerpt() is the body-truncation helper. Internal whitespace collapses;
	// long bodies trim to 280 runes plus ellipsis.
	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty_returns_empty", "", ""},
		{"whitespace_only_returns_empty", "   \n\n\t  ", ""},
		{"normalizes_internal_whitespace", "hello   world\nnext   line", "hello world next line"},
		{"trims_edges", "  hi  ", "hi"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			prev := poller.Existing{State: "open"}
			snap := github.ThreadSnapshot{
				State: "open", Kind: "issue",
				Comments: []github.Comment{
					{ID: 1, Body: c.body, CreatedAt: ts(2)},
				},
			}
			got := poller.Diff(prev, snap)
			if len(got.NewEvents) != 1 {
				t.Fatalf("want one event, got %d", len(got.NewEvents))
			}
			if got.NewEvents[0].BodyExcerpt != c.want {
				t.Errorf("excerpt: got %q, want %q", got.NewEvents[0].BodyExcerpt, c.want)
			}
		})
	}

	// Long body gets truncated to 280 runes plus the horizontal ellipsis.
	t.Run("truncates_at_280_runes", func(t *testing.T) {
		t.Parallel()
		body := strings.Repeat("a", 500)
		prev := poller.Existing{State: "open"}
		snap := github.ThreadSnapshot{
			State: "open", Kind: "issue",
			Comments: []github.Comment{
				{ID: 1, Body: body, CreatedAt: ts(2)},
			},
		}
		got := poller.Diff(prev, snap)
		ex := got.NewEvents[0].BodyExcerpt
		runes := []rune(ex)
		if len(runes) != 281 {
			t.Fatalf("excerpt rune count: got %d, want 281 (280 + ellipsis)", len(runes))
		}
		if runes[280] != '…' {
			t.Errorf("expected trailing horizontal ellipsis, got %q", string(runes[280]))
		}
	})
}

// --- small helpers ---

func equalTimePtr(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "<nil>"
	}
	return t.Format(time.RFC3339)
}

type eventSum struct {
	Kind, ID, Type string
}

func eventSummaries(events []poller.NewEvent) []eventSum {
	out := make([]eventSum, len(events))
	for i, e := range events {
		out[i] = eventSum{e.SourceKind, e.SourceID, e.EventType}
	}
	return out
}
