# Screenshots

Drop UI screenshots here and reference them from the top-level README.

Needed for Checkpoint D item 3.8 (capture from the running instance —
`kubectl -n threadwatch port-forward deploy/threadwatch 8080:8080`, then visit
http://localhost:8080):

- `index.png` — the index page: all watched threads, last-event and
  days-quiet columns.
- `thread.png` — a thread detail page: the event timeline with event types
  (comment / review / state change).

Once added, reference them in `README.md`, e.g.:

```markdown
![threadwatch index](docs/screenshots/index.png)
![thread detail](docs/screenshots/thread.png)
```
